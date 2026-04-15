package adapters

import (
	"context"

	"ap-voice/internal/domain"
)

// NoopNotifier は通知を破棄する Notifier 実装です。
type NoopNotifier struct{}

func (n *NoopNotifier) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	return nil
}

func (n *NoopNotifier) NotifyFailure(ctx context.Context, req domain.Request, err error) error {
	return nil
}

func (n *NoopNotifier) NotifySkipped(ctx context.Context, req domain.Request, reason error) error {
	return nil
}
