package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// apiAccepted is the response when an enqueue is accepted.
//
// It is the same envelope as the sibling services (the MCP server's client.TaskQueuedResponse reads
// status and job_id). If only this one had a different shape, the shared client would need an
// ap-voice-specific branch.
type apiAccepted struct {
	Status string `json:"status"`
	JobID  string `json:"job_id"`
	// Command is ap-voice's own. It returns which of the three commands was accepted.
	Command string `json:"command,omitempty"`
}

// apiEnqueue is the job enqueue request.
//
// There are two ways in: have the AI write from an input source (the generate family),
// or hand over a script already in hand as is (synthesize). The latter is the path for the
// caller to have its own script spoken, and does not call Gemini.
type apiEnqueue struct {
	Command  string `json:"command"`
	InputURI string `json:"input_uri,omitempty"`
	// MusicJobID is the job ID when the input is a music recipe.
	//
	// It is resolved here. Making callers assemble the gs:// would leak the storage layout rule
	// outside the service, and changing it would mean telling everyone.
	// It goes through the same function as the page's 「楽曲レシピ」 tab.
	MusicJobID string `json:"music_job_id,omitempty"`
	Mode       string `json:"mode,omitempty"`
	AIModel    string `json:"ai_model,omitempty"`
	// Script is the script when command is synthesize.
	Script *domain.Script `json:"script,omitempty"`
}

// createJobJSON enqueues a new job from a JSON body (POST /jobs).
func (h *Handler) createJobJSON(w http.ResponseWriter, r *http.Request) {
	var body apiEnqueue
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	command := domain.Command(body.Command)
	switch command {
	case domain.CommandGenerate, domain.CommandGenerateAndSynthesize, domain.CommandSynthesize:
	default:
		respond.ErrorJSON(w, r, http.StatusBadRequest, fmt.Sprintf("command は %q / %q / %q です",
			domain.CommandGenerate, domain.CommandGenerateAndSynthesize, domain.CommandSynthesize))
		return
	}

	// music_job_id takes precedence over input_uri (like the video generation service's compose_video).
	// Rather than silently dropping one when both arrive, it follows the intent of whoever wrote the ID.
	inputURI := body.InputURI
	if body.MusicJobID != "" {
		resolved, rErr := h.recipeInputURI(body.MusicJobID)
		if rErr != nil {
			respond.ErrorJSON(w, r, http.StatusBadRequest, rErr.Error())
			return
		}
		inputURI = resolved
	}

	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		respond.ServerErrorJSON(w, r, http.StatusInternalServerError, err, "ジョブIDの発行に失敗しました")
		return
	}

	req := domain.Request{
		Command:   command,
		JobID:     jobID,
		InputURI:  inputURI,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      body.Mode,
		AIModel:   body.AIModel,
	}

	// A supplied script is saved before the enqueue. Its location follows from the job ID, so
	// it is the same path as replacing an existing job's script (whether the job already exists does not matter).
	if command == domain.CommandSynthesize {
		if body.Script == nil {
			respond.ErrorJSON(w, r, http.StatusBadRequest, "synthesize には script が要ります")
			return
		}
		cleaned, vErr := h.validateScript(*body.Script)
		if vErr != nil {
			respond.ErrorJSON(w, r, http.StatusBadRequest, vErr.Error())
			return
		}
		if saveErr := h.repo.SaveScript(r.Context(), jobID, cleaned); saveErr != nil {
			respond.ServerErrorJSON(w, r, http.StatusBadGateway, saveErr, "台本の保存に失敗しました", "job_id", jobID)
			return
		}
	}

	status, err := h.submit(r.Context(), req)
	if err != nil {
		respond.ErrorJSON(w, r, status, err.Error())
		return
	}

	acceptedAt(w, jobID)
	respond.JSON(w, r, status, apiAccepted{Status: string(jobstatus.StateQueued), JobID: jobID, Command: string(command)})
}

// JobCreate enqueues a new job (POST /jobs).
//
// There is one way in, split by the body's shape. JSON is for machines (the MCP server), the form
// for the page's three tabs. Only the reading and the response shape differ; ID minting, saving and enqueueing are the same path.
func (h *Handler) JobCreate(w http.ResponseWriter, r *http.Request) {
	if isJSONBody(r) {
		h.createJobJSON(w, r)
		return
	}
	h.createJobForm(w, r)
}

// isJSONBody decides from Content-Type whether the body is JSON.
func isJSONBody(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

// isFormBody decides from Content-Type whether the body is an HTML form.
func isFormBody(r *http.Request) bool {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.Contains(ct, "application/x-www-form-urlencoded") || strings.Contains(ct, "multipart/form-data")
}

// acceptedAt puts the polling URL on enqueue and action responses.
//
// Callers know the next URL to call without reading the body. Responses for the page carry
// the same header (so how it reads does not change with Accept).
func acceptedAt(w http.ResponseWriter, jobID string) {
	w.Header().Set("Location", jobsBasePath+"/"+jobID)
}

// jobsBasePath is the path of jobs. One job is named under it from enqueue to delete.
const jobsBasePath = "/jobs"
