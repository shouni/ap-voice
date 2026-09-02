package adapters

import (
	"context"
	"errors"
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
}

// timeoutTitles は、PIPELINE_TIMEOUT で打ち切ったときの見出しです。
//
// 時間切れは「失敗」と対処が違います。設定や入力が壊れているわけではなく、
// もう一度流せば通ることも、台本を分ければ通ることもあります。同じ ❌ で並ぶと、
// 一覧を見たときにどちらなのか開くまで分かりません。
var timeoutTitles = notify.Titles{
	Success: slackTitles.Success,
	Failure: "⏳ 時間切れで打ち切りました。",
}

// timeoutGuidance は、打ち切り時に本文へ足す案内です。
const timeoutGuidance = "上限に達したため、アプリ側から打ち切りました。" +
	"**音声は保存されていません。** 台本は残っているので、詳細画面から音声の作成をやり直せます。" +
	"何度も起きる場合は、台本の行数を減らすか PIPELINE_TIMEOUT を延ばしてください。"

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
// serviceURL には公開側（Web 面）の URLを渡します。通知から辿る詳細画面は
// Worker 面ではなく Web 面にあり、Worker の URL を入れるとリンクが非公開サービスを
// 指して 403 になります。
func NewSlackAdapter(httpClient httpkit.Poster, webhookURL, serviceURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("slackクライアントの初期化に失敗しました: %w", err)
	}

	return &SlackAdapter{
		pipeline:   notify.NewPipeline(notifier, slackTitles),
		serviceURL: serviceURL,
	}, nil
}

// detailURL は、ジョブの詳細画面の URL を返します。
// ジョブ ID が無い場合は空を返し、通知側が行ごと省きます。
func (s *SlackAdapter) detailURL(jobID string) string {
	return notify.JoinURL(s.serviceURL, "/history", jobID)
}

// Notify は Slack への完了通知を実行します。
func (s *SlackAdapter) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	body := s.metadata(req, publicURL)

	if err := s.pipeline.Success(ctx, body); err != nil {
		return fmt.Errorf("Slackへの結果URL投稿に失敗しました: %w", err)
	}

	slog.Info("パイプライン完了通知を Slack に投稿しました。", "public_url", publicURL, "output_uri", req.OutputURI)
	return nil
}

// NotifyFailure は Slack へ失敗通知を送信します。
//
// 時間切れだけは見出しを分けます。go-voicevox がバッチのエラーを
// Unwrap() []error で返すようになったため、打ち切りかどうかが型で分かります。
func (s *SlackAdapter) NotifyFailure(ctx context.Context, req domain.Request, err error) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	pipeline, body := s.pipeline, s.metadata(req, "")
	if isTimeout(err) {
		pipeline = pipeline.WithTitles(timeoutTitles)
		body = body.Text(timeoutGuidance)
	}

	if sendErr := pipeline.Failure(ctx, body, err); sendErr != nil {
		return fmt.Errorf("slackへの失敗通知投稿に失敗しました: %w", sendErr)
	}

	slog.Info("パイプライン失敗通知を Slack に投稿しました。", "output_uri", req.OutputURI, "timeout", isTimeout(err))
	return nil
}

// isTimeout は、打ち切りが原因の失敗かどうかを返します。
//
// context.Canceled も見るのは、Cloud Run が SIGTERM でインスタンスを畳むときに
// そちらで抜けるためです。利用者から見ればどちらも「途中で止まった」です。
func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// metadata は、通知本文を組み立てます。publicURL が空なら音声の行は出ません。
//
// 並びは「何のジョブか → 何ができたか → どう作ったか」です。値が空の項目は
// notify.Body が行ごと省くため、synthesize では最後の組がまるごと消え、
// 識別と成果物だけが残ります。項目が混ざっていると、欠けているのか
// そもそも持たないのかを読み分けられません。
func (s *SlackAdapter) metadata(req domain.Request, publicURL string) *notify.Body {
	body := notify.NewBody()

	// ── どのジョブか ──
	// 詳細画面は台本の確認と再合成の入口です。通知から1手で辿れないと、
	// ジョブ ID を控えて自分で URL を組み立てることになります。
	if url := s.detailURL(req.JobID); url != "" {
		body = body.Link("詳細", url, req.JobID)
	}
	body = body.
		Code("処理", string(req.Command)).
		Code("ジョブID", req.JobID)

	// ── 何ができたか ──
	// リンクの表示名には署名付き URL をそのまま使いません。クエリだけで 1000 文字を
	// 超えるため、本文が URL で埋まって他の項目が読めなくなります。
	body = body.Link("音声", publicURL, req.OutputURI)
	// ファイル名まで出しません。generate の時点では音声がまだ無く、
	// audio.wav を出力先として示すと存在しないものを案内することになります。
	// 何が置かれたかは詳細画面が示します。
	// gs:// は表示のまま Cloud Console へリンクされます（notify.Body.URIField）。
	body = body.URIField("出力先", outputPrefix(req.OutputURI))

	// ── どう作ったか ──
	// synthesize は保存済みの台本から作るため、この 3 つを持ちません。
	body = body.URIField("入力URI", req.InputURI)
	return body.
		Code("モード", req.Mode).
		Code("モデル", req.AIModel)
}

// outputPrefix は出力先のジョブ単位のプレフィックスを返します。
// 成果物はここにまとまるため、どの段階でも案内として正しくなります。
func outputPrefix(outputURI string) string {
	if before, _, ok := strings.CutLast(outputURI, "/"); ok {
		return before + "/"
	}
	return outputURI
}
