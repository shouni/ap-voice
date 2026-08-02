package adapters

import (
	"context"

	"github.com/shouni/ap-voice/internal/domain"
)

// NoopNotifier は通知を破棄する Notifier 実装です。
type NoopNotifier struct{}

// Notify は何も送信せず、常に成功を返します。
func (n *NoopNotifier) Notify(_ context.Context, _ domain.Request, _ string) error {
	return nil
}

// NotifyFailure は何も送信せず、常に成功を返します。
func (n *NoopNotifier) NotifyFailure(_ context.Context, _ domain.Request, _ error) error {
	return nil
}

// NotifySkipped は何も送信せず、常に成功を返します。
func (n *NoopNotifier) NotifySkipped(_ context.Context, _ domain.Request, _ error) error {
	return nil
}
