package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/worker"
	"github.com/shouni/go-notify/notify"

	"github.com/shouni/ap-voice/internal/domain"
)

// recordingNotifier is a fake that records the notify.Message sent.
// Converting to Slack markup is go-notify's job, so this only verifies the title and body
// ap-voice assembled.
type recordingNotifier struct {
	got []notify.Message
}

// Notify implements notify.Notifier and records the Message it receives.
func (r *recordingNotifier) Notify(_ context.Context, msg notify.Message) error {
	r.got = append(r.got, msg)
	return nil
}

// last returns the last Message sent.
func (r *recordingNotifier) last(t *testing.T) notify.Message {
	t.Helper()
	if len(r.got) == 0 {
		t.Fatal("no notification was sent")
	}
	return r.got[len(r.got)-1]
}

// newTestAdapter returns an adapter with a recording Notifier plugged in.
func newTestAdapter() (*SlackAdapter, *recordingNotifier) {
	rec := &recordingNotifier{}
	return &SlackAdapter{
		pipeline:   notify.NewPipeline(rec, slackTitles),
		serviceURL: "https://ap-voice.example.run.app",
	}, rec
}

// testRequest is the request shared by the tests.
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

// TestNewSlackAdapterDisabledWhenWebhookURLEmpty verifies that an unset webhook URL disables
// notifications without an error.
// The builder used to have its own Noop implementation and branch, but this behaviour has
// moved into the adapter.
func TestNewSlackAdapterDisabledWhenWebhookURLEmpty(t *testing.T) {
	t.Parallel()

	adapter, err := NewSlackAdapter(nil, "", "https://ap-voice.example.run.app")
	if err != nil {
		t.Fatalf("NewSlackAdapter failed: %v", err)
	}
	if adapter.pipeline.Enabled() {
		t.Fatal("notifications are enabled although the webhook URL is unset")
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

// TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet verifies that a nil HTTP client is an
// error when a webhook URL is set.
func TestNewSlackAdapterRequiresHTTPClientWhenWebhookSet(t *testing.T) {
	t.Parallel()

	if _, err := NewSlackAdapter(nil, "https://hooks.slack.example/webhook", "https://ap-voice.example.run.app"); err == nil {
		t.Fatal("no error although the HTTP client is nil")
	}
}

// TestNotifySendsPublicURLAndMetadata verifies that the completion notification is sent with the
// public URL and the shared metadata.
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

	// The link text is the gs:// path, not the signed URL. The signed URL's
	// query alone exceeds 1000 characters and would fill the body.
	// The output does not go down to the file name. At generate time there is no audio yet, and
	// pointing at audio.wav would direct people to something that does not exist.
	// The order is "which job → what was made → how it was made".
	want := "**詳細:** [job-1](https://ap-voice.example.run.app/jobs/job-1)\n" +
		"**処理:** `generate`\n" +
		"**ジョブID:** `job-1`\n" +
		"**音声:** [gs://out/voice.wav](https://example.com/voice.wav)\n" +
		// gs:// is plain text in Slack, so the display stays as is and only the link target goes to the console.
		"**出力先:** [gs://out/](https://console.cloud.google.com/storage/browser/out/)\n" +
		"**入力URI:** [gs://in/article.txt](https://console.cloud.google.com/storage/browser/_details/in/article.txt)\n" +
		"**モード:** `dialogue`\n" +
		"**モデル:** `gemini-3-pro`"
	if msg.Body != want {
		t.Errorf("Body =\n%q\nwant\n%q", msg.Body, want)
	}
}

// TestNotifyOmitsPublicURLWhenEmpty verifies that the line is omitted when there is no audio.
// generate takes this path. It makes only the script, so it returns no signed URL and
// does not hand out a link to a non-existent audio.wav.
func TestNotifyOmitsPublicURLWhenEmpty(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	if err := adapter.Notify(context.Background(), testRequest(), ""); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	body := rec.last(t).Body
	if strings.Contains(body, "**音声:**") {
		t.Errorf("Body = %q, the audio line remains", body)
	}
	// generate ends at the script, so this path is the default. The detail page is the entry point.
	if !strings.HasPrefix(body, "**詳細:**") {
		t.Errorf("Body = %q, want it to start with 詳細", body)
	}
}

// TestNotifyFailureAppendsCause verifies that the failure notification appends the error.
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

// TestNotifyFailureWithNilCause verifies that a nil cause does not break the notification.
// The old implementation sent <nil>, the result of fmt.Sprintf("%v", nil), as is,
// a string Slack mistook for link syntax.
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

// TestNotifySetsLevel verifies that each outcome is sent with its level.
// Slack turns it into the attachment colour, so it is needed separately from the title's emoji.
func TestNotifySetsLevel(t *testing.T) {
	tests := []struct {
		name string
		call func(a *SlackAdapter) error
		want notify.Level
	}{
		{
			name: "success",
			call: func(a *SlackAdapter) error {
				return a.Notify(context.Background(), testRequest(), "https://example.com/voice.wav")
			},
			want: notify.LevelSuccess,
		},
		{
			name: "failure",
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
				t.Fatalf("notification failed: %v", err)
			}
			if got := rec.last(t).Level; got != tt.want {
				t.Errorf("Level = %v, want %v", got, tt.want)
			}
		})
	}
}

// The gs:// URI → Cloud Console URL rules (choosing the bucket browser or the details page by a
// trailing slash and so on) moved to notify.Body.URIField and are verified by go-notify's
// tests. This only checks the wiring at the notification body level.

// TestNotifyKeepsNonGCSInputAsPlainValue verifies that an input source that cannot be linked to
// the console stays a plain value rather than a link.
func TestNotifyKeepsNonGCSInputAsPlainValue(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	req := testRequest()
	req.InputURI = "https://example.com/tech-news"

	if err := adapter.Notify(context.Background(), req, ""); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	if body := rec.last(t).Body; !strings.Contains(body, "**入力URI:** https://example.com/tech-news\n") {
		t.Errorf("Body = %q, want a plain 入力URI", body)
	}
}

// TestNotifyFailureDistinguishesTimeout verifies that a cut-off run arrives with its own title.
//
// A timeout and a real failure call for different responses. The former may pass when run again,
// the latter needs the input or settings fixed. With the same ❌ for both, a list cannot tell them apart.
func TestNotifyFailureDistinguishesTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cause     error
		wantTitle string
		wantGuide bool
	}{
		{
			name: "cut off",
			// When Lifecycle cuts the run at PIPELINE_TIMEOUT, the original error reaches Finish
			// marked with worker.ErrTimedOut (the text is unchanged).
			cause:     timedOut{fmt.Errorf("speech synthesis failed: %w", context.DeadlineExceeded)},
			wantTitle: timeoutTitles.Failure,
			wantGuide: true,
		},
		{
			// A single downstream deadline (VOICEVOX's 60 seconds and so on) is the same DeadlineExceeded
			// but not a pipeline cutoff. It used to be reported as a "timeout" too.
			name:      "a downstream deadline is a real failure",
			cause:     fmt.Errorf("speech synthesis failed: %w", context.DeadlineExceeded),
			wantTitle: slackTitles.Failure,
			wantGuide: false,
		},
		{
			name:      "Cloud Run shutdown",
			cause:     fmt.Errorf("interrupted: %w", context.Canceled),
			wantTitle: timeoutTitles.Failure,
			wantGuide: true,
		},
		{
			name:      "real failure",
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
				t.Errorf("guidance present = %v, want %v", got, tt.wantGuide)
			}
			// The cause stays in the body either way.
			if !strings.Contains(msg.Body, "**エラー内容:**") {
				t.Errorf("the error details are missing: %q", msg.Body)
			}
			// The level stays failure even when the title changes (it drives Slack's colour).
			if msg.Level != notify.LevelFailure {
				t.Errorf("Level = %v, want %v", msg.Level, notify.LevelFailure)
			}
		})
	}
}

// TestNotifyGroupsFieldsByPurpose verifies that the fields are ordered "which job → what was made →
// how it was made".
//
// For synthesize the last group vanishes entirely, since it has no input URI, mode or model,
// leaving the identification and the artifacts. With generation settings in the middle,
// you could not tell whether a field is missing or simply does not apply.
func TestNotifyGroupsFieldsByPurpose(t *testing.T) {
	t.Parallel()

	adapter, rec := newTestAdapter()
	req := testRequest()
	// synthesize builds from a stored script, so it has no generation settings.
	req.Command = domain.CommandSynthesize
	req.InputURI = ""
	req.Mode = ""
	req.AIModel = ""

	if err := adapter.Notify(context.Background(), req, "https://example.com/voice.wav"); err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	want := "**詳細:** [job-1](https://ap-voice.example.run.app/jobs/job-1)\n" +
		"**処理:** `synthesize`\n" +
		"**ジョブID:** `job-1`\n" +
		"**音声:** [gs://out/voice.wav](https://example.com/voice.wav)\n" +
		"**出力先:** [gs://out/](https://console.cloud.google.com/storage/browser/out/)"
	if got := rec.last(t).Body; got != want {
		t.Errorf("Body =\n%q\nwant\n%q", got, want)
	}
}

// timedOut is a test error shaped like the mark worker.Lifecycle attaches when Timeout fires.
type timedOut struct{ cause error }

func (e timedOut) Error() string        { return e.cause.Error() }
func (e timedOut) Unwrap() error        { return e.cause }
func (e timedOut) Is(target error) bool { return target == worker.ErrTimedOut }
