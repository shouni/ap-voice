package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
)

// The submission form's tabs, split by input type.
//
// When everything sat on one form, the music promotion mode could be chosen with plain text entered;
// nothing happened on selection, and it failed at decoding once generation started.
// Things with different input types cannot be told apart when placed on the same page.
const (
	// tabInput is the tab that has a script written from an input source (a web URL / gs:// document).
	tabInput = "input"
	// tabRecipe is the tab that has a promotional narration written from a music recipe.
	tabRecipe = "recipe"
	// tabScript is the tab that synthesises a finished script JSON as is. It does not go through Gemini.
	tabScript = "script"
)

// formView is the value passed to the form page.
type formView struct {
	baseView
	// TextModes and RecipeModes are the modes split by the front matter's input.
	// Each tab renders its own <select>. Rearranging a single select in JavaScript
	// would move the basis for which options to show onto the page.
	TextModes   []assets.Mode
	RecipeModes []assets.Mode
	Models      []string
	Message     string
	Error       string
	Form        domain.Request
	// ActiveTab is the tab opened on re-render. The tab that was submitted is reopened.
	// Always opening the first one would show only the result message and leave the input on a hidden tab.
	ActiveTab string
	// MusicJobID and ScriptJSON are what remains of the input on the tabs other than the input source.
	// They do not fit in domain.Request (already converted to a URI / the script itself), so they are kept separately.
	MusicJobID string
	ScriptJSON string
}

// Home shows the submission form.
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, h.formView(r, domain.Request{Command: domain.CommandGenerate}, tabInput))
}

// formView builds the render value filled with each tab's modes.
func (h *Handler) formView(r *http.Request, form domain.Request, activeTab string) formView {
	return formView{
		baseView:    h.base(r),
		TextModes:   assets.FilterModes(h.modes, assets.InputText),
		RecipeModes: assets.FilterModes(h.modes, assets.InputRecipe),
		Models:      h.models,
		Form:        form,
		ActiveTab:   activeTab,
	}
}

// createJobForm validates the form and hands the run to the worker side
// (when POST /jobs is called from the form).
//
// It does not wait for synthesis here. It takes minutes, so it cannot complete within the request.
// The result arrives as a Slack notification and at the output location.
func (h *Handler) createJobForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{}, tabInput, "フォームの解析に失敗しました")
		return
	}

	switch r.FormValue("source") {
	case tabRecipe:
		h.enqueueFromRecipe(w, r)
	case tabScript:
		h.enqueueFromScript(w, r)
	default:
		h.enqueueFromInput(w, r)
	}
}

// enqueueFromInput is the submission from the 「入力ソース」 tab. It has Gemini read a web URL / gs://
// document and write the script.
func (h *Handler) enqueueFromInput(w http.ResponseWriter, r *http.Request) {
	command, ok := h.generateCommand(w, r, tabInput)
	if !ok {
		return
	}
	h.enqueueGenerate(w, r, command, strings.TrimSpace(r.FormValue("input_uri")), tabInput, "")
}

// enqueueFromRecipe is the submission from the 「楽曲レシピ」 tab.
//
// Instead of an input source it takes a job ID from the music generation service.
// It is shorter than pasting a gs:// path, and a typo is rejected by the ID's shape.
func (h *Handler) enqueueFromRecipe(w http.ResponseWriter, r *http.Request) {
	command, ok := h.generateCommand(w, r, tabRecipe)
	if !ok {
		return
	}

	musicJobID := strings.TrimSpace(r.FormValue("music_job_id"))
	inputURI, err := h.recipeInputURI(musicJobID)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{Mode: r.FormValue("mode")}, tabRecipe, err.Error())
		return
	}
	h.enqueueGenerate(w, r, command, inputURI, tabRecipe, musicJobID)
}

// recipeInputURI builds the music recipe's location from a music generation service job ID.
//
// It is the same rule as the video generation service's (gs://<musicBucket>/music/<jobID>/recipe.json).
// Drafts (drafts/) are not looked at — promotional narration belongs to published songs.
func (h *Handler) recipeInputURI(musicJobID string) (string, error) {
	if musicJobID == "" {
		return "", fmt.Errorf("楽曲のジョブIDを入力してください")
	}
	// Validate only checks that it is safe as a path, not its format —
	// "not-a-job-id" would pass too, and a job pointing at a location that does not exist would be enqueued.
	// SortKey returns empty for an ID it cannot extract a time from, so it serves as a shape check.
	if err := jobid.Validate(musicJobID); err != nil {
		return "", fmt.Errorf("楽曲のジョブIDに使えない文字が含まれています: %w", err)
	}
	if jobid.SortKey(musicJobID) == "" {
		return "", fmt.Errorf("楽曲のジョブIDの形式が正しくありません（例: music-20260814-031712-5c812debb05f）")
	}
	if strings.TrimSpace(h.musicBucket) == "" {
		return "", fmt.Errorf("AP_MUSIC_BUCKET が設定されていないため、ジョブIDから楽曲レシピを解決できません")
	}
	return fmt.Sprintf("gs://%s/music/%s/recipe.json", strings.TrimSpace(h.musicBucket), musicJobID), nil
}

// generateCommand checks the command sent by a generation tab's button.
//
// The commands this page can choose are limited. No tab but the script JSON tab can pass synthesize,
// and stating the accepted values here makes the boundary between what comes from the page and
// what comes from the API clear.
func (h *Handler) generateCommand(w http.ResponseWriter, r *http.Request, tab string) (domain.Command, bool) {
	command := domain.Command(r.FormValue("command"))
	if command != domain.CommandGenerate && command != domain.CommandGenerateAndSynthesize {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{}, tab, "この画面から実行できない処理です")
		return "", false
	}
	return command, true
}

// enqueueGenerate is the shared submission path of the generation tabs.
func (h *Handler) enqueueGenerate(w http.ResponseWriter, r *http.Request, command domain.Command, inputURI, tab, musicJobID string) {
	// The output location is not taken from the form. Deriving it from the job ID guarantees one job's
	// artifacts are gathered under one prefix, so they can be listed and deleted later.
	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, domain.Request{}, tab, "ジョブIDの発行に失敗しました")
		return
	}

	req := domain.Request{
		Command:   command,
		JobID:     jobID,
		InputURI:  inputURI,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      r.FormValue("mode"),
		AIModel:   r.FormValue("ai_model"),
	}

	// The worker also validates at the top of Execute, but rejecting before the enqueue avoids
	// "it became a task but will always fail" (this happens inside submit).
	status, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderError(w, r, status, req, tab, err.Error())
		return
	}
	acceptedAt(w, jobID)

	// What was submitted is kept as is. Making another one from the same source with a different mode
	// is the usual use, and clearing it would mean pasting the URL again.
	// The job ID and output location are issued afresh every time, so leaving them does not affect the next submission.
	view := h.formView(r, req, tab)
	view.MusicJobID = musicJobID
	view.Message = acceptedMessage(command, req.JobID)
	h.render(w, http.StatusAccepted, view)
}

// enqueueFromScript is the submission from the 「台本 JSON」 tab.
//
// It does not go through Gemini; it synthesises the pasted script as is. It mints a job ID and
// saves to that location, so the path for replacing an existing job's script becomes the creation
// (the location follows from the job ID, whether or not the job already exists).
func (h *Handler) enqueueFromScript(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.FormValue("script_json"))
	if raw == "" {
		h.renderScriptError(w, r, http.StatusBadRequest, raw, "台本のJSONを貼り付けてください")
		return
	}

	var script domain.Script
	if err := json.Unmarshal([]byte(raw), &script); err != nil {
		h.renderScriptError(w, r, http.StatusBadRequest, raw, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	// Speakers and styles go through the same validation as the API (validateScript).
	cleaned, err := h.validateScript(script)
	if err != nil {
		h.renderScriptError(w, r, http.StatusBadRequest, raw, err.Error())
		return
	}

	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		h.renderScriptError(w, r, http.StatusInternalServerError, raw, "ジョブIDの発行に失敗しました")
		return
	}

	if err := h.repo.SaveScript(r.Context(), jobID, cleaned); err != nil {
		h.renderScriptError(w, r, http.StatusBadGateway, raw, "台本の保存に失敗しました")
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	status, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderScriptError(w, r, status, raw, err.Error())
		return
	}
	acceptedAt(w, jobID)

	// The pasted JSON is not kept. A submitted script can be edited from the history detail page,
	// and leaving the same one pasted, ready to submit twice, is the greater danger.
	view := h.formView(r, domain.Request{Command: domain.CommandGenerate}, tabScript)
	view.Message = fmt.Sprintf("音声の作成を受け付けました（%s）。完了すると通知が届きます。", jobID)
	h.render(w, http.StatusAccepted, view)
}

// acceptedMessage returns guidance matching the accepted action.
// The wording says which button was pressed. When both are made together the wait lasts until
// the audio, so "it will appear in the history" alone does not say what to wait for next.
func acceptedMessage(command domain.Command, jobID string) string {
	if command == domain.CommandGenerateAndSynthesize {
		return fmt.Sprintf("台本と音声の作成を受け付けました（%s）。完了すると通知が届きます。", jobID)
	}
	return fmt.Sprintf("台本の作成を受け付けました（%s）。完了すると履歴に並びます。", jobID)
}

// renderError re-renders a generation tab's failure with that tab open.
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, form domain.Request, tab, msg string) {
	view := h.formView(r, form, tab)
	view.MusicJobID = strings.TrimSpace(r.FormValue("music_job_id"))
	view.Error = msg
	h.render(w, status, view)
}

// renderScriptError re-renders a script JSON tab failure, keeping the pasted JSON.
// It is kept here. Fixing and resubmitting is the usual course, and losing it means pasting again.
func (h *Handler) renderScriptError(w http.ResponseWriter, r *http.Request, status int, raw, msg string) {
	view := h.formView(r, domain.Request{Command: domain.CommandGenerate}, tabScript)
	view.ScriptJSON = raw
	view.Error = msg
	h.render(w, status, view)
}

func (h *Handler) render(w http.ResponseWriter, status int, view formView) {
	h.renderTemplate(w, status, "home.html", &view)
}
