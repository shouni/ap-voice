package adapters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shouni/go-notify/notify"

	"github.com/shouni/ap-voice/internal/domain"
)

// recordingNotifier は送信された notify.Message を記録するフェイクです。
// Slack 記法への変換は go-notify 側の責務なので、ここでは ap-voice が
// 組み立てた見出しと本文だけを検証します。
type recordingNotifier struct {
	got []notify.Message
}

// Notify は notify.Notifier を実装し、受け取った Message を記録します。
func (r *recordingNotifier) Notify(_ context.Context, msg notify.Message) error {
	r.got = append(r.got, msg)
	return nil
}

// last は最後に送信された Message を返します。
func (r *recordingNotifier) last(t *testing.T) notify.Message {
	t.Helper()
	if len(r.got) == 0 {
		t.Fatal("通知が送信されていません")
	}
	return r.got[len(r.got)-1]
}

// newTestAdapter は記録用 Notifier を差し込んだアダプターを返します。
func newTestAdapter() (*SlackAdapter, *recordingNotifier) {
	rec := &recordingNotifier{}
	return &SlackAdapter{pipeline: notify.NewPipeline(rec, slackTitles)}, rec
}

// testRequest はテストで使う共通のリクエストです。
func testRequest() domain.Request {
	return domain.Request{
		InputURI:  "gs://in/article.txt",
		OutputURI: "gs://out/voice.wav",
		Mode:      "dialogue",
		AIModel:   "gemini-3-pro",
	}
}

// TestNewSlackAdapterDisabledWhenWebhookURLEmpty は、Webhook URL が未設定なら
// エラーにならず通知が無効化されることを検証します。
// 以前は builder 側に専用の Noop 実装と分岐がありましたが、この振る舞いは
// アダプターの責務に移っています。
func TestNewSlackAdapterDisabledWhenWebhookURLEmpty(t *testing.T) {
	t.Parallel()

	adapter, err := NewSlackAdapter(nil, "")
	if err != nil {
		t.Fatalf("NewSlackAdapter failed: %v", err)
	}
	if adapter.pipeline.Enabled() {
		t.Fatal("Webhook URL 未設定なのに通知が有効になっています")
	}

	ctx := context.Background()
	req := testRequest()
	if err := adapter.Notify(ctx, req, "https://example.com/voice.wav"); err != nil {
		t.Errorf("Notify = %v, want nil", err)
	}
	if err := adapter.NotifyFailure(ctx, req, errors.New("boom")); err != nil {
		t.Errorf("NotifyFailure = %v, want nil", err)
	}
	if err := adapter.NotifySkipped(ctx, req, errors.New("差分なし")); err != nil {
		t.Errorf("NotifySkipped = %v, want nil", err)
	}
}

// TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet は、Webhook URL があるのに
// HTTP クライアントが nil の場合はエラーになることを検証します。
func TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet(t *testing.T) {
	t.Parallel()

	if _, err := NewSlackAdapter(nil, "https://hooks.slack.example/webhook"); err == nil {
		t.Fatal("HTTPクライアントが nil なのにエラーになりません")
	}
}

// TestNotifySendsPublicURLAndMetadata は、完了通知が公開URLと共通メタデータを
// 含めて送信されることを検証します。
func TestNotifySendsPublicURLAndMetadata(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.Notify(context.Background(), testRequest(), "https://example.com/voice.wav"); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Success {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Success)
	}

	want := "**公開URL:** [https://example.com/voice.wav](https://example.com/voice.wav)\n" +
		"**入力URI:** `gs://in/article.txt`\n" +
		"**出力URI:** `gs://out/voice.wav`\n" +
		"**モード:** `dialogue`\n" +
		"**モデル:** `gemini-3-pro`"
	if msg.Body != want {
		t.Errorf("Body =\n%q\nwant\n%q", msg.Body, want)
	}
}

// TestNotifyOmitsPublicURLWhenEmpty は、公開URLが無い場合に行ごと省かれることを
// 検証します。ローカル出力では署名付きURLが生成されないため通る経路です。
func TestNotifyOmitsPublicURLWhenEmpty(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.Notify(context.Background(), testRequest(), ""); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	body := rec.last(t).Body
	if strings.Contains(body, "公開URL") {
		t.Errorf("Body = %q, 公開URL の行が残っています", body)
	}
	if !strings.HasPrefix(body, "**入力URI:**") {
		t.Errorf("Body = %q, want 入力URI から始まること", body)
	}
}

// TestNotifyFailureAppendsCause は、失敗通知がエラー内容を追記することを検証します。
func TestNotifyFailureAppendsCause(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	err := adapter.NotifyFailure(context.Background(), testRequest(), errors.New("VOICEVOX に接続できません"))
	if err != nil {
		t.Fatalf("NotifyFailure failed: %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Failure {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Failure)
	}
	if !strings.Contains(msg.Body, "**エラー内容:**\nVOICEVOX に接続できません") {
		t.Errorf("Body = %q, want error detail", msg.Body)
	}
	if !strings.Contains(msg.Body, "**入力URI:** `gs://in/article.txt`") {
		t.Errorf("Body = %q, want common metadata", msg.Body)
	}
}

// TestNotifySkippedAppendsReason は、スキップ通知が理由を追記することを検証します。
func TestNotifySkippedAppendsReason(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.NotifySkipped(context.Background(), testRequest(), errors.New("入力に差分がありません")); err != nil {
		t.Fatalf("NotifySkipped failed: %v", err)
	}

	msg := rec.last(t)
	if msg.Title != slackTitles.Skipped {
		t.Errorf("Title = %q, want %q", msg.Title, slackTitles.Skipped)
	}
	if !strings.Contains(msg.Body, "**理由:**\n入力に差分がありません") {
		t.Errorf("Body = %q, want reason", msg.Body)
	}
}

// TestNotifyFailureWithNilCause は、原因が nil でも通知が壊れないことを検証します。
// 旧実装は fmt.Sprintf("%v", nil) の結果である <nil> をそのまま送っており、
// Slack 側でリンク構文と誤認される文字列になっていました。
func TestNotifyFailureWithNilCause(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.NotifyFailure(context.Background(), testRequest(), nil); err != nil {
		t.Fatalf("NotifyFailure failed: %v", err)
	}

	if body := rec.last(t).Body; !strings.Contains(body, "**エラー内容:**\n"+notify.NotAvailable) {
		t.Errorf("Body = %q, want N/A fallback", body)
	}
}

// TestNotifySkippedWithNilReason は、理由が nil でも通知が壊れないことを検証します。
// 旧実装は reason.Error() を直接呼んでいたため、nil でパニックしていました。
//
// 理由が無い場合は「理由」節ごと省かれます。スキップは見出しだけで意味が通り、
// 「理由: N/A」を足しても情報が増えないためです。
func TestNotifySkippedWithNilReason(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.NotifySkipped(context.Background(), testRequest(), nil); err != nil {
		t.Fatalf("NotifySkipped failed: %v", err)
	}

	body := rec.last(t).Body
	if strings.Contains(body, "理由") {
		t.Errorf("Body = %q, 理由が無いのに理由の節が出ています", body)
	}
	if !strings.Contains(body, "**入力URI:** `gs://in/article.txt`") {
		t.Errorf("Body = %q, want common metadata", body)
	}
}
