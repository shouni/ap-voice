package handlers

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/shouni/ap-voice/internal/repository"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
)

// apiTimeFormat is the time format the listing returns.
const apiTimeFormat = "2006-01-02T15:04:05Z07:00"

// apiJob is one entry of the listing.
type apiJob struct {
	JobID     string `json:"job_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	HasAudio  bool   `json:"has_audio"`
	// State is the progress state. Whether artifacts exist cannot tell running from failed.
	// It is included so the listing can tell them apart without fetching each status.
	// Old jobs without a record leave it empty, hence omitempty.
	State string `json:"state,omitempty"`
}

// apiJobPage is the paged listing response.
//
// The metadata has the shape of gcp-kit's PageMeta. The JSON tags give the same JSON as the
// sibling services, so callers do not need to read each service differently.
type apiJobPage struct {
	Jobs []apiJob           `json:"jobs"`
	Page jobstatus.PageMeta `json:"page"`
}

// historyPerPage is the number of entries per page.
//
// The listing does not read artifacts, so raising it only increases the number of records read
// (one entry is one row and uses only the title, state and whether audio exists). Halving the
// number of page-forward clicks is worth more than 50 reads per page.
const historyPerPage = 100

// maxPerPage is the upper bound that ?per_page= can request.
//
// Without a bound, one request becomes one unbounded query on the store. It equals the default
// but stays a separate constant — one is "what happens when nothing is given", the other
// "how much can be asked for", and they change for different reasons.
// The pages use only the default, so this matters only on the machine path.
const maxPerPage = 100

// pageParam reads ?page=. An invalid value is treated as page 1.
// Showing an error page while browsing a listing leaves the user nothing to do.
func pageParam(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// historyView is the value passed to the history listing.
type historyView struct {
	baseView
	Jobs []repository.Job
	Page jobstatus.PageMeta
	// Filter is the active filter (empty means everything). Unless the pager links carry it,
	// the filter drops on page 2.
	Filter string
}

// listableStates are the values ?state= accepts.
//
// It uses jobstatus's vocabulary as is. Inventing a spelling here would make the values written
// in the records differ from the values the page accepts.
func listableStates() []string {
	return []string{
		string(jobstatus.StateQueued),
		string(jobstatus.StateRunning),
		string(jobstatus.StateSucceeded),
		string(jobstatus.StateFailed),
	}
}

// stateParam reads ?state=. Empty means no filter; an unknown value returns false.
//
// Unlike page, it is not silently ignored. A mistyped filter returning everything would read
// as "there are no failed jobs".
func stateParam(r *http.Request) (jobstatus.State, bool) {
	value := strings.TrimSpace(r.URL.Query().Get("state"))
	if value == "" {
		return "", true
	}
	if !slices.Contains(listableStates(), value) {
		return "", false
	}
	return jobstatus.State(value), true
}

// JobList returns jobs newest first (GET /jobs). It accepts ?page= and ?per_page=.
func (h *Handler) JobList(w http.ResponseWriter, r *http.Request) {
	perPage := historyPerPage
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		// Capped at the bound. When anything passed, ?per_page=100000 became a single query
		// as is, and the store absorbed the caller's typo.
		perPage = min(n, maxPerPage)
	}

	state, ok := stateParam(r)
	if !ok {
		respond.Error(w, r, http.StatusBadRequest, "state は "+strings.Join(listableStates(), " / ")+" です")
		return
	}

	var opts []jobstatus.ListOption
	if state != "" {
		opts = append(opts, jobstatus.WithState(state))
	}

	jobs, meta, err := h.repo.List(r.Context(), pageParam(r), perPage, opts...)
	if err != nil {
		// The filter needs a composite index (state and queued_at). The index belongs to the deployment
		// configuration, so in an environment without it this is reached only when filtering.
		respond.ServerError(w, r, http.StatusBadGateway, err, "履歴の取得に失敗しました", "state", state)
		return
	}

	if respond.WantsJSON(w, r) {
		out := make([]apiJob, 0, len(jobs))
		for _, job := range jobs {
			out = append(out, apiJob{
				JobID: job.ID, Title: job.Title,
				CreatedAt: job.CreatedAt.Format(apiTimeFormat),
				HasAudio:  job.HasAudio,
				State:     string(job.State),
			})
		}
		respond.JSON(w, r, http.StatusOK, apiJobPage{Jobs: out, Page: meta})
		return
	}
	h.renderTemplate(w, http.StatusOK, "history.html", &historyView{
		baseView: h.base(r), Jobs: jobs, Page: meta, Filter: string(state),
	})
}
