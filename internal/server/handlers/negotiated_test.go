package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// browserAccept は、ブラウザが実際に送る Accept です。
// application/json を含まないので、表現は HTML 側に倒れます。
const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// requestWithJobID は、jobID を埋めたリクエストを組み立てます。
func requestWithJobID(method, target, accept, jobID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Accept", accept)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}

// 同じルートが、相手の求める表現で答えること。
//
// MCP サーバーの baseClient は全リクエストに Accept: application/json を付けるため、
// ルートを 1 本にしても機械側は JSON を受け取り続けます。
func TestScriptServesBothAudiences(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	h := &Handler{repo: &storedScriptRepo{}}

	t.Run("機械には素の JSON を返す", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/history/"+jobID+"/script", "application/json", jobID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("Content-Disposition = %q, want empty（機械はファイルとして受け取らない）", got)
		}
	})

	// 画面から開いたときに本文が表示されるだけだと、確認はできても手元に残せません。
	t.Run("人にはファイルとして返す", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/history/"+jobID+"/script", browserAccept, jobID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Disposition"); got == "" {
			t.Error("Content-Disposition が無い（ダウンロードにならない）")
		}
	})

	// 同じ URL が Accept で中身を変えるため、キャッシュへ伝える必要があります。
	t.Run("Vary: Accept を立てること", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/history/"+jobID+"/script", "application/json", jobID))

		if got := rec.Header().Get("Vary"); got != "Accept" {
			t.Errorf("Vary = %q, want %q", got, "Accept")
		}
	})
}

// 不正なジョブ ID のエラーも、要求された表現で返ること。
// 機械に HTML のエラーページを返しても解釈できません。
func TestJobIDErrorFollowsTheRequestedFormat(t *testing.T) {
	t.Parallel()

	h := &Handler{repo: &storedScriptRepo{}}

	rec := httptest.NewRecorder()
	h.Script(rec, requestWithJobID(http.MethodGet, "/history/bad/script", "application/json", "../etc/passwd"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
}
