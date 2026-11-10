package pipeline

import (
	"context"
	"log/slog"

	"github.com/shouni/ap-voice/internal/domain"
)

// notifySuccess sends the notification for a successful run.
func (p *Runner) notifySuccess(ctx context.Context, req domain.Request, publicURL string) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.Notify(ctx, req, publicURL); err != nil {
		slog.Error("notification failed; continuing", "error", err, "output_uri", req.OutputURI)
	}
}

// notifyFailure sends the notification for a failed run.
func (p *Runner) notifyFailure(ctx context.Context, req domain.Request, runErr error) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.NotifyFailure(ctx, req, runErr); err != nil {
		slog.Error("failure notification failed; continuing", "error", err, "cause", runErr)
	}
}
