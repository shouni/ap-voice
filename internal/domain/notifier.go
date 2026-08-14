// Package domain は、ap-voice のフレームワーク非依存なドメイン型とインターフェースを定義します。
package domain

import "context"

// Notifier は、生成されたコンテンツまたはエラーに関する通知を指定されたターゲットまたはチャネルに送信するためのインターフェイスです。
//
// **成功と失敗の 2 つだけです。** 兄弟アプリには「差分が無いのでスキップ」という
// 3 つ目がありますが、ap-voice のジョブは台本か音声を作るか失敗するかのどちらかで、
// スキップに当たる分岐がありません。
type Notifier interface {
	// Notify は、処理成功時のメタデータをターゲットに送信します。
	Notify(ctx context.Context, req Request, publicURL string) error
	// NotifyFailure は、処理失敗時のエラー内容をターゲットに通知します。
	NotifyFailure(ctx context.Context, req Request, err error) error
}
