package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// historyPerPage は 1 ページに出す件数です。
//
// 一覧は成果物を読まないので、増やしても効くのは記録の読み取り件数だけです
// （1 件は 1 行で、題名・状態・音声の有無しか使いません）。ページ送りを
// 押す回数が半分になるほうが、ページあたり 50 件の読み取りより価値があります。
const historyPerPage = 100

// maxPerPage は ?per_page= で要求できる上限です。
//
// 上限が無いと、1 リクエストが倉庫への際限のない 1 クエリになります。既定と
// 同じ値ですが、別の定数のままにします — 片方は「何も指定しなかったとき」、
// もう片方は「いくらまで指定できるか」で、変える理由が別だからです。
// 画面は既定しか使わないので、これが効くのは機械から叩く経路だけです。
const maxPerPage = 100

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
	Page jobstatus.PageMeta
	// Filter は絞り込み中の状態です（空なら全件）。ページ送りのリンクに
	// 引き継がないと、2 ページ目で絞り込みが外れます。
	Filter string
}

// listableStates は ?state= に指定できる値です。
//
// jobstatus の語彙をそのまま使います。ここで独自の綴りを作ると、記録に
// 書かれている値と画面が受け付ける値が別物になります。
func listableStates() []string {
	return []string{
		string(jobstatus.StateQueued),
		string(jobstatus.StateRunning),
		string(jobstatus.StateSucceeded),
		string(jobstatus.StateFailed),
	}
}

// stateParam は ?state= を読みます。空なら絞り込みなし、未知の値は false です。
//
// page と違って黙って無視しません。打ち間違えた絞り込みが全件を返すと、
// 「失敗したジョブは無い」と読めてしまいます。
func stateParam(r *http.Request) (jobstatus.State, bool) {
	value := strings.TrimSpace(r.URL.Query().Get("state"))
	if value == "" {
		return "", true
	}
	if !slices.Contains(listableStates(), value) {
		return "", false
	}
	return jobstatus.State(value), true
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

// Detail は、1 件のジョブの台本を表示します。ここから音声の確認と作成を行います。
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	jobID, ok := h.jobID(w, r)
	if !ok {
		return
	}
	h.renderDetail(w, r, jobID, http.StatusOK, "", "")
}

// UpdateScript は、編集された台本を保存し、続けて音声の作成を指示します。
//
// 保存が先で、タスクへ渡すのはジョブ ID だけです（理由は domain.Request.JobID）。
// Worker 側は「保存済み台本を読む」既存の経路をそのまま使います。
func (h *Handler) UpdateScript(w http.ResponseWriter, r *http.Request) {
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

	h.renderDetail(w, r, jobID, status, "台本を保存し、音声の作成を受け付けました。完了すると通知が届きます。", "")
}

// Regenerate は、同じ入力ソースから台本を作り直します。
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

	h.renderDetail(w, r, jobID, code, "同じ入力から台本を作り直しています。完了すると通知が届きます。", "")
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
