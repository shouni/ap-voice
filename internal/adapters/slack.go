package adapters

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-notify/slack"

	"github.com/shouni/ap-voice/internal/domain"
)

// slackTitles はパイプラインの結果ごとの見出しです。
var slackTitles = notify.Titles{
	Success: "✅ 音声生成パイプラインが完了しました。",
	Failure: "❌ 音声生成パイプラインに失敗しました。",
	Skipped: "ℹ️ 音声生成パイプラインをスキップしました。",
}

// SlackAdapter は、Slack Webhook を介してパイプラインの結果を通知するアダプタです。
// domain.Notifier を実装します。
type SlackAdapter struct {
	pipeline *notify.Pipeline
}

var _ domain.Notifier = (*SlackAdapter)(nil)

// NewSlackAdapter は新しいアダプターインスタンスを作成します。
// webhookURL が空の場合は通知を行わないアダプターを返すため、
// 呼び出し側で未設定時の分岐を持つ必要はありません。
func NewSlackAdapter(httpClient httpkit.Requester, webhookURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("slackクライアントの初期化に失敗しました: %w", err)
	}

	return &SlackAdapter{
		pipeline: notify.NewPipeline(notifier, slackTitles),
	}, nil
}

// Notify は Slack への完了通知を実行します。
func (s *SlackAdapter) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	body := notify.NewBody().Link("公開URL", publicURL, publicURL)
	writeCommonMetadata(body, req)

	if err := s.pipeline.Success(ctx, body); err != nil {
		return fmt.Errorf("Slackへの結果URL投稿に失敗しました: %w", err)
	}

	slog.Info("パイプライン完了通知を Slack に投稿しました。", "public_url", publicURL, "output_uri", req.OutputURI)
	return nil
}

// NotifyFailure は Slack へ失敗通知を送信します。
func (s *SlackAdapter) NotifyFailure(ctx context.Context, req domain.Request, err error) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	if sendErr := s.pipeline.Failure(ctx, commonMetadata(req), err); sendErr != nil {
		return fmt.Errorf("slackへの失敗通知投稿に失敗しました: %w", sendErr)
	}

	slog.Info("パイプライン失敗通知を Slack に投稿しました。", "output_uri", req.OutputURI)
	return nil
}

// NotifySkipped は Slack へスキップ通知を送信します。
func (s *SlackAdapter) NotifySkipped(ctx context.Context, req domain.Request, reason error) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	if sendErr := s.pipeline.Skipped(ctx, commonMetadata(req), reason); sendErr != nil {
		return fmt.Errorf("slackへのスキップ通知投稿に失敗しました: %w", sendErr)
	}

	slog.Info("パイプラインスキップ通知を Slack に投稿しました。", "output_uri", req.OutputURI)
	return nil
}

// commonMetadata は、各通知で共通して表示するメタデータだけを持つ本文を返します。
func commonMetadata(req domain.Request) *notify.Body {
	return writeCommonMetadata(notify.NewBody(), req)
}

// writeCommonMetadata は、各通知で共通して表示するメタデータを本文へ追記します。
// 値が空の項目は notify.Body が行ごと省きます。
func writeCommonMetadata(body *notify.Body, req domain.Request) *notify.Body {
	// synthesize は入力URI・モード・モデルを持たないため、通知に処理名が無いと
	// 「項目が欠けている」のか「台本から合成しただけ」なのか読み分けられません。
	return body.
		Code("処理", string(req.Command)).
		Code("入力URI", req.InputURI).
		Code("出力URI", req.OutputURI).
		Code("モード", req.Mode).
		Code("モデル", req.AIModel)
}
