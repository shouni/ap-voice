package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/pprof"
	"time"

	"github.com/shouni/go-job-firestore/jobfirestore"

	"github.com/shouni/ap-voice/internal/domain"
)

// Pipeline はパイプラインの実行に必要な外部依存関係を保持するサービス構造体です。
type Pipeline struct {
	generator scriptGenerator
	publisher publisher
	notifier  domain.Notifier
	// scripts は保存済み台本の読み出しです。synthesize が JobID だけを渡されたときに使います。
	scripts domain.ScriptStore
	// status はジョブの進行状況です。nil でも動きます（記録しないだけ）。
	status *jobfirestore.Recorder[domain.JobStatus]
	// timeout はジョブ 1 件の実行時間の上限です。0 以下は無制限を意味します。
	timeout time.Duration
}

// NewPipeline は、Pipeline を生成します。
func NewPipeline(generator scriptGenerator, publisher publisher, notifier domain.Notifier, scripts domain.ScriptStore, status *jobfirestore.Recorder[domain.JobStatus], timeout time.Duration) *Pipeline {
	return &Pipeline{
		generator: generator,
		publisher: publisher,
		notifier:  notifier,
		scripts:   scripts,
		status:    status,
		timeout:   timeout,
	}
}

// record は、ジョブの状態を書きます。記録の失敗で処理は止めません—
// 状態は進行を知るためのもので、成果物より重くはありません。
func (p *Pipeline) record(ctx context.Context, req domain.Request, state jobfirestore.State, apply ...func(next, prev *domain.JobStatus)) {
	if p.status == nil || req.JobID == "" {
		return
	}
	p.status.Record(ctx, req.JobID, domain.NewJobStatus(req, state), apply...)
}

// begin は、そのジョブが既に完了していれば true を返し、未完了なら処理開始を記録して
// false を返します。判定と記録を前回の記録の 1 回の読みで行うので、間に別の配信が
// 割り込む隙がありません。試行回数はここで 1 つ進みます（2 以上なら再配信されたということです）。
//
// 状態を読めなかった場合はエラーを返し、記録もしません。「未完了」に倒すと完了済みの
// ジョブを作り直してこのガードが防ぐはずの時間を自分で使い、「完了済み」に倒すと未完了の
// ジョブがタスクごと ACK されて二度と実行されません。どちらへも倒さず、判断を
// 呼び出し側へ返します。
func (p *Pipeline) begin(ctx context.Context, req domain.Request) (bool, error) {
	if p.status == nil || req.JobID == "" {
		return false, nil
	}

	done, err := p.status.Begin(ctx, req.JobID, domain.NewJobStatus(req, jobfirestore.StateRunning),
		func(next, prev *domain.JobStatus) {
			next.Attempts++
			next.CarryFrom(prev)
		})
	if err != nil {
		return false, fmt.Errorf("ジョブ状態を読めないため実行を見送ります (%s): %w", req.JobID, err)
	}
	return done, nil
}

// Execute は、すべての依存関係を構築し実行します。
func (p *Pipeline) Execute(ctx context.Context, req domain.Request) (err error) {
	// pprof のゴルーチンラベルにジョブを載せます。Go 1.27 以降、ラベルは
	// パニックのトレースバックの見出し行にも出るため、落ちたときにどのジョブ
	// だったかがスタックだけで分かります。ラベルは子ゴルーチン（セグメントの
	// 並列合成）へも継承されます。
	ctx = pprof.WithLabels(ctx, pprof.Labels("job_id", req.JobID, "command", string(req.Command)))
	pprof.SetGoroutineLabels(ctx)

	// 完了済みのジョブをもう一度受け取ったら、ここで打ち切ります。
	// Cloud Tasks は at-least-once 配信なので、同じタスクが再び届くことがあります。
	// 素通りさせると、数分かかる合成で同じ音声を作り直すことになります。
	//
	// 作り直しの投入がここで止まることはありません。投入経路はどれも enqueue の前に
	// queued を書くため（handlers.recordQueued）、同じジョブ ID への 2 度目の依頼は
	// succeeded ではなく queued として届きます。succeeded のまま届くのは、
	// ハンドラーを経由しない再配信だけです。
	//
	// 失敗を記録する defer より前に置きます。あとに置くと、状態を読めなかった
	// ときの return が failed の記録を呼び、succeeded だったかもしれない記録を
	// 上書きします。次の配信でこのガードが効かなくなり、防ぐはずの再実行を
	// ガード自身が招きます。
	done, guardErr := p.begin(ctx, req)
	if guardErr != nil {
		return guardErr
	}
	if done {
		slog.InfoContext(ctx, "完了済みのジョブなので実行しません",
			"job_id", req.JobID, "command", req.Command)
		return nil
	}

	// 通知は打ち切りの外側で送ります。下で ctx に上限を掛けるため、期限切れで
	// 抜けたときには ctx は既にキャンセル済みです。そのまま通知に使うと通知自体が
	// 送れず、「先に諦めて失敗を知らせる」という目的が達成できません。
	notifyCtx := ctx

	// アプリが自分で先に諦めます。Cloud Tasks の dispatch_deadline より内側で
	// 打ち切ることで、下の defer が失敗通知を出す余地を残します。逆順だと
	// Cloud Tasks が先にリクエストを打ち切り、プロセスは SIGTERM で落ちて
	// 通知の機会を失います。再配信が来ても、記録が running のままなので
	// 失敗として観測できません。
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	defer func() {
		if err != nil {
			// 通知と同じ ctx を使います。打ち切り済みの ctx で書くと、
			// 失敗したことすら記録に残りません。
			p.record(notifyCtx, req, jobfirestore.StateFailed, func(next, prev *domain.JobStatus) {
				next.Error = err.Error()
				// 前回までの成果物は残ります。合成に失敗しても台本は既に
				// 保存されているので、在り処を消すと詳細画面からやり直せません。
				next.CarryFrom(prev)
			})
			p.notifyFailure(notifyCtx, req, err)
		}
	}()

	if err = req.Validate(); err != nil {
		return err
	}

	var script domain.Script
	script, err = p.resolveScript(ctx, req)
	if err != nil {
		return err
	}

	var publicURL string
	publicURL, err = p.publish(ctx, req, script)
	if err != nil {
		err = fmt.Errorf("公開処理の実行に失敗しました: %w", err)
		return err
	}

	p.record(notifyCtx, req, jobfirestore.StateSucceeded, func(next, prev *domain.JobStatus) {
		next.Title = script.Title
		next.CarryFrom(prev)

		// 成果物の在り処を状態に載せます。「できた」だけでは、投入した側が
		// 音声へ辿り着けません。台本はどの経路でも保存され、音声は publish が
		// 作ったときだけです（generate は台本までで終わります）。
		layout := domain.NewStorageLayout()
		next.ScriptURI = layout.ScriptURIFor(req.OutputURI)
		if req.Command != domain.CommandGenerate {
			next.AudioURI = req.OutputURI
		}
	})
	p.notifySuccess(notifyCtx, req, publicURL)

	return nil
}

// publish は、Command に応じて保存する成果物を切り替えます。
//
// generate は台本まで、synthesize は音声まで。generate で音声を作らないのは、
// 台本を確認・修正してから合成へ進めるようにするためです。
func (p *Pipeline) publish(ctx context.Context, req domain.Request, script domain.Script) (string, error) {
	if req.Command == domain.CommandGenerate {
		return p.publisher.PublishScript(ctx, req.OutputURI, script)
	}
	return p.publisher.Run(ctx, req.OutputURI, script)
}

// resolveScript は、Command に応じて合成対象の台本を用意します。
//
// generate は入力ソースから作ります。synthesize は JobID で保存済みの台本を読みます
// （台本はペイロードに載りません。理由は domain.Request.JobID）。持ち込みの台本も
// 投入側が先に保存するので、読み出しは 1 経路です — ペイロードの台本を優先する分岐が
// かつてありましたが、投入側がどれも台本を載せなくなったあとは、テストだけが
// 通る道になっていました。
func (p *Pipeline) resolveScript(ctx context.Context, req domain.Request) (domain.Script, error) {
	if req.Command == domain.CommandSynthesize {
		script, err := p.scripts.Load(ctx, req.JobID)
		if err != nil {
			return domain.Script{}, fmt.Errorf("保存済み台本の読み込みに失敗しました (%s): %w", req.JobID, err)
		}
		if len(script.Lines) == 0 {
			return domain.Script{}, fmt.Errorf("保存済み台本が空です (%s)", req.JobID)
		}
		return script, nil
	}

	script, err := p.generator.Run(ctx, req)
	if err != nil {
		return domain.Script{}, fmt.Errorf("スクリプトテキスト作成に失敗しました: %w", err)
	}
	if len(script.Lines) == 0 {
		return domain.Script{}, fmt.Errorf("AIモデルが空のスクリプトを返しました。プロンプトや入力コンテンツに問題がないか確認してください")
	}

	return script, nil
}
