package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/domain"
)

// Runner は、台本生成と音声合成のワーカー本体です。
//
// ジョブの一生（再配信ガード → 検証 → 実行 → 結末の記録）は gcp-kit/worker.Lifecycle が
// 持ち、ここはそれぞれの中身だけを渡します（public-docs のワーカー規約）。
type Runner struct {
	// generate は台本を作る工程、publish は保存と合成の工程です。
	generate Step
	publish  Step
	notifier domain.Notifier
	// scripts は保存済み台本の読み出しです。synthesize の LoadScriptStep が使います。
	scripts domain.ScriptStore
	// status はジョブの進行状況です。nil でも動きます（記録しないだけ）。
	status *jobstatus.Recorder[domain.JobStatus]
	// timeout はジョブ 1 件の実行時間の上限です。0 以下は無制限を意味します。
	timeout time.Duration
}

// NewRunner は、Runner を生成します。
func NewRunner(generate, publish Step, notifier domain.Notifier, scripts domain.ScriptStore, status *jobstatus.Recorder[domain.JobStatus], timeout time.Duration) *Runner {
	return &Runner{
		generate: generate,
		publish:  publish,
		notifier: notifier,
		scripts:  scripts,
		status:   status,
		timeout:  timeout,
	}
}

// planner は、コマンドから工程列を決める Planner です。テストが Runner を
// 構造体リテラルで組むので、構築時ではなく呼ばれるたびに組みます。
func (p *Runner) planner() Planner {
	return DefaultPlanner{
		Generate: p.generate,
		Load:     NewLoadScriptStep(p.scripts),
		Publish:  p.publish,
	}
}

// runResult は、実行本体が結末の記録へ渡すものです。
type runResult struct {
	script    domain.Script
	publicURL string
}

// Execute は worker.TaskExecutor を満たします。
func (p *Runner) Execute(ctx context.Context, req domain.Request) error {
	return p.lifecycle().Execute(ctx, req)
}

// lifecycle は、ジョブの一生の各段にこのアプリの中身を当てはめます。
func (p *Runner) lifecycle() worker.Lifecycle[domain.Request, runResult] {
	return worker.Lifecycle[domain.Request, runResult]{
		Labels: func(req domain.Request) map[string]string {
			return map[string]string{"job_id": req.JobID, "command": string(req.Command)}
		},
		Begin: p.begin,
		// 依頼そのものが不正なら、配り直しても同じ行で落ちます（Lifecycle が Permanent に包みます）。
		Validate: func(req domain.Request) error { return req.Validate() },
		Run:      p.run,
		Finish:   p.finish,
		Timeout:  p.timeout,
	}
}

// begin は、そのジョブが既に完了していれば true を返し、未完了なら処理開始を記録して
// false を返します。判定と記録を前回の記録の 1 回の読みで行うので、間に別の配信が
// 割り込む隙がありません。試行回数はここで 1 つ進みます（2 以上なら再配信されたということです）。
//
// 状態を読めなかった場合はエラーを返し、記録もしません。「未完了」に倒すと完了済みの
// ジョブを作り直してこのガードが防ぐはずの時間を自分で使い、「完了済み」に倒すと未完了の
// ジョブがタスクごと ACK されて二度と実行されません。どちらへも倒さず、失敗を人に
// 知らせてから判断を呼び出し側へ返します。
func (p *Runner) begin(ctx context.Context, req domain.Request) (bool, error) {
	if p.status == nil || req.JobID == "" {
		return false, nil
	}

	done, err := p.status.Begin(ctx, req.JobID, domain.NewJobStatus(req, jobstatus.StateRunning),
		func(next, prev *domain.JobStatus) {
			next.Attempts++
			next.CarryFrom(prev)
		})
	if err != nil {
		err = fmt.Errorf("ジョブ状態を読めないため実行を見送ります (%s): %w", req.JobID, err)
		p.notifyFailure(ctx, req, err)
		return false, err
	}
	return done, nil
}

// run は、Command に対応する工程列を順に実行します。
func (p *Runner) run(ctx context.Context, req domain.Request) (runResult, error) {
	steps, err := p.planner().Plan(req)
	if err != nil {
		return runResult{}, err
	}

	sc := &Context{Request: req}
	for _, st := range steps {
		if err := st.Execute(ctx, sc); err != nil {
			return runResult{}, err
		}
	}
	return runResult{script: sc.Script, publicURL: sc.PublicURL}, nil
}

// finish は、結末を記録して通知します。成功も失敗も同じ経路で、記録 → 通知の順です。
// 呼び出し元の context からは Lifecycle が切り離してくれます。
func (p *Runner) finish(ctx context.Context, req domain.Request, result runResult, cause error) error {
	if cause != nil {
		p.record(ctx, req, jobstatus.StateFailed, func(next, prev *domain.JobStatus) {
			next.Error = cause.Error()
			// 前回までの成果物は残ります。合成に失敗しても台本は既に
			// 保存されているので、在り処を消すと詳細画面からやり直せません。
			next.CarryFrom(prev)
		})
		p.notifyFailure(ctx, req, cause)
		return cause
	}

	p.record(ctx, req, jobstatus.StateSucceeded, func(next, prev *domain.JobStatus) {
		next.Title = result.script.Title
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
	p.notifySuccess(ctx, req, result.publicURL)
	return nil
}

// record は、ジョブの状態を書きます。記録の失敗で処理は止めません—
// 状態は進行を知るためのもので、成果物より重くはありません。
func (p *Runner) record(ctx context.Context, req domain.Request, state jobstatus.State, apply ...func(next, prev *domain.JobStatus)) {
	if p.status == nil || req.JobID == "" {
		return
	}
	p.status.Record(ctx, req.JobID, domain.NewJobStatus(req, state), apply...)
}
