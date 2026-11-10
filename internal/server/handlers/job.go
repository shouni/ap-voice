package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// This file gathers the paths on which people and machines look at the same resource.
//
// Giving two handlers to something that differs only in representation means fixing one leaves
// the page and the machine-readable result disagreeing. Fetching and validation happen once,
// and Accept decides HTML or JSON at the end.
//
// Conversely, operations only one kind of reader has (input forms, synthesis requests and so on)
// are separate resources, so they do not live in this file.

// jobIDParam extracts and validates the job ID from the URL.
//
// It writes no response, because the same validation is answered in three ways (the page, chosen
// by Accept; the JSON-only API; plain text). Only extraction and validation are gathered here,
// and the caller decides how to respond. The ID goes straight into the object path, so
// an unvalidated value must not be passed on.
func jobIDParam(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "jobID")
	if err := jobid.Validate(id); err != nil {
		return "", false
	}
	return id, true
}

// jobID extracts the job ID from the URL and returns an error in the requested representation.
func (h *Handler) jobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := jobIDParam(r)
	if !ok {
		respond.Error(w, r, http.StatusBadRequest, "不正なジョブIDです")
		return "", false
	}
	return id, true
}

// JobDelete deletes the job and its artifacts together (DELETE /jobs/{jobID}).
//
// The page's delete button also sends DELETE through fetch (App.deleteResource in app.js).
func (h *Handler) JobDelete(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), jobID); err != nil {
		if errors.Is(err, repository.ErrJobNotFound) {
			respond.Error(w, r, http.StatusNotFound, "ジョブが見つかりません")
			return
		}
		respond.ServerError(w, r, http.StatusBadGateway, err, "ジョブの削除に失敗しました", "job_id", jobID)
		return
	}

	if respond.WantsJSON(w, r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The detail page of what was deleted cannot open, so it goes back to the listing.
	http.Redirect(w, r, jobsBasePath, http.StatusSeeOther)
}

// JobStatus returns the job's progress.
//
// It is the only way for the enqueuer to learn of completion and failure. Whether artifacts exist
// cannot tell still-running from failed. The format is gcp-kit's
// jobstatus.Status, the same shape as the sibling services.
//
// With no record (ErrNotFound) it is a 404. The MCP server treats that as unknown, so
// "jobs from before the status feature" and "just enqueued" are not tool failures.
//
// Merely unreadable is not mixed into 404. Turning permission or GCS failures (ErrUnavailable)
// into 404 would make every job look "unrecorded" during an outage,
// and the poller would quietly accept that as unknown.
//
// There is one representation (JSON). It is what GET /jobs/{jobID} returns when JSON is asked for,
// and Job calls it.
func (h *Handler) jobStatus(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	status, err := h.repo.Get(r.Context(), jobID)
	switch {
	case errors.Is(err, jobstatus.ErrNotFound):
		respond.ErrorJSON(w, r, http.StatusNotFound, "ジョブ状態が見つかりません")
		return
	case err != nil:
		respond.ServerErrorJSON(w, r, http.StatusBadGateway, err, "ジョブ状態の取得に失敗しました", "job_id", jobID)
		return
	}
	respond.JSON(w, r, http.StatusOK, status)
}

// Job returns one job (GET /jobs/{jobID}).
//
// The same URL names it from the moment it is enqueued until it is deleted. JSON is the progress (JobStatus),
// HTML the detail page for fixing the script. Callers do not have to switch
// "which one to call now" by state.
func (h *Handler) Job(w http.ResponseWriter, r *http.Request) {
	if respond.WantsJSON(w, r) {
		h.jobStatus(w, r)
		return
	}
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}
	h.renderDetail(w, r, jobID, http.StatusOK, "", "")
}

// apiJobID extracts and validates the job ID from the URL.
//
// The validation itself is jobIDParam. This only owns how to respond; it is fixed to JSON because
// this path returns JSON unconditionally on success too (so a caller that sends no Accept
// need not read the body differently for success and failure).
func (h *Handler) apiJobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	jobID, ok := jobIDParam(r)
	if !ok {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "不正なジョブIDです")
		return "", false
	}
	return jobID, true
}

// detailView is the value passed to the detail page.
type detailView struct {
	baseView
	JobID string
	// Script is the stored script. Its content is checked and fixed here before the audio is made.
	Script domain.Script
	// HasScript reports whether a script is stored. When false, the state is shown instead of
	// the edit form. No script is not an anomaly: it is the normal shape of a job mid-generation
	// or one whose generation failed, and if the page could not open then, deletion would be lost too.
	HasScript bool
	// State is the recorded progress state (queued / running / succeeded / failed).
	// Empty when the record cannot be read, and the page then omits the state field.
	State jobstatus.State
	// JobError is the recorded failure reason. Set only when State is failed.
	// It differs from the page's Error (this operation's failure), hence the separate name.
	JobError string
	// HasAudio reports whether audio already exists. Without it only 「音声を作成」 is shown.
	HasAudio bool
	// Speakers is the list of speaker names, StylesJSON the existing styles per speaker.
	// The latter goes to JS to narrow the options by speaker.
	Speakers []string
	// InputURI is the input source the script was made from. Set only when recorded, and it also
	// decides whether 「同じ入力で作り直す」 is shown. A supplied script has no input source,
	// so no button is shown that could not regenerate anything.
	InputURI string
	// MaxLines is the cap on lines accepted in one script. It goes to the page because, since lines
	// can be added there, the page needs the same cap. Copying the number into JS would leave one
	// stale when the other is fixed (a script over the cap is rejected on save, and the page then
	// reloads the stored script, losing what was being edited).
	MaxLines int
	// StylesJSON goes into a data attribute, so it is a plain string. As template.JS
	// html/template would pass it through, allowing a breakout when the value contains </script>.
	// In attribute context it is reliably escaped, and the page reads it back from dataset.
	StylesJSON string
	Message    string
	Error      string
}

// maxScriptLines is the cap on lines accepted in one script.
// A form can assemble any number of lines, so without a cap one request could queue
// unbounded synthesis. It is wider than the prompt's target (at most 80 utterances).
const maxScriptLines = 200

// Regenerate rewrites the script from the same input source (POST /jobs/{jobID}/regenerate).
//
// This is where a failed job goes. A job that failed before writing a script has nothing to fix,
// and until now the only way was to delete it and paste the URL into the submission form again.
// What was read is in the record, so there is no reason to make anyone paste it again.
//
// The job ID does not change. It is a redo, so two entries in the history would be pointless, and
// with the same ID the artifacts' location and record are simply overwritten. The enqueue record
// goes back to queued, so the re-run guard (do not run if complete) does not catch it either.
func (h *Handler) Regenerate(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	status, err := h.repo.Get(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, jobstatus.ErrNotFound) {
			h.renderDetail(w, r, jobID, http.StatusNotFound, "", "このジョブの記録が無いため、作り直せません。")
			return
		}
		h.renderDetail(w, r, jobID, http.StatusBadGateway, "", "ジョブ状態を読めませんでした")
		return
	}
	if status.InputURI == "" {
		// A job with a supplied script (the script JSON tab, the API's script) has no input
		// source. There is nothing to regenerate from, so the button itself is not shown.
		h.renderDetail(w, r, jobID, http.StatusBadRequest, "",
			"このジョブには入力ソースの記録が無いため、作り直せません。")
		return
	}

	req := domain.Request{
		Command:   domain.CommandGenerate,
		JobID:     jobID,
		InputURI:  status.InputURI,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      status.Mode,
		AIModel:   status.AIModel,
	}
	code, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderDetail(w, r, jobID, code, "", err.Error())
		return
	}

	acceptedAt(w, jobID)
	h.renderDetail(w, r, jobID, code, "同じ入力から台本を作り直しています。完了すると通知が届きます。", "")
}

// renderDetail renders the detail page. It rereads the script every time.
//
// It renders even without a script yet. This page is also the job's entry point, and deletion is
// possible only from here, so if it could not open for a failed job, the job would stay in the history undeletable.
func (h *Handler) renderDetail(w http.ResponseWriter, r *http.Request, jobID string, status int, message, errMsg string) {
	view := detailView{
		baseView: h.base(r),
		JobID:    jobID,
		Speakers: h.speakers.SpeakerNames(),
		MaxLines: maxScriptLines,
		Message:  message,
		Error:    errMsg,
	}
	view.State, view.JobError, view.InputURI = h.jobState(r, jobID)

	script, err := h.repo.Load(r.Context(), jobID)
	switch {
	case errors.Is(err, domain.ErrScriptNotFound):
		// A job mid-generation, or one whose generation failed. The page shows only the state.
	case err != nil:
		http.Error(w, "台本の取得に失敗しました", http.StatusBadGateway)
		return
	default:
		view.Script, view.HasScript = script, true
	}

	hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
	if err != nil {
		http.Error(w, "音声の有無を確認できませんでした", http.StatusBadGateway)
		return
	}
	view.HasAudio = hasAudio

	view.StylesJSON = h.stylesJSON

	h.renderTemplate(w, status, "detail.html", &view)
}

// jobState returns the recorded progress state, failure reason and input source.
//
// The page renders even if they cannot be read. The state is not an artifact, so it may be absent
// (jobs from before the status feature) or temporarily unreadable. Stopping the whole page there
// would keep its real purpose, fixing the script, from being served because of the state.
func (h *Handler) jobState(r *http.Request, jobID string) (jobstatus.State, string, string) {
	status, err := h.repo.Get(r.Context(), jobID)
	if err != nil {
		if !errors.Is(err, jobstatus.ErrNotFound) {
			slog.WarnContext(r.Context(), "could not read the job state", "job_id", jobID, "error", err)
		}
		return "", "", ""
	}
	return status.State, status.Error, status.InputURI
}
