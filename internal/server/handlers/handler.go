// Package handlers は、Web 面の HTTP ハンドラーを実装します。
package handlers

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"

	"github.com/shouni/ap-voice/internal/domain"
)

// Handler は Web 面のハンドラーです。
type Handler struct {
	queue     domain.TaskQueue
	templates *template.Template
	// modes は投入フォームに出す生成モードです。**生成時と同じ埋め込みテンプレートから
	// 取ります。** フォーム側が別の一覧を持つと、画面に出したモードが worker に無い、
	// という食い違いが起こり得ます。
	modes []string
}

// HandlerOptions は Handler の依存です。
type HandlerOptions struct {
	Queue     domain.TaskQueue
	Templates *template.Template
	Modes     []string
}

// NewHandler は Handler を生成します。
func NewHandler(opts HandlerOptions) (*Handler, error) {
	if opts.Queue == nil {
		return nil, errors.New("TaskQueue が指定されていません")
	}
	if opts.Templates == nil {
		return nil, errors.New("テンプレートが指定されていません")
	}
	if len(opts.Modes) == 0 {
		return nil, errors.New("生成モードが1つも読み込まれていません")
	}

	modes := append([]string(nil), opts.Modes...)
	sort.Strings(modes)

	return &Handler{queue: opts.Queue, templates: opts.Templates, modes: modes}, nil
}

// formView はフォーム画面に渡す値です。
type formView struct {
	Modes   []string
	Message string
	Error   string
	Form    domain.Request
}

// Home は投入フォームを表示します。
func (h *Handler) Home(w http.ResponseWriter, _ *http.Request) {
	h.render(w, http.StatusOK, formView{
		Modes: h.modes,
		Form:  domain.Request{Command: domain.CommandGenerate},
	})
}

// Enqueue はフォームの内容を検証し、Worker 面へ実行を引き渡します。
//
// ここでは合成を待ちません。分単位かかるため、リクエストの中で完了させられないためです。
// 結果は Slack 通知と出力先で受け取ります。
func (h *Handler) Enqueue(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, http.StatusBadRequest, domain.Request{}, "フォームの解析に失敗しました")
		return
	}

	req := domain.Request{
		Command:   domain.Command(r.FormValue("command")),
		InputURI:  r.FormValue("input_uri"),
		OutputURI: r.FormValue("output_uri"),
		Mode:      r.FormValue("mode"),
		AIModel:   r.FormValue("ai_model"),
	}

	// worker 側でも Execute の冒頭で検証しますが、投入前に弾けば
	// 「タスクにはなったが必ず失敗する」状態を作らずに済みます。
	if err := req.Validate(); err != nil {
		h.renderError(w, http.StatusBadRequest, req, err.Error())
		return
	}

	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		h.renderError(w, http.StatusBadGateway, req, err.Error())
		return
	}

	h.render(w, http.StatusAccepted, formView{
		Modes:   h.modes,
		Message: fmt.Sprintf("実行を受け付けました。完了すると %s に出力されます。", req.OutputURI),
		Form:    domain.Request{Command: domain.CommandGenerate},
	})
}

func (h *Handler) renderError(w http.ResponseWriter, status int, form domain.Request, msg string) {
	h.render(w, status, formView{Modes: h.modes, Error: msg, Form: form})
}

func (h *Handler) render(w http.ResponseWriter, status int, view formView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := h.templates.ExecuteTemplate(w, "home.html", view); err != nil {
		// ヘッダーは送信済みなので、ここでステータスは変えられません。
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
	}
}
