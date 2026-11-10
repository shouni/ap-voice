// Package pipeline is the worker that generates scripts and synthesises audio.
package pipeline

import (
	"context"

	"github.com/shouni/ap-voice/internal/domain"
)

// Step is one step of the worker. Only planner.go (DefaultPlanner.Plan) knows which
// command becomes which sequence of steps.
//
// It lives alongside its implementations (ScriptStep / LoadScriptStep / PublishStep), so there
// is no need to implement it from outside. It is exported so that tests can swap the steps and
// exercise only the Runner's control flow.
type Step interface {
	Name() string
	Execute(ctx context.Context, sc *Context) error
}

// Context is the execution context handed from step to step. Request is fixed,
// and the steps fill in the rest in order.
type Context struct {
	Request domain.Request
	// Script is the generated or loaded script (filled by ScriptStep / LoadScriptStep).
	Script domain.Script
	// PublicURL is the public URL of the artifact (filled by PublishStep; may be absent).
	PublicURL string
}
