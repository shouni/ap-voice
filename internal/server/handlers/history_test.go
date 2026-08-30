package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-voice/internal/domain"
)

// storedScriptRepo は、保存済みの台本を返すフェイクです。
type storedScriptRepo struct {
	ScriptRepository
	script domain.Script
}

func (r *storedScriptRepo) Load(_ context.Context, _ string) (domain.Script, error) {
	return r.script, nil
}

// getScript は GET /history/{jobID}/script を呼びます。
func getScript(t *testing.T, h *Handler, jobID string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/history/"+jobID+"/script", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Script(rec, req)
	return rec
}

// TestScriptDownloadsTheStoredScript は、保存済みの台本がそのまま JSON として
// 落ちてくることを検証します。
//
// ファイル名を付ける Content-Disposition が要ります。無いとブラウザは
// JSON を画面に表示するだけで、ダウンロードにならない経路があります。
func TestScriptDownloadsTheStoredScript(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	want := domain.Script{
		Title: "テスト台本",
		Lines: []domain.ScriptLine{
			{Speaker: "ずんだもん", Style: "ノーマル", Text: "こんにちはなのだ"},
		},
	}
	h := apiHandler(t, &storedScriptRepo{script: want})

	rec := getScript(t, h, jobID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="`+jobID+`.json"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}

	var got domain.Script
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("台本が JSON として読めません: %v (body=%q)", err, rec.Body.String())
	}
	if got.Title != want.Title || len(got.Lines) != len(want.Lines) || got.Lines[0] != want.Lines[0] {
		t.Errorf("script = %+v, want %+v", got, want)
	}
}

// TestScriptRejectsBadJobID は、ジョブ ID の検証を通ることを確認します。
// ID はそのままファイル名になるため、素通りさせられません。
func TestScriptRejectsBadJobID(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &storedScriptRepo{})

	rec := getScript(t, h, "../../etc/passwd")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
