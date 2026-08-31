package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/repository"
)

// このファイルは、人と機械が同じリソースを見る経路をまとめています。
//
// 表現だけが違うものに 2 つのハンドラーを持たせると、片方だけ直したときに
// 画面の表示と機械可読な結果が食い違います。取得と検証は 1 度だけ行い、
// 最後に Accept を見て HTML か JSON かを決めます。
//
// 逆に、片方の読者にしか無い操作（入力フォーム、合成の指示など）は
// 別のリソースなので、このファイルには置きません。

// jobIDParam は URL のジョブ ID を取り出して検証します。
//
// 応答は返しません。同じ検証でも返し方が 3 通りあるためです（Accept で選ぶ画面、
// JSON 固定の API、素のテキスト）。値の取り出しと検証だけをここに集め、
// どう返すかは呼び出し側が決めます。ID はそのままオブジェクトのパスに入るので、
// 検証を通っていない値を先へ渡せません。
func jobIDParam(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "jobID")
	if err := jobid.Validate(id); err != nil {
		return "", false
	}
	return id, true
}

// jobID は URL のジョブ ID を取り出し、要求された表現でエラーを返します。
func (h *Handler) jobID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := jobIDParam(r)
	if !ok {
		respond.Error(w, r, http.StatusBadRequest, "不正なジョブIDです")
		return "", false
	}
	return id, true
}

// Jobs は、ジョブを新しい順に返します。?page= と ?per_page= を受けます。
func (h *Handler) Jobs(w http.ResponseWriter, r *http.Request) {
	perPage := historyPerPage
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		// 上限で頭打ちにします。何でも通していた頃は ?per_page=100000 が
		// そのまま 1 クエリになり、呼び出し側の打ち間違いを倉庫が引き受けていました。
		perPage = min(n, maxPerPage)
	}

	state, ok := stateParam(r)
	if !ok {
		respond.Error(w, r, http.StatusBadRequest, "state は "+strings.Join(listableStates(), " / ")+" です")
		return
	}

	var opts []jobfirestore.ListOption
	if state != "" {
		opts = append(opts, jobfirestore.WithState(state))
	}

	jobs, meta, err := h.repo.List(r.Context(), pageParam(r), perPage, opts...)
	if err != nil {
		// 絞り込みは複合索引を要ります（state と queued_at）。索引はデプロイ設定が
		// 持つので、無い環境では絞り込んだときだけここに来ます。
		slog.ErrorContext(r.Context(), "履歴の取得に失敗しました", "state", state, "error", err)
		respond.Error(w, r, http.StatusBadGateway, "履歴の取得に失敗しました")
		return
	}

	if respond.WantsJSON(w, r) {
		out := make([]apiJob, 0, len(jobs))
		for _, job := range jobs {
			out = append(out, apiJob{
				JobID: job.ID, Title: job.Title,
				CreatedAt: job.CreatedAt.Format(apiTimeFormat),
				HasAudio:  job.HasAudio,
				State:     string(job.State),
			})
		}
		respond.JSON(w, r, http.StatusOK, apiJobPage{Jobs: out, Page: meta})
		return
	}
	h.renderTemplate(w, http.StatusOK, "history.html", &historyView{
		baseView: h.base(r), Jobs: jobs, Page: meta, Filter: string(state),
	})
}

// Modes は、選べる指示モードを返します。
func (h *Handler) Modes(w http.ResponseWriter, r *http.Request) {
	if respond.WantsJSON(w, r) {
		modes := make([]apiMode, 0, len(h.modes))
		for _, mode := range h.modes {
			modes = append(modes, apiMode{
				Key: mode.Key, Label: mode.DisplayName(),
				Direction: mode.Direction, UseWhen: mode.UseWhen,
			})
		}
		respond.JSON(w, r, http.StatusOK, modes)
		return
	}
	h.renderTemplate(w, http.StatusOK, "modes.html", &modesView{baseView: h.base(r), Modes: h.modes})
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

	wantsJSON := respond.WantsJSON(w, r)
	if wantsJSON {
		// 画面はリンクを出す前に一覧で有無を知っていますが、機械は知りません。
		// 無い場合に署名付き URL を返すと、開いた先で 404 に当たります。
		hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
		if err != nil {
			respond.Error(w, r, http.StatusBadGateway, "音声の有無を確認できませんでした")
			return
		}
		if !hasAudio {
			respond.Error(w, r, http.StatusNotFound, "このジョブにはまだ音声がありません")
			return
		}
	}

	url, err := h.signer.SignURL(r.Context(), h.layout.AudioURI(h.bucket, jobID), http.MethodGet, signedURLExpiry)
	if err != nil {
		respond.Error(w, r, http.StatusBadGateway, "音声のURL生成に失敗しました")
		return
	}

	if wantsJSON {
		respond.JSON(w, r, http.StatusOK, apiAudio{
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
	switch {
	case errors.Is(err, repository.ErrScriptNotFound):
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
		if errors.Is(err, repository.ErrJobNotFound) {
			respond.Error(w, r, http.StatusNotFound, "ジョブが見つかりません")
			return
		}
		slog.ErrorContext(r.Context(), "ジョブの削除に失敗しました", "job_id", jobID, "error", err)
		respond.Error(w, r, http.StatusBadGateway, "削除に失敗しました")
		return
	}

	if respond.WantsJSON(w, r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// 消した先の詳細は開けないため、一覧へ戻します。
	http.Redirect(w, r, "/history", http.StatusSeeOther)
}

// JobStatus は、ジョブの進行状況を返します。
//
// 投入した側が完了と失敗を知る唯一の手段です。成果物の有無だけでは、
// まだ動いているのか失敗したのかを区別できません。書式は go-job-firestore の
// jobfirestore.Status で、姉妹サービスと同じ形です。
//
// 記録が無い場合（ErrNotFound）は 404 です。MCP サーバー側はこれを unknown として扱い、
// 「状態機能より前のジョブ」や「投入直後」をツールの失敗にしません。
//
// 読めなかっただけの場合は 404 と混ぜません。権限や GCS 障害（ErrUnavailable）
// まで 404 にすると、障害の間すべてのジョブが「記録が無い」ように見え、
// ポーリング側が unknown として静かに受け入れてしまいます。
//
// 表現は 1 つ（JSON）です。読者で分かれないので Accept は見ませんが、詳細画面の
// 自動更新と MCP のポーリングが同じものを読む以上、置き場は /api ではありません。
func (h *Handler) JobStatus(w http.ResponseWriter, r *http.Request) {
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
