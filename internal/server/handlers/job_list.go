package handlers

import (
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/shouni/ap-voice/internal/repository"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-serve-kit/respond"
)

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

// apiJobPage は、ページ付きの一覧応答です。
//
// メタデータの形は gcp-kit の PageMeta です。JSON タグは姉妹サービスと同じ
// JSON なので、呼び出し側はサービスごとに読み方を変えずに済みます。
type apiJobPage struct {
	Jobs []apiJob           `json:"jobs"`
	Page jobstatus.PageMeta `json:"page"`
}

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

// JobList は、ジョブを新しい順に返します（GET /jobs）。?page= と ?per_page= を受けます。
func (h *Handler) JobList(w http.ResponseWriter, r *http.Request) {
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

	var opts []jobstatus.ListOption
	if state != "" {
		opts = append(opts, jobstatus.WithState(state))
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
