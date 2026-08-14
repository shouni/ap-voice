package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
)

// API は、ブラウザではなく機械（ap-mcp など）から使う口です。
//
// **画面と同じミドルウェアの下にあります。** ProtectedMiddleware が OIDC の
// Bearer とセッションの両方を通すため、同じ URL を人も機械も叩けます。
//
// 台本の検証は画面と同じ scriptFromLines を通します。別に書くと、
// どちらか一方だけが実在しない話者を受け付けるようになります。

// apiJob は一覧の 1 件です。
type apiJob struct {
	JobID     string `json:"job_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	HasAudio  bool   `json:"has_audio"`
}

// apiAccepted は投入を受け付けたときの応答です。
type apiAccepted struct {
	JobID   string `json:"job_id"`
	Command string `json:"command"`
}

// apiEnqueue は、ジョブ投入の要求です。
type apiEnqueue struct {
	// Command は generate か generate_and_synthesize です。
	// synthesize は台本が要るため、既存ジョブへの POST 側で受けます。
	Command  string `json:"command"`
	InputURI string `json:"input_uri"`
	Mode     string `json:"mode"`
	AIModel  string `json:"ai_model,omitempty"`
}

// APIJobs は、ジョブを新しい順に返します。
func (h *Handler) APIJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.repo.List(r.Context(), historyLimit)
	if err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "履歴の取得に失敗しました")
		return
	}

	out := make([]apiJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, apiJob{
			JobID: job.ID, Title: job.Title,
			CreatedAt: job.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			HasAudio:  job.HasAudio,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// APIEnqueue は、入力ソースから新しいジョブを投入します。
func (h *Handler) APIEnqueue(w http.ResponseWriter, r *http.Request) {
	var body apiEnqueue
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	command := domain.Command(body.Command)
	if command != domain.CommandGenerate && command != domain.CommandGenerateAndSynthesize {
		writeErrorJSON(w, http.StatusBadRequest, fmt.Sprintf("command は %q か %q です",
			domain.CommandGenerate, domain.CommandGenerateAndSynthesize))
		return
	}

	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "ジョブIDの発行に失敗しました")
		return
	}

	req := domain.Request{
		Command:   command,
		JobID:     jobID,
		InputURI:  body.InputURI,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      body.Mode,
		AIModel:   body.AIModel,
	}
	if err := req.Validate(); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		writeErrorJSON(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, apiAccepted{JobID: jobID, Command: string(command)})
}

// APIScript は、保存済みの台本を返します。
func (h *Handler) APIScript(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	script, err := h.repo.Load(r.Context(), jobID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "台本が見つかりません")
		return
	}
	writeJSON(w, http.StatusOK, script)
}

// APIUpdateScript は、台本を差し替えます。**合成はしません。**
//
// 保存と合成を分けているのは、クライアントが何度か直してから 1 度だけ
// 合成できるようにするためです。画面はその 2 つを 1 つのボタンにまとめていますが、
// 機械には分かれている方が使いやすくなります。
func (h *Handler) APIUpdateScript(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	var script domain.Script
	if err := json.NewDecoder(r.Body).Decode(&script); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	// **画面と同じ検証です。** 実在しない話者・スタイルは、合成時に既定へ黙って
	// 落ちて指示が消えるため、保存する前に弾きます。
	cleaned, err := h.validateScript(script)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.Save(r.Context(), jobID, cleaned); err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "台本の保存に失敗しました")
		return
	}
	writeJSON(w, http.StatusOK, cleaned)
}

// APISynthesize は、保存済みの台本から音声を作ります。
func (h *Handler) APISynthesize(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	if err := req.Validate(); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		writeErrorJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, apiAccepted{JobID: jobID, Command: string(domain.CommandSynthesize)})
}

// APIDeleteJob は、1 つのジョブの成果物をまとめて消します。
func (h *Handler) APIDeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), jobID); err != nil {
		writeErrorJSON(w, http.StatusBadGateway, "削除に失敗しました")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiJobID は、URL のジョブ ID を取り出して検証します。
func (h *Handler) apiJobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "不正なジョブIDです")
		return "", false
	}
	return jobID, true
}

// validateScript は、台本の各行が実在する話者・スタイルであることを確かめ、
// 本文が空の行を落とした台本を返します。
//
// **フォームと API の共通の入口です。** 片方だけに書くと、もう片方から
// 実在しない組み合わせが保存できてしまいます。
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
			// 空の行は落とします。行を消す手段でもあります。
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

// stylesBySpeaker は、話者名からその話者が持つスタイル名への対応を返します。
// 編集画面の選択肢と /api/speakers の両方がここを見ます。
func (h *Handler) stylesBySpeaker() map[string][]string {
	names := h.speakers.SpeakerNames()
	out := make(map[string][]string, len(names))
	for _, name := range names {
		if styles, ok := h.speakers.StylesFor(name); ok {
			out[name] = styles
		}
	}
	return out
}
