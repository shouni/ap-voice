package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/jobstatus"
)

// An invalid job ID error is also returned in the requested representation.
// A machine cannot interpret an HTML error page.
func TestJobIDErrorFollowsTheRequestedFormat(t *testing.T) {
	t.Parallel()

	h := &Handler{repo: &storedScriptRepo{}}

	rec := httptest.NewRecorder()
	h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/bad/script", "application/json", "../etc/passwd"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
}

// getJobStatus calls GET /history/{jobID}/status.
func getJobStatus(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("GET", "/jobs/"+jobID, nil)
	req.Header.Set("Accept", "application/json")
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Job(rec, req)
	return rec
}

// Not recorded (ErrNotFound) is a 404. The MCP server treats it as unknown, a normal case.
func TestJobStatusNotRecordedIs404(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{err: fmt.Errorf("%w: not recorded", jobstatus.ErrNotFound)})
	if rec := getJobStatus(t, h); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// Merely unreadable (ErrUnavailable) must not answer 404.
// Mixing them would make every job look "unrecorded" during a storage outage,
// and the poller would quietly accept that as unknown.
func TestJobStatusUnreadableIsNot404(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{err: fmt.Errorf("%w: storage down", jobstatus.ErrUnavailable)})
	rec := getJobStatus(t, h)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("status = 404: a merely unreadable job is reported as unrecorded: %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

// When readable, return 200 in the flat shape of jobstatus.Status.
func TestJobStatusReturnsRecord(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{status: domain.JobStatus{
		State:    jobstatus.StateRunning,
		AudioURI: "gs://test/voice/x/audio.wav",
	}})
	rec := getJobStatus(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if got["state"] != string(jobstatus.StateRunning) {
		t.Errorf("state = %v, want running", got["state"])
	}
	if got["audio_uri"] != "gs://test/voice/x/audio.wav" {
		t.Errorf("audio_uri = %v (the embedded struct's flattening is broken)", got["audio_uri"])
	}
}

// TestDetailOpensWithoutAScript verifies that the detail page opens even for a job with no script yet.
//
// If it does not open, the job cannot be deleted. Deleting is possible only from this page, and
// jobs that failed before writing a script are exactly the ones to delete. It used to return 502
// as soon as the script could not be read, leaving jobs listed in the history but out of reach.
func TestDetailOpensWithoutAScript(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	h := detailHandler(t, &stateOnlyRepo{status: domain.JobStatus{
		JobID: jobID,
		State: jobstatus.StateFailed,
		Error: "AIモデルが空のスクリプトを返しました",
	}})

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+jobID, nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Job(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	// Apart from the Slack notification, this page is the only place a person sees why a job failed.
	for _, want := range []string{"このジョブは失敗しました", "AIモデルが空のスクリプトを返しました", "このジョブを削除"} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

// TestRegenerateReusesTheRecordedInput verifies that regenerating reuses the input source left
// in the record.
//
// What was read is left in neither the script nor the artifacts, and a failed job has no script at all.
// Unless it can be restored from the record, it depends on whether the user noted the URL.
func TestRegenerateReusesTheRecordedInput(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	repo := &stateOnlyRepo{status: domain.JobStatus{
		JobID:    jobID,
		State:    jobstatus.StateFailed,
		Error:    "AIモデルが空のスクリプトを返しました",
		InputURI: "https://example.com/article",
		Mode:     "tech_solo",
		AIModel:  "gemini-test",
	}}
	queue := &capturingQueue{}
	h := detailHandler(t, repo)
	h.queue = queue

	rec := httptest.NewRecorder()
	h.Regenerate(rec, postWithJobID(jobID))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if queue.calls != 1 {
		t.Fatalf("enqueue count = %d, want 1", queue.calls)
	}
	got := queue.got
	if got.Command != domain.CommandGenerate {
		t.Errorf("Command = %q, want %q", got.Command, domain.CommandGenerate)
	}
	// The job ID does not change. It is a redo, so there is no point listing two entries in the history.
	if got.JobID != jobID {
		t.Errorf("JobID = %q, want %q", got.JobID, jobID)
	}
	if got.InputURI != "https://example.com/article" {
		t.Errorf("InputURI = %q, not restored from the record", got.InputURI)
	}
	if got.Mode != "tech_solo" || got.AIModel != "gemini-test" {
		t.Errorf("mode/model = %q/%q, not restored from the record", got.Mode, got.AIModel)
	}
}

// TestRegenerateRefusesWithoutAnInput verifies that a job with no recorded input source is not
// regenerated.
//
// A supplied script (the script JSON tab, the API's script) is such a job. There is nothing to
// regenerate from, so allowing the button would queue a task that always fails.
func TestRegenerateRefusesWithoutAnInput(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	repo := &stateOnlyRepo{status: domain.JobStatus{JobID: jobID, State: jobstatus.StateSucceeded}}
	queue := &capturingQueue{}
	h := detailHandler(t, repo)
	h.queue = queue

	rec := httptest.NewRecorder()
	h.Regenerate(rec, postWithJobID(jobID))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if queue.calls != 0 {
		t.Errorf("enqueued %d times although there is no input source", queue.calls)
	}
}
