package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
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

// JobDelete は、ジョブと成果物をまとめて消します（DELETE /jobs/{jobID}）。
//
// 画面の削除ボタンも fetch で DELETE を送ります（app.js の App.deleteResource）。
func (h *Handler) JobDelete(w http.ResponseWriter, r *http.Request) {
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
	http.Redirect(w, r, jobsBasePath, http.StatusSeeOther)
}

// JobStatus は、ジョブの進行状況を返します。
//
// 投入した側が完了と失敗を知る唯一の手段です。成果物の有無だけでは、
// まだ動いているのか失敗したのかを区別できません。書式は gcp-kit の
// jobstatus.Status で、姉妹サービスと同じ形です。
//
// 記録が無い場合（ErrNotFound）は 404 です。MCP サーバー側はこれを unknown として扱い、
// 「状態機能より前のジョブ」や「投入直後」をツールの失敗にしません。
//
// 読めなかっただけの場合は 404 と混ぜません。権限や GCS 障害（ErrUnavailable）
// まで 404 にすると、障害の間すべてのジョブが「記録が無い」ように見え、
// ポーリング側が unknown として静かに受け入れてしまいます。
//
// 表現は 1 つ（JSON）です。GET /jobs/{jobID} が JSON を求められたときの中身で、
// Job から呼ばれます。
func (h *Handler) jobStatus(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.apiJobID(w, r)
	if !ok {
		return
	}

	status, err := h.repo.Get(r.Context(), jobID)
	switch {
	case errors.Is(err, jobstatus.ErrNotFound):
		respond.ErrorJSON(w, r, http.StatusNotFound, "ジョブ状態が見つかりません")
		return
	case err != nil:
		slog.ErrorContext(r.Context(), "ジョブ状態の取得に失敗しました", "job_id", jobID, "error", err)
		respond.ErrorJSON(w, r, http.StatusBadGateway, "ジョブ状態を読めませんでした")
		return
	}
	respond.JSON(w, r, http.StatusOK, status)
}

// Job は、ジョブ 1 件を返します（GET /jobs/{jobID}）。
//
// 投入した瞬間から削除するまで同じ URL で指します。JSON は進行状況（JobStatus）、
// HTML は台本を直す詳細画面です。呼び出し側は「今どちらを叩くべきか」を
// 状態で切り替えずに済みます。
func (h *Handler) Job(w http.ResponseWriter, r *http.Request) {
	if respond.WantsJSON(w, r) {
		h.jobStatus(w, r)
		return
	}
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}
	h.renderDetail(w, r, jobID, http.StatusOK, "", "")
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

// detailView は詳細画面に渡す値です。
type detailView struct {
	baseView
	JobID string
	// Script は保存済みの台本です。ここで内容を確認・修正してから音声を作ります。
	Script domain.Script
	// HasScript は台本が保存済みかです。false のときは編集フォームの代わりに
	// 状態を出します。台本が無いのは異常ではなく、生成の途中と生成に失敗した
	// ジョブの通常の姿で、そこで画面ごと開けなくなると削除する手段まで失われます。
	HasScript bool
	// State は記録された進行状態（queued / running / succeeded / failed）です。
	// 記録が読めなければ空になり、画面は状態の欄を出しません。
	State jobstatus.State
	// JobError は記録された失敗理由です。State が failed のときだけ入ります。
	// 画面の Error（この操作の失敗）とは別物なので名前を分けています。
	JobError string
	// HasAudio は音声が既にあるかです。無ければ「音声を作成」だけを出します。
	HasAudio bool
	// Speakers は話者名の一覧、StylesJSON は話者ごとの実在スタイルです。
	// 後者は選択肢を話者に応じて絞るために JS へ渡します。
	Speakers []string
	// InputURI は台本の元になった入力ソースです。記録にあるときだけ入り、
	// 「同じ入力で作り直す」を出すかどうかもこれで決まります。持ち込みの台本には
	// 入力ソースが無いので、押しても作り直せないボタンを出さずに済みます。
	InputURI string
	// MaxLines は 1 つの台本で受け付ける行数の上限です。画面へ渡すのは、
	// 行を足せる以上、画面側にも同じ上限が要るためです。JS 側に数を写すと、
	// どちらかを直したときにもう一方が古いままになります（超えた台本は保存時に
	// 弾かれ、そのとき画面は保存済みの台本を読み直すので編集中の内容が消えます）。
	MaxLines int
	// StylesJSON は data 属性へ入れるため素の string です。template.JS にすると
	// html/template が素通しし、値に </script> が入ったときブレイクアウトを許します。
	// 属性コンテキストなら確実にエスケープされ、画面側は dataset から読み戻せます。
	StylesJSON string
	Message    string
	Error      string
}

// maxScriptLines は 1 つの台本で受け付ける行数の上限です。
// フォームから任意の行数を組み立てられるため、上限が無いと 1 リクエストで
// 際限なく合成を積めます。プロンプトの目安（最大 80 発言）より広く取ります。
const maxScriptLines = 200

// Regenerate は、同じ入力ソースから台本を作り直します（POST /jobs/{jobID}/regenerate）。
//
// 失敗したジョブの行き先がこれです。台本を書く前に失敗したジョブには直すものが
// 無く、これまでは削除して投入フォームから URL を貼り直すしかありませんでした。
// 何を読ませたかは記録に残っているので、貼り直させる理由がありません。
//
// ジョブ ID は変えません。作り直しなので履歴に 2 件並べる意味がなく、同じ ID なら
// 成果物の置き場も記録もそのまま上書きされます。投入の記録が queued に戻るため、
// 再実行ガード（完了済みなら実行しない）にも掛かりません。
func (h *Handler) Regenerate(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}

	status, err := h.repo.Get(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, jobstatus.ErrNotFound) {
			h.renderDetail(w, r, jobID, http.StatusNotFound, "", "このジョブの記録が無いため、作り直せません。")
			return
		}
		h.renderDetail(w, r, jobID, http.StatusBadGateway, "", "ジョブ状態を読めませんでした")
		return
	}
	if status.InputURI == "" {
		// 台本を持ち込んだジョブ（台本 JSON タブ・API の script）には入力ソースが
		// ありません。作り直す先が無いので、ボタン自体も出していません。
		h.renderDetail(w, r, jobID, http.StatusBadRequest, "",
			"このジョブには入力ソースの記録が無いため、作り直せません。")
		return
	}

	req := domain.Request{
		Command:   domain.CommandGenerate,
		JobID:     jobID,
		InputURI:  status.InputURI,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      status.Mode,
		AIModel:   status.AIModel,
	}
	code, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderDetail(w, r, jobID, code, "", err.Error())
		return
	}

	acceptedAt(w, jobID)
	h.renderDetail(w, r, jobID, code, "同じ入力から台本を作り直しています。完了すると通知が届きます。", "")
}

// renderDetail は詳細画面を描画します。台本は毎回読み直します。
//
// 台本がまだ無くても描きます。この画面はジョブの入口でもあり、削除もここからしか
// できないため、生成に失敗したジョブで開けなくなると、履歴に残ったまま消せません。
func (h *Handler) renderDetail(w http.ResponseWriter, r *http.Request, jobID string, status int, message, errMsg string) {
	view := detailView{
		baseView: h.base(r),
		JobID:    jobID,
		Speakers: h.speakers.SpeakerNames(),
		MaxLines: maxScriptLines,
		Message:  message,
		Error:    errMsg,
	}
	view.State, view.JobError, view.InputURI = h.jobState(r, jobID)

	script, err := h.repo.Load(r.Context(), jobID)
	switch {
	case errors.Is(err, domain.ErrScriptNotFound):
		// 生成の途中か、生成に失敗したジョブです。状態だけの画面になります。
	case err != nil:
		http.Error(w, "台本の取得に失敗しました", http.StatusBadGateway)
		return
	default:
		view.Script, view.HasScript = script, true
	}

	hasAudio, err := h.repo.HasAudio(r.Context(), jobID)
	if err != nil {
		http.Error(w, "音声の有無を確認できませんでした", http.StatusBadGateway)
		return
	}
	view.HasAudio = hasAudio

	view.StylesJSON = h.stylesJSON

	h.renderTemplate(w, status, "detail.html", &view)
}

// jobState は、記録された進行状態・失敗理由・入力ソースを返します。
//
// 読めなくても画面は描きます。状態は成果物ではないので、記録が無い（状態機能より
// 前のジョブ）ことも、一時的に読めないこともあります。そこで画面ごと止めると、
// 台本を直すという本来の用が状態の都合で果たせなくなります。
func (h *Handler) jobState(r *http.Request, jobID string) (jobstatus.State, string, string) {
	status, err := h.repo.Get(r.Context(), jobID)
	if err != nil {
		if !errors.Is(err, jobstatus.ErrNotFound) {
			slog.WarnContext(r.Context(), "ジョブ状態を読めませんでした", "job_id", jobID, "error", err)
		}
		return "", "", ""
	}
	return status.State, status.Error, status.InputURI
}
