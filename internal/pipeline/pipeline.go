package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-job-kit/jobstatus"

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
	status *jobstatus.Recorder[jobstatus.Status]
	// timeout はジョブ 1 件の実行時間の上限です。0 以下は無制限を意味します。
	timeout time.Duration
}

// NewPipeline は、Pipeline を生成します。
func NewPipeline(generator scriptGenerator, publisher publisher, notifier domain.Notifier, scripts domain.ScriptStore, status *jobstatus.Recorder[jobstatus.Status], timeout time.Duration) *Pipeline {
	return &Pipeline{
		generator: generator,
		publisher: publisher,
		notifier:  notifier,
		scripts:   scripts,
		status:    status,
		timeout:   timeout,
	}
}

// record は、ジョブの状態を書きます。**記録の失敗で処理は止めません** —
// 状態は進行を知るためのもので、成果物より重くはありません。
func (p *Pipeline) record(ctx context.Context, req domain.Request, state jobstatus.State, apply ...func(next, prev *jobstatus.Status)) {
	if p.status == nil || req.JobID == "" {
		return
	}
	p.status.Record(ctx, req.JobID, jobstatus.Status{
		JobID:   req.JobID,
		Command: string(req.Command),
		State:   state,
	}, apply...)
}

// Execute は、すべての依存関係を構築し実行します。
func (p *Pipeline) Execute(ctx context.Context, req domain.Request) (err error) {
	// **通知は打ち切りの外側で送ります。** 下で ctx に上限を掛けるため、期限切れで
	// 抜けたときには ctx は既にキャンセル済みです。そのまま通知に使うと通知自体が
	// 送れず、「先に諦めて失敗を知らせる」という目的が達成できません。
	notifyCtx := ctx

	// **アプリが自分で先に諦めます。** Cloud Tasks の dispatch_deadline より内側で
	// 打ち切ることで、下の defer が失敗通知を出す余地を残します。逆順だと
	// Cloud Tasks が先にリクエストを打ち切り、プロセスは SIGTERM で落ちて
	// 通知の機会を失います（キューは max_attempts = 1 なので再試行も来ません）。
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	defer func() {
		if err != nil {
			// **通知と同じ ctx を使います。** 打ち切り済みの ctx で書くと、
			// 失敗したことすら記録に残りません。
			p.record(notifyCtx, req, jobstatus.StateFailed, func(next, _ *jobstatus.Status) {
				next.Error = err.Error()
			})
			p.notifyFailure(notifyCtx, req, err)
		}
	}()

	// 試行回数を進めます。2 以上なら再配信されたということです。
	p.record(ctx, req, jobstatus.StateRunning, func(next, _ *jobstatus.Status) {
		next.Attempts++
	})

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

	p.record(notifyCtx, req, jobstatus.StateSucceeded, func(next, _ *jobstatus.Status) {
		next.Title = script.Title
	})
	p.notifySuccess(notifyCtx, req, publicURL)

	return nil
}

// publish は、Command に応じて保存する成果物を切り替えます。
//
// generate は台本まで、synthesize は音声まで。**generate で音声を作らない**のは、
// 台本を確認・修正してから合成へ進めるようにするためです。
func (p *Pipeline) publish(ctx context.Context, req domain.Request, script domain.Script) (string, error) {
	if req.Command == domain.CommandGenerate {
		return p.publisher.PublishScript(ctx, req.OutputURI, script)
	}
	return p.publisher.Run(ctx, req.OutputURI, script)
}

// resolveScript は、Command に応じて合成対象の台本を用意します。
//
// generate は入力ソースから作ります。synthesize は渡された台本を使い、
// 無ければ JobID で保存済みのものを読みます。**台本をタスクのペイロードで
// 運ばない**のは、長い台本が Cloud Tasks の 1MB 上限に当たりうるためです。
func (p *Pipeline) resolveScript(ctx context.Context, req domain.Request) (domain.Script, error) {
	if req.Command == domain.CommandSynthesize {
		// API から直接渡された台本を優先します。タイトルは保存済みのものを引き継ぎます。
		if len(req.Script) > 0 {
			stored, err := p.scripts.Load(ctx, req.JobID)
			if err != nil {
				// 新規の貼り戻しならまだ保存されていないこともあります。
				return domain.Script{Lines: req.Script}, nil
			}
			return domain.Script{Title: stored.Title, Lines: req.Script}, nil
		}

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
