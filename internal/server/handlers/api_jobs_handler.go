package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"

	"github.com/shouni/go-serve-kit/respond"
)

// API は、ブラウザではなく機械（ap-mcp など）から使う口です。
//
// **画面と同じミドルウェアの下にあります。** ProtectedMiddleware が OIDC の
// Bearer とセッションの両方を通すため、同じ URL を人も機械も叩けます。
//
// 台本の検証は画面と同じ scriptFromLines を通します。別に書くと、
// どちらか一方だけが実在しない話者を受け付けるようになります。

// apiTimeFormat は、一覧が返す時刻の書式です。
const apiTimeFormat = "2006-01-02T15:04:05Z07:00"

// apiJob は一覧の 1 件です。
type apiJob struct {
	JobID     string `json:"job_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	HasAudio  bool   `json:"has_audio"`
}

// apiAccepted は投入を受け付けたときの応答です。
//
// **御三家と同じ封筒**です（ap-mcp の client.TaskQueuedResponse が
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
// **入口は 2 つあります。** 入力ソースから AI に書かせるか（generate 系）、
// 既に手元にある台本をそのまま渡すか（synthesize）です。後者は呼び出し側が
// 自分で書いた台本を喋らせる経路で、Gemini を呼びません。
type apiEnqueue struct {
	Command  string `json:"command"`
	InputURI string `json:"input_uri,omitempty"`
	// MusicJobID は、入力が ap-comp の楽曲レシピのときのジョブ ID です。
	//
	// **解決はここでやります。** 呼び出し側に gs:// を組み立てさせると、
	// 置き場の規則がサービスの外へ漏れ、変えるときに全員へ知らせて回ることに
	// なります。画面の「楽曲レシピ」タブと同じ関数を通ります。
	MusicJobID string `json:"music_job_id,omitempty"`
	Mode       string `json:"mode,omitempty"`
	AIModel    string `json:"ai_model,omitempty"`
	// Script は command が synthesize のときの台本です。
	Script *domain.Script `json:"script,omitempty"`
}

// apiJobPage は、ページ付きの一覧応答です。
//
// **メタデータの形は go-job-kit の paging.PageMeta です。** 御三家の一覧と同じ
// JSON なので、呼び出し側はサービスごとに読み方を変えずに済みます。
type apiJobPage struct {
	Jobs []apiJob        `json:"jobs"`
	Page paging.PageMeta `json:"page"`
}

// APIEnqueue は、入力ソースから新しいジョブを投入します。
func (h *Handler) APIEnqueue(w http.ResponseWriter, r *http.Request) {
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

	// music_job_id は input_uri より優先します（ap-mv の compose_video と同じ）。
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

	// **持ち込まれた台本は、投入の前に保存します。** 保存先はジョブ ID から決まるので、
	// 既存ジョブの差し替えと同じ経路です（ジョブが既にあるかどうかは問いません）。
	// タスクには載せないため、長い台本でも Cloud Tasks の 1MB 上限に当たりません。
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

	if err := req.Validate(); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, err.Error())
		return
	}
	h.recordQueued(r.Context(), req)
	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadGateway, err.Error())
		return
	}

	respond.JSON(w, r, http.StatusAccepted, apiAccepted{Status: string(jobfirestore.StateQueued), JobID: jobID, Command: string(command)})
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
		respond.ErrorJSON(w, r, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	// **画面と同じ検証です。** 実在しない話者・スタイルは、合成時に既定へ黙って
	// 落ちて指示が消えるため、保存する前に弾きます。
	cleaned, err := h.validateScript(script)
	if err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.repo.SaveScript(r.Context(), jobID, cleaned); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadGateway, "台本の保存に失敗しました")
		return
	}
	respond.JSON(w, r, http.StatusOK, cleaned)
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
		respond.ErrorJSON(w, r, http.StatusBadRequest, err.Error())
		return
	}
	h.recordQueued(r.Context(), req)
	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadGateway, err.Error())
		return
	}
	respond.JSON(w, r, http.StatusAccepted, apiAccepted{Status: string(jobfirestore.StateQueued), JobID: jobID, Command: string(domain.CommandSynthesize)})
}

// APIJobStatus は、ジョブの進行状況を返します。
//
// **投入した側が完了と失敗を知る唯一の手段です。** 成果物の有無だけでは、
// まだ動いているのか失敗したのかを区別できません。書式は go-job-kit の
// jobfirestore.Status で、御三家と同じ形です。
//
// 記録が無い場合（ErrNotFound）は 404 です。ap-mcp 側はこれを unknown として扱い、
// 「状態機能より前のジョブ」や「投入直後」をツールの失敗にしません。
//
// **読めなかっただけの場合は 404 と混ぜません。** 権限や GCS 障害（ErrUnavailable）
// まで 404 にすると、障害の間すべてのジョブが「記録が無い」ように見え、
// ポーリング側が unknown として静かに受け入れてしまいます。
func (h *Handler) APIJobStatus(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	status, err := h.repo.Get(r.Context(), jobID)
	switch {
	case errors.Is(err, jobfirestore.ErrNotFound):
		respond.ErrorJSON(w, r, http.StatusNotFound, "ジョブ状態が見つかりません")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "ジョブ状態の取得に失敗しました", "job_id", jobID, "error", err)
		respond.ErrorJSON(w, r, http.StatusBadGateway, "ジョブ状態を読めませんでした")
		return
	}
	respond.JSON(w, r, http.StatusOK, status)
}

// apiReadingRequest は POST /api/preview-reading の要求です。
type apiReadingRequest struct {
	// Lines は確かめたい行です。台本をそのまま渡せる形にしてあります。
	Lines []domain.ScriptLine `json:"lines"`
}

// apiReadingLine は 1 行分の読みです。
type apiReadingLine struct {
	Text string `json:"text"`
	// Reading は合成時に実際に読まれるカタカナです。
	Reading string `json:"reading"`
	// Changed は、変換で表記が変わったかどうかです。**確かめる価値がある行の目印**で、
	// カタカナだけの行は変換しても変わらないため false になります。
	Changed bool `json:"changed"`
}

// apiReadingResponse は POST /api/preview-reading の応答です。
type apiReadingResponse struct {
	Lines []apiReadingLine `json:"lines"`
}

// APIPreviewReading は、合成したらどう読まれるかを行ごとに返します。**合成はしません。**
//
// **読みは自明ではありません。**「田中」「同姓同名」のような語がどう読まれるかは、
// 合成して聴くまで分かりませんでした。台本の長さぶんの合成時間を使ってから
// 気付くことになるため、その前に確かめられるようにします。
//
// 意図と違う読みになる語は、その部分をカタカナで書けば直せます。
func (h *Handler) APIPreviewReading(w http.ResponseWriter, r *http.Request) {
	var body apiReadingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}
	if len(body.Lines) == 0 {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "lines が空です")
		return
	}
	if len(body.Lines) > maxScriptLines {
		respond.ErrorJSON(w, r, http.StatusBadRequest,
			fmt.Sprintf("行が多すぎます（%d 行、上限 %d 行）", len(body.Lines), maxScriptLines))
		return
	}

	out := make([]apiReadingLine, 0, len(body.Lines))
	for _, line := range body.Lines {
		reading, err := h.reading.ConvertToReading(line.Text)
		if err != nil {
			respond.ErrorJSON(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, apiReadingLine{
			Text: line.Text, Reading: reading, Changed: reading != line.Text,
		})
	}
	respond.JSON(w, r, http.StatusOK, apiReadingResponse{Lines: out})
}

// apiAudio は GET /api/jobs/{jobID}/audio の応答です。
type apiAudio struct {
	JobID string `json:"job_id"`
	// AudioURI は保存先です。期限が無いので、記録や再取得の手掛かりになります。
	AudioURI string `json:"audio_uri"`
	// SignedURL は誰でも再生・取得できるリンクです。**期限があります。**
	SignedURL string `json:"signed_url"`
	// ExpiresInSeconds は SignedURL の有効期間です。
	ExpiresInSeconds int `json:"expires_in_seconds"`
}

// apiJobID は、URL のジョブ ID を取り出して検証します。
func (h *Handler) apiJobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "不正なジョブIDです")
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
