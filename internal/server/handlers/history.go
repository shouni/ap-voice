package handlers

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// historyPerPage は 1 ページに出す件数です。
const historyPerPage = 50

// pageParam は ?page= を読みます。不正な値は 1 ページ目として扱います。
// 一覧の閲覧でエラー画面を出しても、利用者にできることがありません。
func pageParam(r *http.Request) int {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// historyView は履歴一覧に渡す値です。
type historyView struct {
	baseView
	Jobs []repository.Job
	Page paging.PageMeta
}

// detailView は詳細画面に渡す値です。
type detailView struct {
	baseView
	JobID string
	// Script は保存済みの台本です。ここで内容を確認・修正してから音声を作ります。
	Script domain.Script
	// HasAudio は音声が既にあるかです。無ければ「音声を作成」だけを出します。
	HasAudio bool
	// Speakers は話者名の一覧、StylesJSON は話者ごとの実在スタイルです。
	// 後者は選択肢を話者に応じて絞るために JS へ渡します。
	Speakers   []string
	StylesJSON template.JS
	Message    string
	Error      string
}

// maxScriptLines は 1 つの台本で受け付ける行数の上限です。
// フォームから任意の行数を組み立てられるため、上限が無いと 1 リクエストで
// 際限なく合成を積めます。プロンプトの目安（最大 80 発言）より広く取ります。
const maxScriptLines = 200

// History は、これまでのジョブを新しい順に並べます。
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	jobs, meta, err := h.repo.List(r.Context(), pageParam(r), historyPerPage)
	if err != nil {
		http.Error(w, "履歴の取得に失敗しました", http.StatusBadGateway)
		return
	}

	h.renderTemplate(w, http.StatusOK, "history.html", historyView{baseView: h.base(r), Jobs: jobs, Page: meta})
}

// Detail は、1 件のジョブの台本を表示します。ここから音声の確認と作成を行います。
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	h.renderDetail(w, r, http.StatusOK, "", "")
}

// UpdateScript は、編集された台本を保存し、続けて音声の作成を指示します。
//
// **台本はタスクに載せません。** 先に保存してから JobID だけを渡すことで、
// Cloud Tasks の 1MB 上限に台本の長さが当たらず、Worker 側も
// 「保存済み台本を読む」既存の経路をそのまま使えます。
func (h *Handler) UpdateScript(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		http.Error(w, "不正なジョブIDです", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderDetail(w, r, http.StatusBadRequest, "", "フォームの解析に失敗しました")
		return
	}

	script, err := h.scriptFromForm(r)
	if err != nil {
		h.renderDetail(w, r, http.StatusBadRequest, "", err.Error())
		return
	}

	if err := h.repo.SaveScript(r.Context(), jobID, script); err != nil {
		h.renderDetail(w, r, http.StatusBadGateway, "", "台本の保存に失敗しました")
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	h.recordQueued(r.Context(), req)
	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		h.renderDetail(w, r, http.StatusBadGateway, "", err.Error())
		return
	}

	h.renderDetail(w, r, http.StatusAccepted, "台本を保存し、音声の作成を受け付けました。完了すると通知が届きます。", "")
}

// scriptFromForm は、送られてきた行をドメインの台本へ組み立てます。
//
// 組み立てるだけで、実在するかの確認は validateScript が行います。
// **API と同じ関数を通す**ため、どちらか一方だけが緩くなることがありません。
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

// Delete は、1 つのジョブの成果物をまとめて消します。
//
// 消す側が中身を知らずに済むよう、プレフィックス配下をまとめて対象にします。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		http.Error(w, "不正なジョブIDです", http.StatusBadRequest)
		return
	}

	if err := h.repo.Delete(r.Context(), jobID); err != nil {
		http.Error(w, "削除に失敗しました", http.StatusBadGateway)
		return
	}

	// 消した先の詳細は開けないため、一覧へ戻します。
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}

// Audio は、音声の署名付き URL へ転送します。
//
// **バイト列はアプリが配信しません。** 音声は数十 MB になりうるため、
// Cloud Run のインスタンスを転送で占有させる理由がありません。
func (h *Handler) Audio(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		http.Error(w, "不正なジョブIDです", http.StatusBadRequest)
		return
	}

	url, err := h.signer.GenerateSignedURL(r.Context(), h.layout.AudioURI(h.bucket, jobID), http.MethodGet, signedURLExpiry)
	if err != nil {
		http.Error(w, "音声のURL生成に失敗しました", http.StatusBadGateway)
		return
	}

	http.Redirect(w, r, url, http.StatusFound)
}

// renderDetail は詳細画面を描画します。台本は毎回読み直します。
func (h *Handler) renderDetail(w http.ResponseWriter, r *http.Request, status int, message, errMsg string) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		http.Error(w, "不正なジョブIDです", http.StatusBadRequest)
		return
	}

	script, err := h.repo.Load(r.Context(), jobID)
	if err != nil {
		http.Error(w, "台本の取得に失敗しました", http.StatusBadGateway)
		return
	}

	hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
	if err != nil {
		http.Error(w, "音声の有無を確認できませんでした", http.StatusBadGateway)
		return
	}

	stylesJSON, err := json.Marshal(h.stylesBySpeaker())
	if err != nil {
		http.Error(w, "話者一覧の組み立てに失敗しました", http.StatusInternalServerError)
		return
	}

	h.renderTemplate(w, status, "detail.html", detailView{
		baseView:   h.base(r),
		JobID:      jobID,
		Script:     script,
		HasAudio:   hasAudio,
		Speakers:   h.speakers.SpeakerNames(),
		StylesJSON: template.JS(stylesJSON),
		Message:    message,
		Error:      errMsg,
	})
}
