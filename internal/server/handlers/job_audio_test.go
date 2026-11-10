package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestAPIAudioRefusesWhenThereIsNoAudio verifies that a job without audio is given no link.
//
// Signing does not check that the object exists. A URL can be signed even for audio that was never
// made, so without checking first we would hand out "a link that 404s when opened".
// The Slack notification did exactly that once.
func TestAudioRefusesWhenThereIsNoAudio(t *testing.T) {
	t.Parallel()

	signer := &stubSigner{}
	h := apiHandler(t, audioRepo{hasAudio: false})
	h.signer = signer

	rec := callAudio(t, h)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if signer.calls != 0 {
		t.Error("signed although there is no audio")
	}
}

// TestAPIAudioReturnsBothLocations verifies that both the non-expiring storage location and a
// playable link are returned. They serve different purposes, so one is not enough.
func TestAudioReturnsBothLocations(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, audioRepo{hasAudio: true})
	h.signer = &stubSigner{}

	rec := callAudio(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got apiAudio
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}
	if !strings.HasPrefix(got.AudioURI, "gs://") {
		t.Errorf("audio_uri = %q, want it to start with gs://", got.AudioURI)
	}
	if !strings.Contains(got.SignedURL, "X-Goog-Signature") {
		t.Errorf("signed_url carries no signature: %q", got.SignedURL)
	}
	// It states the expiry. Without knowing how long the link lasts, the caller cannot
	// notice that it handed out an expired one.
	if got.ExpiresInSeconds <= 0 {
		t.Errorf("expires_in_seconds = %d", got.ExpiresInSeconds)
	}
}

// callAudio calls GET /api/jobs/{jobID}/audio.
func callAudio(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("GET", "/jobs/"+jobID+"/audio", nil)
	// Since the merge, Accept decides the representation. The MCP server's baseClient always sets it.
	req.Header.Set("Accept", "application/json")
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Audio(rec, req)
	return rec
}
