package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/internal/domain"
)

// このファイルは、画面に対応するページが無く、機械だけが使う操作です
// （JSON で投入する、台本を差し替える、合成を指示する）。人と機械の両方が使うものは
// /api の外にあります — ジョブの状態は negotiated.go、読みの確認は reading.go です。
//
// 画面と同じミドルウェアの下にあります。ProtectedMiddleware が OIDC の
// Bearer とセッションの両方を通すため、同じ URL を人も機械も叩けます。
//
// 台本の検証は画面と同じ validateScript を通します。別に書くと、
// どちらか一方だけが実在しない話者を受け付けるようになります。

// apiTimeFormat は、一覧が返す時刻の書式です。
const apiTimeFormat = "2006-01-02T15:04:05Z07:00"

// apiJob は一覧の 1 件です。
type apiJob struct {
	JobID     string `json:"job_id"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	HasAudio  bool   `json:"has_audio"`
	// State は進行状態です。成果物の有無だけでは、実行中なのか失敗したのかを
	// 区別できません。1 件ずつ status を引かずに一覧で見分けるために載せます。
	// 記録の無い古いジョブでは空になるので omitempty です。
	State string `json:"state,omitempty"`
}

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

// apiJobPage は、ページ付きの一覧応答です。
//
// メタデータの形は gcp-kit の PageMeta です。JSON タグは姉妹サービスと同じ
// JSON なので、呼び出し側はサービスごとに読み方を変えずに済みます。
type apiJobPage struct {
	Jobs []apiJob           `json:"jobs"`
	Page jobstatus.PageMeta `json:"page"`
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

	respond.JSON(w, r, status, apiAccepted{Status: string(jobstatus.StateQueued), JobID: jobID, Command: string(command)})
}

// APIUpdateScript は、台本を差し替えます。合成はしません。
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

	// 画面と同じ検証です（validateScript）。
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
	status, err := h.submit(r.Context(), req)
	if err != nil {
		respond.ErrorJSON(w, r, status, err.Error())
		return
	}
	respond.JSON(w, r, status, apiAccepted{Status: string(jobstatus.StateQueued), JobID: jobID, Command: string(domain.CommandSynthesize)})
}

// apiAudio は GET /api/jobs/{jobID}/audio の応答です。
type apiAudio struct {
	JobID string `json:"job_id"`
	// AudioURI は保存先です。期限が無いので、記録や再取得の手掛かりになります。
	AudioURI string `json:"audio_uri"`
	// SignedURL は誰でも再生・取得できるリンクです。期限があります。
	SignedURL string `json:"signed_url"`
	// ExpiresInSeconds は SignedURL の有効期間です。
	ExpiresInSeconds int `json:"expires_in_seconds"`
}

// apiJobID は、URL のジョブ ID を取り出して検証します。
//
// 検証そのものは jobIDParam です。ここが持つのは返し方だけで、JSON に固定するのは
// この経路が成功時も無条件に JSON を返すためです（Accept を送らない呼び出し側が、
// 成功と失敗で本文の読み方を変えずに済みます）。
func (h *Handler) apiJobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	jobID, ok := jobIDParam(r)
	if !ok {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "不正なジョブIDです")
		return "", false
	}
	return jobID, true
}

// validateScript は、台本の各行が実在する話者・スタイルであることを確かめ、
// 本文が空の行を落とした台本を返します。
//
// フォームと API の共通の入口です。片方だけに書くと、もう片方から
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

// stylesBySpeaker は、話者名からその話者が持つスタイル名への対応を組み立てます。
//
// 呼ぶのは NewHandler だけです。話者一覧は起動から変わらないので、編集画面と
// /api/speakers はその結果（h.styles / h.stylesJSON）を使い回します。
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
