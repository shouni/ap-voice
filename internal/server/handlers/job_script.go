package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-voicevox/speaker"
)

// Script は、保存済みの台本を返します。
//
// どちらの読者にも JSON を返しますが、人にはファイルとして保存させます。
// 画面から開いたときにブラウザが本文を表示してしまうと、確認はできても
// 手元に残せません。
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
		// 読めなかっただけの場合を 404 に混ぜません。混ぜると、GCS の障害中は
		// すべてのジョブが「台本が無い」ように見え、呼び出し側が静かに受け入れます。
		slog.ErrorContext(r.Context(), "台本の取得に失敗しました", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusBadGateway, "台本を読めませんでした")
		return
	}

	if !respond.WantsJSON(w, r) {
		// jobid.Validate を通った ID だけがここに来るため、ファイル名に使えます。
		w.Header().Set("Content-Disposition", `attachment; filename="`+jobID+`.json"`)
	}
	respond.JSON(w, r, http.StatusOK, script)
}

// ScriptUpdate は、台本を差し替えます（PUT /jobs/{jobID}/script）。合成はしません。
//
// 保存と合成を分けているのは、クライアントが何度か直してから 1 度だけ
// 合成できるようにするためです。画面はその 2 つを 1 つのボタンにまとめていますが、
// 機械には分かれている方が使いやすくなります。
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

// synthesizeSaved は、保存済みの台本から音声を作ります（POST /jobs/{jobID}/synthesize を JSON で呼んだとき）。
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
// /speakers はその結果（h.styles / h.stylesJSON）を使い回します。
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

// Synthesize は、台本から音声を作ります（POST /jobs/{jobID}/synthesize）。
//
// フォームは編集中の台本を本文に載せてくるので、保存してから合成します。JSON
// （本文なし）は保存済みの台本をそのまま合成します。機械は PUT /jobs/{jobID}/script で
// 何度か直してから 1 度だけここを呼び、画面は 1 つのボタンで両方を済ませます。
func (h *Handler) Synthesize(w http.ResponseWriter, r *http.Request) {
	if isFormBody(r) {
		h.synthesizeFromForm(w, r)
		return
	}
	h.synthesizeSaved(w, r)
}

// synthesizeFromForm は、編集された台本を保存し、続けて音声の作成を指示します
// （POST /jobs/{jobID}/synthesize をフォームから呼んだとき）。
//
// 保存が先で、タスクへ渡すのはジョブ ID だけです（理由は domain.Request.JobID）。
// Worker 側は「保存済み台本を読む」既存の経路をそのまま使います。
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

// scriptFromForm は、送られてきた行をドメインの台本へ組み立てます。
//
// 組み立てるだけで、実在するかの確認は validateScript が行います。
// API と同じ関数を通すため、どちらか一方だけが緩くなることがありません。
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
