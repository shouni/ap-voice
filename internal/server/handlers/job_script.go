package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-voicevox/speaker"
)

// Script returns the stored script.
//
// Both kinds of reader get JSON, but a person is made to save it as a file.
// If the browser displayed the body when opened from the page, it could be checked but
// not kept locally.
func (h *Handler) Script(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	script, err := h.repo.Load(r.Context(), jobID)
	switch {
	case errors.Is(err, domain.ErrScriptNotFound):
		respond.Error(w, r, http.StatusNotFound, "台本が見つかりません")
		return
	case err != nil:
		// A merely unreadable script is not folded into 404. Otherwise, during a GCS outage every job
		// would look like it "has no script", and the caller would quietly accept that.
		respond.ServerError(w, r, http.StatusBadGateway, err, "台本の取得に失敗しました", "job_id", jobID)
		return
	}

	if !respond.WantsJSON(w, r) {
		// Only IDs that passed jobid.Validate reach here, so it can be used as a file name.
		w.Header().Set("Content-Disposition", `attachment; filename="`+jobID+`.json"`)
	}
	respond.JSON(w, r, http.StatusOK, script)
}

// ScriptUpdate replaces the script (PUT /jobs/{jobID}/script). It does not synthesise.
//
// Saving and synthesis are separate so that a client can fix the script a few times and then
// synthesise once. The page combines the two into one button, but for machines
// having them separate is easier to use.
func (h *Handler) ScriptUpdate(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	var script domain.Script
	if err := json.NewDecoder(r.Body).Decode(&script); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	// The same validation as the page (validateScript).
	cleaned, err := h.validateScript(script)
	if err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.SaveScript(r.Context(), jobID, cleaned); err != nil {
		respond.ServerErrorJSON(w, r, http.StatusBadGateway, err, "台本の保存に失敗しました", "job_id", jobID)
		return
	}
	respond.JSON(w, r, http.StatusOK, cleaned)
}

// synthesizeSaved makes audio from the stored script (when POST /jobs/{jobID}/synthesize is called with JSON).
func (h *Handler) synthesizeSaved(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	status, err := h.submit(r.Context(), req)
	if err != nil {
		respond.ErrorJSON(w, r, status, err.Error())
		return
	}
	acceptedAt(w, jobID)
	respond.JSON(w, r, status, apiAccepted{Status: string(jobstatus.StateQueued), JobID: jobID, Command: string(domain.CommandSynthesize)})
}

// validateScript checks that every line of the script uses an existing speaker and style, and
// returns the script with empty-text lines dropped.
//
// It is the shared entry point for the form and the API. Written for only one of them, the other
// could store non-existent pairs.
func (h *Handler) validateScript(script domain.Script) (domain.Script, error) {
	if len(script.Lines) == 0 {
		return domain.Script{}, errors.New("台本が空です")
	}
	if len(script.Lines) > maxScriptLines {
		return domain.Script{}, fmt.Errorf("台本が長すぎます（%d 行、上限 %d 行）", len(script.Lines), maxScriptLines)
	}

	lines := make([]domain.ScriptLine, 0, len(script.Lines))
	for i, line := range script.Lines {
		text := strings.TrimSpace(line.Text)
		if text == "" {
			// Empty lines are dropped. It is also the way to delete a line.
			continue
		}
		valid, ok := h.speakers.StylesFor(line.Speaker)
		if !ok {
			return domain.Script{}, fmt.Errorf("%d 行目: 話者 %q は一覧にありません", i+1, line.Speaker)
		}
		if !slices.Contains(valid, line.Style) {
			return domain.Script{}, fmt.Errorf("%d 行目: %q に %q というスタイルはありません", i+1, line.Speaker, line.Style)
		}
		lines = append(lines, domain.ScriptLine{Speaker: line.Speaker, Style: line.Style, Text: text})
	}
	if len(lines) == 0 {
		return domain.Script{}, errors.New("本文のある行がありません")
	}

	return domain.Script{Title: strings.TrimSpace(script.Title), Lines: lines}, nil
}

// stylesBySpeaker builds the mapping from speaker name to that speaker's style names.
//
// Only NewHandler calls it. The speaker roster does not change after startup, so the editor and
// /speakers reuse its result (h.styles / h.stylesJSON).
func stylesBySpeaker(speakers *speaker.Registry) map[string][]string {
	names := speakers.SpeakerNames()
	out := make(map[string][]string, len(names))
	for _, name := range names {
		if styles, ok := speakers.StylesFor(name); ok {
			out[name] = styles
		}
	}
	return out
}

// Synthesize makes audio from the script (POST /jobs/{jobID}/synthesize).
//
// The form carries the script being edited in the body, so it is saved before synthesis. JSON
// (no body) synthesises the stored script as is. Machines fix it a few times with PUT /jobs/{jobID}/script
// and then call this once; the page does both with one button.
func (h *Handler) Synthesize(w http.ResponseWriter, r *http.Request) {
	if isFormBody(r) {
		h.synthesizeFromForm(w, r)
		return
	}
	h.synthesizeSaved(w, r)
}

// synthesizeFromForm saves the edited script and then requests the audio
// (when POST /jobs/{jobID}/synthesize is called from the form).
//
// Saving comes first, and only the job ID goes to the task (see domain.Request.JobID).
// The worker uses its existing "read the stored script" path as is.
func (h *Handler) synthesizeFromForm(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderDetail(w, r, jobID, http.StatusBadRequest, "", "フォームの解析に失敗しました")
		return
	}

	script, err := h.scriptFromForm(r)
	if err != nil {
		h.renderDetail(w, r, jobID, http.StatusBadRequest, "", err.Error())
		return
	}

	if err := h.repo.SaveScript(r.Context(), jobID, script); err != nil {
		h.renderDetail(w, r, jobID, http.StatusBadGateway, "", "台本の保存に失敗しました")
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	status, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderDetail(w, r, jobID, status, "", err.Error())
		return
	}

	acceptedAt(w, jobID)
	h.renderDetail(w, r, jobID, status, "台本を保存し、音声の作成を受け付けました。完了すると通知が届きます。", "")
}

// scriptFromForm assembles the submitted lines into the domain script.
//
// It only assembles; validateScript checks that they exist.
// It goes through the same function as the API, so neither one can be looser than the other.
func (h *Handler) scriptFromForm(r *http.Request) (domain.Script, error) {
	speakers := r.Form["speaker"]
	styles := r.Form["style"]
	texts := r.Form["text"]

	if len(speakers) != len(styles) || len(speakers) != len(texts) {
		return domain.Script{}, errors.New("台本の項目数が揃っていません")
	}

	lines := make([]domain.ScriptLine, 0, len(speakers))
	for i := range speakers {
		lines = append(lines, domain.ScriptLine{Speaker: speakers[i], Style: styles[i], Text: texts[i]})
	}

	return h.validateScript(domain.Script{Title: r.FormValue("title"), Lines: lines})
}
