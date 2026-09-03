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

// apiAccepted は投入を受け付けたときの応答です。
//
// 姉妹サービスと同じ封筒です（MCP サーバーの client.TaskQueuedResponse が
// status と job_id を読みます）。ここだけ形が違うと、共通クライアントに
// ap-voice 用の分岐が要ります。
type apiAccepted struct {
	Status string `json:"status"`
	JobID  string `json:"job_id"`
	// Command は ap-voice 独自です。3 つのコマンドのどれを受け付けたかを返します。
	Command string `json:"command,omitempty"`
}

// apiEnqueue は、ジョブ投入の要求です。
//
// 入口は 2 つあります。入力ソースから AI に書かせるか（generate 系）、
// 既に手元にある台本をそのまま渡すか（synthesize）です。後者は呼び出し側が
// 自分で書いた台本を喋らせる経路で、Gemini を呼びません。
type apiEnqueue struct {
	Command  string `json:"command"`
	InputURI string `json:"input_uri,omitempty"`
	// MusicJobID は、入力が楽曲レシピのときのジョブ ID です。
	//
	// 解決はここでやります。呼び出し側に gs:// を組み立てさせると、
	// 置き場の規則がサービスの外へ漏れ、変えるときに全員へ知らせて回ることに
	// なります。画面の「楽曲レシピ」タブと同じ関数を通ります。
	MusicJobID string `json:"music_job_id,omitempty"`
	Mode       string `json:"mode,omitempty"`
	AIModel    string `json:"ai_model,omitempty"`
	// Script は command が synthesize のときの台本です。
	Script *domain.Script `json:"script,omitempty"`
}

// createJobJSON は、JSON 本文から新しいジョブを投入します（POST /jobs）。
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

	// music_job_id は input_uri より優先します（動画生成サービスの compose_video と同じ）。
	// 両方来たときに黙って片方を捨てるより、ID を書いた側の意図を採ります。
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
		respond.ErrorJSON(w, r, http.StatusInternalServerError, "ジョブIDの発行に失敗しました")
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

	// 持ち込まれた台本は、投入の前に保存します。保存先はジョブ ID から決まるので、
	// 既存ジョブの差し替えと同じ経路です（ジョブが既にあるかどうかは問いません）。
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
			respond.ErrorJSON(w, r, http.StatusBadGateway, "台本の保存に失敗しました")
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

// JobCreate は、新しいジョブを投入します（POST /jobs）。
//
// 入口は 1 本で、本文の形で分かれます。JSON は機械（MCP サーバー）、フォームは
// 画面の 3 タブです。読み取りと応答の形だけが違い、採番・保存・投入は同じ経路です。
func (h *Handler) JobCreate(w http.ResponseWriter, r *http.Request) {
	if isJSONBody(r) {
		h.createJobJSON(w, r)
		return
	}
	h.createJobForm(w, r)
}

// isJSONBody は、本文が JSON かどうかを Content-Type で判定します。
func isJSONBody(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

// isFormBody は、本文が HTML フォームかどうかを Content-Type で判定します。
func isFormBody(r *http.Request) bool {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.Contains(ct, "application/x-www-form-urlencoded") || strings.Contains(ct, "multipart/form-data")
}

// acceptedAt は、投入とアクションの応答にポーリング先を載せます。
//
// 呼び出し側は本文を読まなくても次に叩く URL が分かります。画面向けの応答にも
// 同じヘッダを付けます（読み方が Accept で変わらないように）。
func acceptedAt(w http.ResponseWriter, jobID string) {
	w.Header().Set("Location", jobsBasePath+"/"+jobID)
}

// jobsBasePath はジョブのパスです。投入から削除まで 1 件をこの配下で指します。
const jobsBasePath = "/jobs"
