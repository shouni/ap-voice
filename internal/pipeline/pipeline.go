package pipeline

import (
	"context"
	"time"

	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/domain"
)

// Runner is the worker that generates scripts and synthesises audio.
//
// A job's life (redelivery guard → validation → execution → recording the outcome) is owned by
// gcp-kit/worker.Lifecycle; this only supplies what goes in each stage (public-docs worker conventions).
type Runner struct {
	// generate is the step that writes the script, publish the step that stores and synthesises.
	generate Step
	publish  Step
	notifier domain.Notifier
	// scripts reads stored scripts. synthesize's LoadScriptStep uses it.
	scripts domain.ScriptStore
	// status is the job's progress. It works when nil (it just records nothing).
	status *jobstatus.Recorder[domain.JobStatus]
	// timeout is the cap on one job's run time. Zero or less means unlimited.
	timeout time.Duration
}

// NewRunner creates a Runner.
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

// planner is the Planner that maps a command to steps. Tests build the Runner as a
// struct literal, so it is built on every call rather than at construction.
func (p *Runner) planner() Planner {
	return DefaultPlanner{
		Generate: p.generate,
		Load:     NewLoadScriptStep(p.scripts),
		Publish:  p.publish,
	}
}

// runResult is what the execution hands to the outcome recording.
type runResult struct {
	script    domain.Script
	publicURL string
}

// Execute satisfies worker.TaskExecutor.
func (p *Runner) Execute(ctx context.Context, req domain.Request) error {
	return p.lifecycle().Execute(ctx, req)
}

// lifecycle fills each stage of a job's life with this app's logic.
func (p *Runner) lifecycle() worker.Lifecycle[domain.Request, runResult] {
	return worker.Lifecycle[domain.Request, runResult]{
		Labels: func(req domain.Request) map[string]string {
			return map[string]string{"job_id": req.JobID, "command": string(req.Command)}
		},
		Begin: p.begin,
		// If the request itself is invalid, redelivery fails on the same line (Lifecycle wraps it in Permanent).
		Validate: func(req domain.Request) error { return req.Validate() },
		Run:      p.run,
		Finish:   p.finish,
		Timeout:  p.timeout,
	}
}

// run executes the steps for the Command in order.
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
