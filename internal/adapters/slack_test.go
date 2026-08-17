package adapters

import (
	"context"
	"errors"
	"fmt"
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
	// 並びは「どのジョブか → 何ができたか → どう作ったか」です。
	want := "**詳細:** [job-1](https://ap-voice.example.run.app/history/job-1)\n" +
		"**処理:** `generate`\n" +
		"**ジョブID:** `job-1`\n" +
		"**音声:** [gs://out/voice.wav](https://example.com/voice.wav)\n" +
		// gs:// は Slack では文字列なので、表示はそのままリンク先だけコンソールへ向けます。
		"**出力先:** [gs://out/](https://console.cloud.google.com/storage/browser/out/)\n" +
		"**入力URI:** [gs://in/article.txt](https://console.cloud.google.com/storage/browser/_details/in/article.txt)\n" +
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
	if !strings.Contains(msg.Body, "**入力URI:** [gs://in/article.txt](") {
		t.Errorf("Body = %q, want common metadata", msg.Body)
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

// gs:// URI → Cloud Console URL の変換規則（末尾スラッシュでバケットブラウザと
// 詳細ページを使い分ける等）は notify.Body.URIField に移管し、go-notify 側の
// テストが検証します。ここでは通知本文レベルの配線だけを確認します。

// TestNotifyKeepsNonGCSInputAsPlainValue は、コンソールへ飛ばせない入力ソースが
// リンクではなく素の値として残ることを検証します。
func TestNotifyKeepsNonGCSInputAsPlainValue(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	req := testRequest()
	req.InputURI = "https://example.com/tech-news"

	if err := adapter.Notify(context.Background(), req, ""); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	if body := rec.last(t).Body; !strings.Contains(body, "**入力URI:** https://example.com/tech-news\n") {
		t.Errorf("Body = %q, want plain 入力URI", body)
	}
}

// TestNotifyFailureDistinguishesTimeout は、打ち切りが専用の見出しで届くことを検証します。
//
// **時間切れと本当の失敗は対処が違います。** 前者は流し直せば通ることがあり、
// 後者は入力か設定を直す必要があります。同じ ❌ で並ぶと、一覧では区別できません。
func TestNotifyFailureDistinguishesTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cause     error
		wantTitle string
		wantGuide bool
	}{
		{
			name: "打ち切り",
			// go-voicevox は原因を包んで返すため、素の DeadlineExceeded では届きません。
			cause:     fmt.Errorf("音声合成に失敗しました: %w", context.DeadlineExceeded),
			wantTitle: timeoutTitles.Failure,
			wantGuide: true,
		},
		{
			name:      "Cloud Run の停止",
			cause:     fmt.Errorf("中断されました: %w", context.Canceled),
			wantTitle: timeoutTitles.Failure,
			wantGuide: true,
		},
		{
			name:      "本当の失敗",
			cause:     errors.New("VOICEVOX に接続できません"),
			wantTitle: slackTitles.Failure,
			wantGuide: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			adapter, rec := newTestAdapter()
			if err := adapter.NotifyFailure(context.Background(), testRequest(), tt.cause); err != nil {
				t.Fatalf("NotifyFailure failed: %v", err)
			}

			msg := rec.last(t)
			if msg.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", msg.Title, tt.wantTitle)
			}
			if got := strings.Contains(msg.Body, timeoutGuidance); got != tt.wantGuide {
				t.Errorf("案内の有無 = %v, want %v", got, tt.wantGuide)
			}
			// 原因はどちらの場合も本文に残ります。
			if !strings.Contains(msg.Body, "**エラー内容:**") {
				t.Errorf("エラー内容が欠けています: %q", msg.Body)
			}
			// 見出しが変わっても種別は失敗のままです（Slack の色に効きます）。
			if msg.Level != notify.LevelFailure {
				t.Errorf("Level = %v, want %v", msg.Level, notify.LevelFailure)
			}
		})
	}
}

// TestNotifyGroupsFieldsByPurpose は、項目が「どのジョブか → 何ができたか →
// どう作ったか」の順に並ぶことを検証します。
//
// **synthesize では最後の組がまるごと消えます。** 入力URI・モード・モデルを
// 持たないためで、識別と成果物だけが残ります。生成条件が途中に挟まっていると、
// 項目が欠けているのか、そもそも持たないのかを読み分けられません。
func TestNotifyGroupsFieldsByPurpose(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	req := testRequest()
	// synthesize は保存済みの台本から作るため、生成条件を持ちません。
	req.Command = domain.CommandSynthesize
	req.InputURI = ""
	req.Mode = ""
	req.AIModel = ""

	if err := adapter.Notify(context.Background(), req, "https://example.com/voice.wav"); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	want := "**詳細:** [job-1](https://ap-voice.example.run.app/history/job-1)\n" +
		"**処理:** `synthesize`\n" +
		"**ジョブID:** `job-1`\n" +
		"**音声:** [gs://out/voice.wav](https://example.com/voice.wav)\n" +
		"**出力先:** [gs://out/](https://console.cloud.google.com/storage/browser/out/)"
	if got := rec.last(t).Body; got != want {
		t.Errorf("Body =\n%q\nwant\n%q", got, want)
	}
}
