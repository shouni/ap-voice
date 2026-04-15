package domain

import "context"

// Notifier は、生成されたコンテンツまたはエラーに関する通知を指定されたターゲットまたはチャネルに送信するためのインターフェイスです。
type Notifier interface {
	// Notify は、処理成功時のメタデータをターゲットに送信します。
	Notify(ctx context.Context, req Request, publicURL string) error
	// NotifyFailure は、処理失敗時のエラー内容をターゲットに通知します。
	NotifyFailure(ctx context.Context, req Request, err error) error
	// NotifySkipped は、処理をスキップしたことをターゲットに通知します。
	NotifySkipped(ctx context.Context, req Request, reason error) error
}
