package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// historyLimit は一覧に出す件数です。
const historyLimit = 50

// historyView は履歴一覧に渡す値です。
type historyView struct {
	Jobs []repository.Job
}

// detailView は詳細画面に渡す値です。
type detailView struct {
	CSRFToken string
	JobID     string
	// Script は保存済みの台本です。ここで内容を確認してから音声を作ります。
	Script []domain.ScriptLine
	// HasAudio は音声が既にあるかです。無ければ「音声を作成」だけを出します。
	HasAudio bool
	Message  string
	Error    string
}

// History は、これまでのジョブを新しい順に並べます。
func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.repo.List(r.Context(), historyLimit)
	if err != nil {
		http.Error(w, "履歴の取得に失敗しました", http.StatusBadGateway)
		return
	}

	h.renderTemplate(w, http.StatusOK, "history.html", historyView{Jobs: jobs})
}

// Detail は、1 件のジョブの台本を表示します。ここから音声の作成を指示します。
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	h.renderDetail(w, r, http.StatusOK, "", "")
}

// Synthesize は、保存済みの台本から音声を作るよう指示します。
//
// 台本そのものは載せません。Cloud Tasks のペイロードは 1MB が上限で、長い台本は
// そこに当たりうるためです。Worker 側が JobID で読み出します。
func (h *Handler) Synthesize(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	if err := jobid.Validate(jobID); err != nil {
		http.Error(w, "不正なジョブIDです", http.StatusBadRequest)
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	if err := req.Validate(); err != nil {
		h.renderDetail(w, r, http.StatusBadRequest, "", err.Error())
		return
	}

	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		h.renderDetail(w, r, http.StatusBadGateway, "", err.Error())
		return
	}

	h.renderDetail(w, r, http.StatusAccepted, "音声の作成を受け付けました。完了すると通知が届きます。", "")
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

	jobs, err := h.repo.List(r.Context(), historyLimit)
	if err != nil {
		http.Error(w, "履歴の取得に失敗しました", http.StatusBadGateway)
		return
	}
	hasAudio := false
	for _, job := range jobs {
		if job.ID == jobID {
			hasAudio = job.HasAudio
			break
		}
	}

	h.renderTemplate(w, status, "detail.html", detailView{
		CSRFToken: csrfToken(r),
		JobID:     jobID,
		Script:    script,
		HasAudio:  hasAudio,
		Message:   message,
		Error:     errMsg,
	})
}
