package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-job-firestore/jobfirestore"

	"github.com/shouni/ap-voice/internal/domain"
)

// getJobStatus は GET /history/{jobID}/status を呼びます。
func getJobStatus(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("GET", "/history/"+jobID+"/status", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.JobStatus(rec, req)
	return rec
}

// 未記録（ErrNotFound）は 404。MCP サーバーが unknown として扱う正常系。
func TestJobStatusNotRecordedIs404(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{err: fmt.Errorf("%w: 未記録", jobfirestore.ErrNotFound)})
	if rec := getJobStatus(t, h); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// 読めなかっただけ（ErrUnavailable）のときに 404 と答えないこと。
// 混ぜると、ストレージ障害の間すべてのジョブが「記録が無い」ように見え、
// ポーリング側が unknown として静かに受け入れてしまう。
func TestJobStatusUnreadableIsNot404(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{err: fmt.Errorf("%w: storage down", jobfirestore.ErrUnavailable)})
	rec := getJobStatus(t, h)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("status = 404: 読めないだけのジョブを未記録と答えている: %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

// 読めたら 200 で jobfirestore.Status のフラットな形のまま返すこと。
func TestJobStatusReturnsRecord(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{status: domain.JobStatus{
		State:    jobfirestore.StateRunning,
		AudioURI: "gs://test/voice/x/audio.wav",
	}})
	rec := getJobStatus(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答が JSON ではない: %v", err)
	}
	if got["state"] != string(jobfirestore.StateRunning) {
		t.Errorf("state = %v, want running", got["state"])
	}
	if got["audio_uri"] != "gs://test/voice/x/audio.wav" {
		t.Errorf("audio_uri = %v（埋め込みのフラット展開が崩れている）", got["audio_uri"])
	}
}
