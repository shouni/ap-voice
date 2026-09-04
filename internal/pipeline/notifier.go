package pipeline

import (
	"context"
	"log/slog"

	"github.com/shouni/ap-voice/internal/domain"
)

// notifySuccess は、処理成功の通知を送信します。
func (p *Runner) notifySuccess(ctx context.Context, req domain.Request, publicURL string) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.Notify(ctx, req, publicURL); err != nil {
		slog.Error("通知の実行中にエラーが発生しましたが、処理を続行します。", "error", err, "output_uri", req.OutputURI)
	}
}

// notifyFailure は、処理失敗の通知を送信します。
func (p *Runner) notifyFailure(ctx context.Context, req domain.Request, runErr error) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.NotifyFailure(ctx, req, runErr); err != nil {
		slog.Error("失敗通知の実行中にエラーが発生しましたが、処理を続行します。", "error", err, "cause", runErr)
	}
}
