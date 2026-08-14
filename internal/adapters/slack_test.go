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
	return &SlackAdapter{
		pipeline:   notify.NewPipeline(rec, slackTitles),
		serviceURL: "https://ap-voice.example.run.app",
	}, rec
}

// testRequest はテストで使う共通のリクエストです。
func testRequest() domain.Request {
	return domain.Request{
		Command:   domain.CommandGenerate,
		JobID:     "job-1",
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

	adapter, err := NewSlackAdapter(nil, "", "https://ap-voice.example.run.app")
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

	if _, err := NewSlackAdapter(nil, "https://hooks.slack.example/webhook", "https://ap-voice.example.run.app"); err == nil {
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

	// リンクの表示名は署名付き URL ではなく gs:// のパスです。署名付き URL は
	// クエリだけで 1000 文字を超え、本文が URL で埋まるためです。
	// 出力は**ファイル名まで出しません**。generate の時点では音声がまだ無く、
	// audio.wav を示すと存在しないものを案内することになります。
	want := "**音声:** [gs://out/voice.wav](https://example.com/voice.wav)\n" +
		"**詳細:** [job-1](https://ap-voice.example.run.app/history/job-1)\n" +
		"**処理:** `generate`\n" +
		"**ジョブID:** `job-1`\n" +
		"**入力URI:** `gs://in/article.txt`\n" +
		"**出力先:** `gs://out/`\n" +
		"**モード:** `dialogue`\n" +
		"**モデル:** `gemini-3-pro`"
	if msg.Body != want {
		t.Errorf("Body =\n%q\nwant\n%q", msg.Body, want)
	}
}

// TestNotifyOmitsPublicURLWhenEmpty は、音声が無い場合に行ごと省かれることを検証します。
// **generate はここを通ります。** 台本しか作らないため署名付き URL を返さず、
// 存在しない audio.wav のリンクを配らないようにしています。
func TestNotifyOmitsPublicURLWhenEmpty(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.Notify(context.Background(), testRequest(), ""); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	body := rec.last(t).Body
	if strings.Contains(body, "**音声:**") {
		t.Errorf("Body = %q, 音声の行が残っています", body)
	}
	// generate は台本までなので、この経路が既定です。詳細画面が入口になります。
	if !strings.HasPrefix(body, "**詳細:**") {
		t.Errorf("Body = %q, want 詳細 から始まること", body)
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

// TestNotifySetsLevel は、3 つの結果それぞれが種別を伴って送信されることを検証します。
// Slack 側はこれを attachment の色に落とすため、見出しの絵文字とは別に必要です。
func TestNotifySetsLevel(t *testing.T) {
	tests := []struct {
		name string
		call func(a *SlackAdapter) error
		want notify.Level
	}{
		{
			name: "完了",
			call: func(a *SlackAdapter) error {
				return a.Notify(context.Background(), testRequest(), "https://example.com/voice.wav")
			},
			want: notify.LevelSuccess,
		},
		{
			name: "失敗",
			call: func(a *SlackAdapter) error {
				return a.NotifyFailure(context.Background(), testRequest(), errors.New("boom"))
			},
			want: notify.LevelFailure,
		},
		{
			name: "スキップ",
			call: func(a *SlackAdapter) error {
				return a.NotifySkipped(context.Background(), testRequest(), nil)
			},
			want: notify.LevelSkipped,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter, rec := newTestAdapter()

			if err := tt.call(adapter); err != nil {
				t.Fatalf("通知に失敗しました: %v", err)
			}
			if got := rec.last(t).Level; got != tt.want {
				t.Errorf("Level = %v, want %v", got, tt.want)
			}
		})
	}
}
