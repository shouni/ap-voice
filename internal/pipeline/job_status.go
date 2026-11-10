package pipeline

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-voice/internal/domain"
)

// begin returns true if the job is already complete; otherwise it records the start and
// returns false. Deciding and recording use a single read of the previous record, so no other
// delivery can slip in between. The attempt count advances by one here (2 or more means it was redelivered).
//
// If the state cannot be read, it returns an error and records nothing. Falling to "incomplete"
// would remake a finished job and spend the very time this guard exists to save; falling to "complete"
// would ACK an unfinished job with its task so it never runs again. It falls to neither: it tells a
// person about the failure and returns the decision to the caller.
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
		err = fmt.Errorf("skipping the run because the job state cannot be read (%s): %w", req.JobID, err)
		p.notifyFailure(ctx, req, err)
		return false, err
	}
	return done, nil
}

// finish records the outcome and notifies. Success and failure take the same path: record, then notify.
// Lifecycle detaches it from the caller's context.
func (p *Runner) finish(ctx context.Context, req domain.Request, result runResult, cause error) error {
	if cause != nil {
		p.record(ctx, req, jobstatus.StateFailed, func(next, prev *domain.JobStatus) {
			next.Error = cause.Error()
			// Earlier artifacts remain. Even when synthesis fails, the script is already
			// stored, so clearing the location would make it impossible to retry from the detail page.
			next.CarryFrom(prev)
		})
		p.notifyFailure(ctx, req, cause)
		return cause
	}

	p.record(ctx, req, jobstatus.StateSucceeded, func(next, prev *domain.JobStatus) {
		next.Title = result.script.Title
		next.CarryFrom(prev)

		// The artifact locations go on the state. "Done" alone does not let the enqueuer
		// reach the audio. The script is stored on every path, the audio only when publish
		// made it (generate ends at the script).
		layout := domain.NewStorageLayout()
		next.ScriptURI = layout.ScriptURIFor(req.OutputURI)
		if req.Command != domain.CommandGenerate {
			next.AudioURI = req.OutputURI
		}
	})
	p.notifySuccess(ctx, req, result.publicURL)
	return nil
}

// record writes the job state. A failed write does not stop processing —
// the state is for following progress and weighs less than the artifacts.
func (p *Runner) record(ctx context.Context, req domain.Request, state jobstatus.State, apply ...func(next, prev *domain.JobStatus)) {
	if p.status == nil || req.JobID == "" {
		return
	}
	p.status.Record(ctx, req.JobID, domain.NewJobStatus(req, state), apply...)
}
