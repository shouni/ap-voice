package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestJobsCapsPerPage verifies that ?per_page= is capped at the bound.
//
// Before there was a bound, ?per_page=100000 became one query as is, and the store absorbed
// the caller's typo. The pages use only the default, so
// this matters only on the machine path.
func TestJobListCapsPerPage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		query string
		want  int
	}{
		{query: "", want: historyPerPage},
		{query: "?per_page=10", want: 10},
		{query: "?per_page=100000", want: maxPerPage},
		// Zero or less is treated as not given (the real store reads zero or less as "everything",
		// so passing it through would be a loophole around the cap).
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

// TestJobsFiltersByState verifies that ?state= is passed on as a filter.
//
// The records held the state, yet the listing could only show everything. Seeing "only the failed
// jobs" meant a person searching by eye.
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
		// A typo is not silently turned into everything. It would read as "there are no failures".
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
				t.Errorf("filter count = %d, want %d", repo.opts, tt.wantOpts)
			}
		})
	}
}
