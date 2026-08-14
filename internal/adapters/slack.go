package adapters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-notify/slack"

	"github.com/shouni/ap-voice/internal/domain"
)

// slackTitles はパイプラインの結果ごとの見出しです。
//
// 「音声生成」と書かないのは、generate が台本までで終わるためです。何をしたかは
// 本文の「処理」で分かります。
var slackTitles = notify.Titles{
	Success: "✅ 処理が完了しました。",
	Failure: "❌ 処理に失敗しました。",
	Skipped: "ℹ️ 処理をスキップしました。",
}

// SlackAdapter は、Slack Webhook を介してパイプラインの結果を通知するアダプタです。
// domain.Notifier を実装します。
type SlackAdapter struct {
	pipeline *notify.Pipeline
	// serviceURL は Web 面の公開 URL です。通知に詳細画面へのリンクを載せるために持ちます。
	serviceURL string
}

var _ domain.Notifier = (*SlackAdapter)(nil)

// NewSlackAdapter は新しいアダプターインスタンスを作成します。
// webhookURL が空の場合は通知を行わないアダプターを返すため、
// 呼び出し側で未設定時の分岐を持つ必要はありません。
// serviceURL には**公開側（Web 面）の URL**を渡します。通知から辿る詳細画面は
// Worker 面ではなく Web 面にあり、Worker の URL を入れるとリンクが非公開サービスを
// 指して 403 になります。
func NewSlackAdapter(httpClient httpkit.Requester, webhookURL, serviceURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("slackクライアントの初期化に失敗しました: %w", err)
	}

	return &SlackAdapter{
		pipeline:   notify.NewPipeline(notifier, slackTitles),
		serviceURL: strings.TrimSuffix(serviceURL, "/"),
	}, nil
}

// detailURL は、ジョブの詳細画面の URL を返します。
// ジョブ ID が無い場合は空を返し、通知側が行ごと省きます。
func (s *SlackAdapter) detailURL(jobID string) string {
	if s.serviceURL == "" || jobID == "" {
		return ""
	}
	return s.serviceURL + "/history/" + jobID
}

// Notify は Slack への完了通知を実行します。
func (s *SlackAdapter) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	// リンクの表示名には署名付き URL をそのまま使いません。クエリだけで 1000 文字を
	// 超えるため、本文が URL で埋まって他の項目が読めなくなります。
	body := notify.NewBody().Link("音声", publicURL, req.OutputURI)
	s.writeCommonMetadata(body, req)

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

	if sendErr := s.pipeline.Failure(ctx, s.commonMetadata(req), err); sendErr != nil {
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

	if sendErr := s.pipeline.Skipped(ctx, s.commonMetadata(req), reason); sendErr != nil {
		return fmt.Errorf("slackへのスキップ通知投稿に失敗しました: %w", sendErr)
	}

	slog.Info("パイプラインスキップ通知を Slack に投稿しました。", "output_uri", req.OutputURI)
	return nil
}

// commonMetadata は、各通知で共通して表示するメタデータだけを持つ本文を返します。
func (s *SlackAdapter) commonMetadata(req domain.Request) *notify.Body {
	return s.writeCommonMetadata(notify.NewBody(), req)
}

// writeCommonMetadata は、各通知で共通して表示するメタデータを本文へ追記します。
// 値が空の項目は notify.Body が行ごと省きます。
func (s *SlackAdapter) writeCommonMetadata(body *notify.Body, req domain.Request) *notify.Body {
	// 詳細画面は台本の確認と再合成の入口です。通知から1手で辿れないと、
	// ジョブ ID を控えて自分で URL を組み立てることになります。
	if url := s.detailURL(req.JobID); url != "" {
		body = body.Link("詳細", url, req.JobID)
	}

	// synthesize は入力URI・モード・モデルを持たないため、通知に処理名が無いと
	// 「項目が欠けている」のか「台本から合成しただけ」なのか読み分けられません。
	return body.
		Code("処理", string(req.Command)).
		Code("ジョブID", req.JobID).
		Code("入力URI", req.InputURI).
		// **ファイル名まで出しません。** generate の時点では音声がまだ無く、
		// audio.wav を出力先として示すと存在しないものを案内することになります。
		// 何が置かれたかは詳細画面が示します。
		Code("出力先", outputPrefix(req.OutputURI)).
		Code("モード", req.Mode).
		Code("モデル", req.AIModel)
}

// outputPrefix は出力先のジョブ単位のプレフィックスを返します。
// 成果物はここにまとまるため、どの段階でも案内として正しくなります。
func outputPrefix(outputURI string) string {
	if idx := strings.LastIndex(outputURI, "/"); idx >= 0 {
		return outputURI[:idx+1]
	}
	return outputURI
}
