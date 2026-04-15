package pipeline

import (
	"context"
	"log/slog"

	"ap-voice/internal/domain"
)

// notifySuccess は、処理成功の通知を送信します。
func (p *Pipeline) notifySuccess(ctx context.Context, req domain.Request, publicURL string) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.Notify(ctx, req, publicURL); err != nil {
		slog.Error("Slack通知の実行中にエラーが発生しましたが、処理を続行します。", "error", err, "output_uri", req.OutputURI)
	}
}

// notifyFailure は、処理失敗の通知を送信します。
func (p *Pipeline) notifyFailure(ctx context.Context, req domain.Request, runErr error) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.NotifyFailure(ctx, req, runErr); err != nil {
		slog.Error("Slackへの失敗通知の実行中にエラーが発生しましたが、処理を続行します。", "error", err, "cause", runErr)
	}
}

// notifySkipped は、処理スキップの通知を送信します。
func (p *Pipeline) notifySkipped(ctx context.Context, req domain.Request, reason error) {
	if p.notifier == nil {
		return
	}
	if err := p.notifier.NotifySkipped(ctx, req, reason); err != nil {
		slog.Error("Slackへのスキップ通知の実行中にエラーが発生しましたが、処理を続行します。", "error", err, "cause", reason)
	}
}
