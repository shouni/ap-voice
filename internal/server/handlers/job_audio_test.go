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

// TestAPIAudioRefusesWhenThereIsNoAudio は、音声が無いジョブにリンクを出さないことを
// 検証します。
//
// 署名は対象の存在を確かめません。作っていない音声の URL も署名できてしまうため、
// 先に確かめないと「開くと 404 になるリンク」を配ることになります。
// Slack 通知で同じことを一度やっています。
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
		t.Error("音声が無いのに署名しています")
	}
}

// TestAPIAudioReturnsBothLocations は、期限の無い保存先と再生できるリンクの
// 両方を返すことを検証します。用途が違うため、片方では足りません。
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
		t.Fatalf("応答のデコードに失敗しました: %v", err)
	}
	if !strings.HasPrefix(got.AudioURI, "gs://") {
		t.Errorf("audio_uri = %q, want gs:// で始まること", got.AudioURI)
	}
	if !strings.Contains(got.SignedURL, "X-Goog-Signature") {
		t.Errorf("signed_url に署名がありません: %q", got.SignedURL)
	}
	// 期限を伝えます。いつまで使えるかが分からないと、呼び出し側は
	// 切れたリンクを配ったことに気付けません。
	if got.ExpiresInSeconds <= 0 {
		t.Errorf("expires_in_seconds = %d", got.ExpiresInSeconds)
	}
}

// callAudio は GET /api/jobs/{jobID}/audio を呼びます。
func callAudio(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("GET", "/jobs/"+jobID+"/audio", nil)
	// 統合後は Accept が表現を決めます。MCP サーバーの baseClient は常に付けています。
	req.Header.Set("Accept", "application/json")
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Audio(rec, req)
	return rec
}
