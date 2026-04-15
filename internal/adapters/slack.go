package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shouni/code-reviewer/httpkit"
	"github.com/shouni/code-reviewer/slack"

	"ap-voice/internal/domain"
)

// SlackAdapter は、Slack APIと連携し、Webhookを介してメッセージを投稿するためのアダプタを表します。
type SlackAdapter struct {
	slackClient *slack.Client
}

// NewSlackAdapter は新しいアダプターインスタンスを作成します。
func NewSlackAdapter(httpClient httpkit.RequestExecutor, webhookURL string) (*SlackAdapter, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("HTTPクライアントが指定されていません (nil)")
	}

	client, err := slack.NewClient(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("Slackクライアントの初期化に失敗しました: %w", err)
	}

	return &SlackAdapter{
		slackClient: client,
	}, nil
}

// Notify は Slack への通知を実行します。
func (s *SlackAdapter) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	title := "✅ 音声生成パイプラインが完了しました。"
	content := s.buildSlackContent(req, publicURL)

	if err := s.slackClient.SendTextWithHeader(ctx, title, content); err != nil {
		return fmt.Errorf("Slackへの結果URL投稿に失敗しました: %w", err)
	}

	slog.Info("パイプライン完了通知を Slack に投稿しました。", "public_url", publicURL, "output_uri", req.OutputURI)
	return nil
}

// NotifyFailure は Slack へ失敗通知を送信します。
func (s *SlackAdapter) NotifyFailure(ctx context.Context, req domain.Request, err error) error {
	title := "❌ 音声生成パイプラインに失敗しました。"
	content := s.buildSlackFailureContent(req, err)

	if sendErr := s.slackClient.SendTextWithHeader(ctx, title, content); sendErr != nil {
		return fmt.Errorf("Slackへの失敗通知投稿に失敗しました: %w", sendErr)
	}

	slog.Info("パイプライン失敗通知を Slack に投稿しました。", "output_uri", req.OutputURI)
	return nil
}

// NotifySkipped は Slack へスキップ通知を送信します。
func (s *SlackAdapter) NotifySkipped(ctx context.Context, req domain.Request, reason error) error {
	title := "ℹ️ 音声生成パイプラインをスキップしました。"
	content := s.buildSlackSkippedContent(req, reason)

	if sendErr := s.slackClient.SendTextWithHeader(ctx, title, content); sendErr != nil {
		return fmt.Errorf("Slackへのスキップ通知投稿に失敗しました: %w", sendErr)
	}

	slog.Info("パイプラインスキップ通知を Slack に投稿しました。", "output_uri", req.OutputURI)
	return nil
}

// --- Helper Methods ---

// buildCommonMetadata は、各通知で共通して表示するメタデータを組み立てます。
func (s *SlackAdapter) buildCommonMetadata(req domain.Request) string {
	return fmt.Sprintf(
		"*入力URI:* `%s`\n"+
			"*出力URI:* `%s`\n"+
			"*モード:* `%s`\n"+
			"*モデル:* `%s`",
		req.InputURI,
		req.OutputURI,
		req.Mode,
		req.AIModel,
	)
}

// buildSlackContent は成功時の投稿メッセージの本文を組み立てます。
func (s *SlackAdapter) buildSlackContent(req domain.Request, publicURL string) string {
	sections := []string{}
	if publicURL != "" {
		sections = append(sections, fmt.Sprintf("*公開URL:* <%s|%s>", publicURL, publicURL))
	}
	sections = append(sections, s.buildCommonMetadata(req))
	return strings.TrimSpace(strings.Join(sections, "\n"))
}

// buildSlackFailureContent は失敗時の投稿メッセージの本文を組み立てます。
func (s *SlackAdapter) buildSlackFailureContent(req domain.Request, err error) string {
	safeErr := strings.ReplaceAll(fmt.Sprintf("%v", err), "`", "'")
	footer := fmt.Sprintf("\n*エラー:* ```%s```", safeErr)
	return strings.TrimSpace(s.buildCommonMetadata(req) + footer)
}

// buildSlackSkippedContent はスキップ時の投稿メッセージの本文を組み立てます。
func (s *SlackAdapter) buildSlackSkippedContent(req domain.Request, reason error) string {
	safeReason := strings.ReplaceAll(reason.Error(), "`", "'")
	footer := fmt.Sprintf("\n*理由:* `%s`", safeReason)
	return strings.TrimSpace(s.buildCommonMetadata(req) + footer)
}
