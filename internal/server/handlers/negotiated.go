package handlers

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/gcp-kit/negotiate"
	"github.com/shouni/go-utils/jobid"
)

// このファイルは、人と機械が同じリソースを見る経路をまとめています。
//
// 表現だけが違うものに 2 つのハンドラーを持たせると、片方だけ直したときに
// 画面の表示と機械可読な結果が食い違います。取得と検証は 1 度だけ行い、
// 最後に Accept を見て HTML か JSON かを決めます。
//
// 逆に、片方の読者にしか無い操作（入力フォーム、合成の指示など）は
// 別のリソースなので、このファイルには置きません。

// jobID は URL のジョブ ID を取り出し、要求された表現でエラーを返します。
func (h *Handler) jobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "jobID")
	if err := jobid.Validate(id); err != nil {
		negotiate.Error(w, r, http.StatusBadRequest, "不正なジョブIDです")
		return "", false
	}
	return id, true
}

// Jobs は、ジョブを新しい順に返します。?page= と ?per_page= を受けます。
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	perPage := historyPerPage
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		perPage = n
	}

	jobs, meta, err := h.repo.List(r.Context(), pageParam(r), perPage)
	if err != nil {
		negotiate.Error(w, r, http.StatusBadGateway, "履歴の取得に失敗しました")
		return
	}

	if negotiate.WantsJSON(w, r) {
		out := make([]apiJob, 0, len(jobs))
		for _, job := range jobs {
			out = append(out, apiJob{
				JobID: job.ID, Title: job.Title,
				CreatedAt: job.CreatedAt.Format(apiTimeFormat),
				HasAudio:  job.HasAudio,
			})
		}
		negotiate.JSON(w, r, http.StatusOK, apiJobPage{Jobs: out, Page: meta})
		return
	}
	h.renderTemplate(w, http.StatusOK, "history.html", historyView{baseView: h.base(r), Jobs: jobs, Page: meta})
}

// Modes は、選べる指示モードを返します。
func (h *Handler) Modes(w http.ResponseWriter, r *http.Request) {
	if negotiate.WantsJSON(w, r) {
		modes := make([]apiMode, 0, len(h.modes))
		for _, mode := range h.modes {
			modes = append(modes, apiMode{
				Key: mode.Key, Label: mode.DisplayName(),
				Direction: mode.Direction, UseWhen: mode.UseWhen,
			})
		}
		negotiate.JSON(w, r, http.StatusOK, modes)
		return
	}
	h.renderTemplate(w, http.StatusOK, "modes.html", modesView{baseView: h.base(r), Modes: h.modes})
}

// Audio は、生成済み音声の署名付き URL を返します。
//
// 人にはその URL へ転送し、機械には URL そのものを返します。転送で返すと、
// 呼び出し側は URL を受け取るために転送を追わない設定を書く必要があります。
func (h *Handler) Audio(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	wantsJSON := negotiate.WantsJSON(w, r)
	if wantsJSON {
		// 画面はリンクを出す前に一覧で有無を知っていますが、機械は知りません。
		// 無い場合に署名付き URL を返すと、開いた先で 404 に当たります。
		hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
		if err != nil {
			negotiate.Error(w, r, http.StatusBadGateway, "音声の有無を確認できませんでした")
			return
		}
		if !hasAudio {
			negotiate.Error(w, r, http.StatusNotFound, "このジョブにはまだ音声がありません")
			return
		}
	}

	url, err := h.signer.GenerateSignedURL(r.Context(), h.layout.AudioURI(h.bucket, jobID), http.MethodGet, signedURLExpiry)
	if err != nil {
		negotiate.Error(w, r, http.StatusBadGateway, "音声のURL生成に失敗しました")
		return
	}

	if wantsJSON {
		negotiate.JSON(w, r, http.StatusOK, apiAudio{
			JobID:            jobID,
			AudioURI:         h.layout.AudioURI(h.bucket, jobID),
			SignedURL:        url,
			ExpiresInSeconds: int(signedURLExpiry.Seconds()),
		})
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

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
	if err != nil {
		negotiate.Error(w, r, http.StatusNotFound, "台本が見つかりません")
		return
	}

	if !negotiate.WantsJSON(w, r) {
		// jobid.Validate を通った ID だけがここに来るため、ファイル名に使えます。
		w.Header().Set("Content-Disposition", `attachment; filename="`+jobID+`.json"`)
	}
	negotiate.JSON(w, r, http.StatusOK, script)
}

// Delete は、ジョブと成果物をまとめて消します。
//
// 人はフォームから POST し、機械は DELETE を送ります。HTML のフォームは
// DELETE を出せないため、メソッドは 2 つのままです。
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	if err := h.repo.Delete(r.Context(), jobID); err != nil {
		negotiate.Error(w, r, http.StatusBadGateway, "削除に失敗しました")
		return
	}

	if negotiate.WantsJSON(w, r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// 消した先の詳細は開けないため、一覧へ戻します。
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}
