package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-voice/internal/repository"
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

// listingRepo は、要求されたページ幅と絞り込みを覚えておくフェイクです。
type listingRepo struct {
	ScriptRepository
	perPage int
	opts    int
}

func (r *listingRepo) List(_ context.Context, _, perPage int, opts ...jobstatus.ListOption) ([]repository.Job, jobstatus.PageMeta, error) {
	r.perPage = perPage
	r.opts = len(opts)
	return nil, jobstatus.PageMeta{}, nil
}

// TestJobsCapsPerPage は、?per_page= が上限で頭打ちになることを検証します。
//
// 上限が無かった頃は ?per_page=100000 がそのまま 1 クエリになり、呼び出し側の
// 打ち間違いを倉庫が引き受けていました。画面は既定しか使わないので、
// これが効くのは機械から叩く経路だけです。
func TestJobsCapsPerPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query string
		want  int
	}{
		{query: "", want: historyPerPage},
		{query: "?per_page=10", want: 10},
		{query: "?per_page=100000", want: maxPerPage},
		// 0 以下は指定が無いのと同じ扱いです（実物は 0 以下を「全件」と読むため、
		// 素通しすると上限を外す抜け道になります）。
		{query: "?per_page=0", want: historyPerPage},
		{query: "?per_page=-1", want: historyPerPage},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()

			repo := &listingRepo{}
			h := &Handler{repo: repo, templates: parseTemplates(t)}
			req := httptest.NewRequest(http.MethodGet, "/history"+tt.query, nil)
			req.Header.Set("Accept", "application/json")

			h.Jobs(httptest.NewRecorder(), req)

			if repo.perPage != tt.want {
				t.Errorf("perPage = %d, want %d", repo.perPage, tt.want)
			}
		})
	}
}

// TestJobsFiltersByState は、?state= が絞り込みとして渡ることを検証します。
//
// 記録には状態が入っているのに、一覧は全件しか出せませんでした。「失敗した
// ジョブだけ」を見るのに、人が目で探すことになります。
func TestJobsFiltersByState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query      string
		wantStatus int
		wantOpts   int
	}{
		{query: "", wantStatus: http.StatusOK, wantOpts: 0},
		{query: "?state=failed", wantStatus: http.StatusOK, wantOpts: 1},
		{query: "?state=running", wantStatus: http.StatusOK, wantOpts: 1},
		// 打ち間違いを黙って全件にしません。「失敗は無い」と読めてしまいます。
		{query: "?state=broken", wantStatus: http.StatusBadRequest, wantOpts: 0},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			t.Parallel()

			repo := &listingRepo{}
			h := &Handler{repo: repo, templates: parseTemplates(t)}
			req := httptest.NewRequest(http.MethodGet, "/history"+tt.query, nil)
			req.Header.Set("Accept", "application/json")

			rec := httptest.NewRecorder()
			h.Jobs(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if repo.opts != tt.wantOpts {
				t.Errorf("絞り込みの数 = %d, want %d", repo.opts, tt.wantOpts)
			}
		})
	}
}
