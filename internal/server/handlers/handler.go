// Package handlers は、Web 面の HTTP ハンドラーを実装します。
package handlers

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
)

// jobIDPrefix は発行するジョブ ID の接頭辞です（voice-{日付}-{時刻}-{hex12}）。
const jobIDPrefix = "voice"

// Handler は Web 面のハンドラーです。
type Handler struct {
	queue     domain.TaskQueue
	templates *template.Template
	// modes は投入フォームに出す生成モードです。**生成時と同じ埋め込みテンプレートから
	// 取ります。** フォーム側が別の一覧を持つと、画面に出したモードが worker に無い、
	// という食い違いが起こり得ます。
	modes []string
	// models は GEMINI_MODELS です。先頭が既定で、フォームでは選択肢になります。
	models []string
	// bucket と layout で出力先を決めます。**利用者には入力させません。**
	bucket string
	layout domain.StorageLayout
}

// HandlerOptions は Handler の依存です。
type HandlerOptions struct {
	Queue     domain.TaskQueue
	Templates *template.Template
	Modes     []string
	Models    []string
	Bucket    string
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
	if len(opts.Models) == 0 {
		return nil, errors.New("モデルが1つも指定されていません")
	}
	if opts.Bucket == "" {
		return nil, errors.New("出力先バケットが指定されていません")
	}

	modes := append([]string(nil), opts.Modes...)
	sort.Strings(modes)

	return &Handler{
		queue:     opts.Queue,
		templates: opts.Templates,
		modes:     modes,
		models:    append([]string(nil), opts.Models...),
		bucket:    opts.Bucket,
		layout:    domain.NewStorageLayout(),
	}, nil
}

// formView はフォーム画面に渡す値です。
type formView struct {
	Modes   []string
	Models  []string
	Message string
	Error   string
	Form    domain.Request
}

// Home は投入フォームを表示します。
func (h *Handler) Home(w http.ResponseWriter, _ *http.Request) {
	h.render(w, http.StatusOK, formView{
		Modes:  h.modes,
		Models: h.models,
		Form:   domain.Request{Command: domain.CommandGenerate},
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

	// **出力先はフォームから受け取りません。** ジョブ ID から導くことで、1 ジョブの
	// 成果物が必ず 1 つのプレフィックスにまとまり、あとから一覧・削除できます。
	//
	// command も同様にフォームでは選ばせません。synthesize は台本が要り、この画面には
	// それを渡す口が無いためです。台本からの再合成は履歴の詳細画面が担います。
	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, domain.Request{}, "ジョブIDの発行に失敗しました")
		return
	}

	req := domain.Request{
		Command:   domain.Command(r.FormValue("command")),
		JobID:     jobID,
		InputURI:  r.FormValue("input_uri"),
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
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
		Models:  h.models,
		Message: fmt.Sprintf("実行を受け付けました（%s）。完了すると %s に出力されます。", req.JobID, req.OutputURI),
		Form:    domain.Request{Command: domain.CommandGenerate},
	})
}

func (h *Handler) renderError(w http.ResponseWriter, status int, form domain.Request, msg string) {
	h.render(w, status, formView{Modes: h.modes, Models: h.models, Error: msg, Form: form})
}

func (h *Handler) render(w http.ResponseWriter, status int, view formView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := h.templates.ExecuteTemplate(w, "home.html", view); err != nil {
		// ヘッダーは送信済みなので、ここでステータスは変えられません。
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
	}
}
