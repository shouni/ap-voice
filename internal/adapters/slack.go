package adapters

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/shouni/gcp-kit/worker"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-notify/notify"
	"github.com/shouni/go-notify/slack"

	"github.com/shouni/ap-voice/internal/domain"
)

// slackTitles are the headings per pipeline outcome.
//
// They do not say "audio generation" because generate ends at the script. What was done shows
// in the body's 「処理」 field.
var slackTitles = notify.Titles{
	Success: "✅ 処理が完了しました。",
	Failure: "❌ 処理に失敗しました。",
}

// timeoutTitles is the heading used when PIPELINE_TIMEOUT cut the run.
//
// A timeout calls for a different response than a "failure". Neither the configuration nor the input
// is broken; running it again may pass, and so may splitting the script. With the same ❌ for both,
// you cannot tell which it is from the list without opening it.
var timeoutTitles = notify.Titles{
	Success: slackTitles.Success,
	Failure: "⏳ 時間切れで打ち切りました。",
}

// timeoutGuidance is the guidance added to the body on a timeout.
const timeoutGuidance = "上限に達したため、アプリ側から打ち切りました。" +
	"**音声は保存されていません。** 台本は残っているので、詳細画面から音声の作成をやり直せます。" +
	"何度も起きる場合は、台本の行数を減らすか PIPELINE_TIMEOUT を延ばしてください。"

// SlackAdapter notifies pipeline outcomes through a Slack webhook.
// It implements domain.Notifier.
type SlackAdapter struct {
	pipeline *notify.Pipeline
	// serviceURL is the web side's public URL, kept to put a link to the detail page in notifications.
	serviceURL string
}

var _ domain.Notifier = (*SlackAdapter)(nil)

// NewSlackAdapter creates a new adapter instance.
// When webhookURL is empty it returns an adapter that sends nothing, so
// callers need no branch for the unset case.
// serviceURL takes the public (web side) URL. The detail page reached from a notification lives
// on the web side, not the worker side; given the worker's URL, the link would point at the
// private service and 403.
func NewSlackAdapter(httpClient httpkit.Poster, webhookURL, serviceURL string) (*SlackAdapter, error) {
	notifier, err := slack.NewNotifier(httpClient, webhookURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the slack client: %w", err)
	}

	return &SlackAdapter{
		pipeline:   notify.NewPipeline(notifier, slackTitles),
		serviceURL: serviceURL,
	}, nil
}

// detailURL returns the URL of the job's detail page.
// Without a job ID it returns empty, and the notification omits the line.
func (s *SlackAdapter) detailURL(jobID string) string {
	return notify.JoinURL(s.serviceURL, "/jobs", jobID)
}

// Notify sends the completion notification to Slack.
func (s *SlackAdapter) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	if !s.pipeline.Enabled() {
		return nil
	}

	body := s.metadata(req, publicURL)

	if err := s.pipeline.Success(ctx, body); err != nil {
		return fmt.Errorf("failed to post the result URL to Slack: %w", err)
	}

	slog.Info("posted the pipeline completion notification to Slack", "public_url", publicURL, "output_uri", req.OutputURI)
	return nil
}

// NotifyFailure sends the failure notification to Slack.
//
// Only timeouts get a separate heading. go-voicevox now returns batch errors as
// Unwrap() []error, so whether it was cut off can be told by type.
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
		return fmt.Errorf("failed to post the failure notification to Slack: %w", sendErr)
	}

	slog.Info("posted the pipeline failure notification to Slack", "output_uri", req.OutputURI, "timeout", isTimeout(err))
	return nil
}

// isTimeout reports whether the failure was caused by the run being cut off.
//
// worker.ErrTimedOut marks that Lifecycle cut the run at PIPELINE_TIMEOUT. A single downstream
// deadline (VOICEVOX's 60 seconds and so on) also returns DeadlineExceeded, so that alone cannot
// tell them apart. context.Canceled is checked too because Cloud Run exits through it when it
// folds an instance with SIGTERM. To the user both are "it stopped halfway".
func isTimeout(err error) bool {
	return errors.Is(err, worker.ErrTimedOut) || errors.Is(err, context.Canceled)
}

// metadata builds the notification body. When publicURL is empty, the audio line is omitted.
//
// The order is "which job → what was made → how it was made". notify.Body omits the line of any
// empty field, so for synthesize the last group vanishes entirely and only the identification and
// the artifacts remain. If the fields were mixed, you could not tell whether one is missing
// or simply does not apply.
func (s *SlackAdapter) metadata(req domain.Request, publicURL string) *notify.Body {
	body := notify.NewBody()

	// ── Which job ──
	// The detail page is the entry point for checking the script and re-synthesising. Unless it is
	// one step from the notification, you have to note the job ID and build the URL yourself.
	if url := s.detailURL(req.JobID); url != "" {
		body = body.Link("詳細", url, req.JobID)
	}
	body = body.
		Code("処理", string(req.Command)).
		Code("ジョブID", req.JobID)

	// ── What was made ──
	// The link text is not the signed URL itself. Its query alone exceeds 1000 characters,
	// so the body would fill with the URL and the other fields would become unreadable.
	body = body.Link("音声", publicURL, req.OutputURI)
	// No file name is shown. At generate time there is no audio yet, and pointing at
	// audio.wav as the output would direct people to something that does not exist.
	// The detail page shows what was placed.
	// gs:// is linked to the Cloud Console as displayed (notify.Body.URIField).
	body = body.URIField("出力先", outputPrefix(req.OutputURI))

	// ── How it was made ──
	// synthesize builds from a stored script, so it has none of these three.
	body = body.URIField("入力URI", req.InputURI)
	return body.
		Code("モード", req.Mode).
		Code("モデル", req.AIModel)
}

// outputPrefix returns the job-level prefix of the output location.
// The artifacts are gathered there, so it is correct guidance at every stage.
func outputPrefix(outputURI string) string {
	if before, _, ok := strings.CutLast(outputURI, "/"); ok {
		return before + "/"
	}
	return outputURI
}
