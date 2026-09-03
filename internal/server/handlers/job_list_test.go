package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJobsCapsPerPage は、?per_page= が上限で頭打ちになることを検証します。
//
// 上限が無かった頃は ?per_page=100000 がそのまま 1 クエリになり、呼び出し側の
// 打ち間違いを倉庫が引き受けていました。画面は既定しか使わないので、
// これが効くのは機械から叩く経路だけです。
func TestJobListCapsPerPage(t *testing.T) {
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
			req := httptest.NewRequest(http.MethodGet, "/jobs"+tt.query, nil)
			req.Header.Set("Accept", "application/json")

			h.JobList(httptest.NewRecorder(), req)

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
func TestJobListFiltersByState(t *testing.T) {
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
			req := httptest.NewRequest(http.MethodGet, "/jobs"+tt.query, nil)
			req.Header.Set("Accept", "application/json")

			rec := httptest.NewRecorder()
			h.JobList(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if repo.opts != tt.wantOpts {
				t.Errorf("絞り込みの数 = %d, want %d", repo.opts, tt.wantOpts)
			}
		})
	}
}
