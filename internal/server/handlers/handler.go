// Package handlers は、Web 面の HTTP ハンドラーを実装します。
package handlers

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/go-utils/jobid"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// jobIDPrefix は発行するジョブ ID の接頭辞です（voice-{日付}-{時刻}-{hex12}）。
const jobIDPrefix = "voice"

// Handler は Web 面のハンドラーです。
type Handler struct {
	queue     domain.TaskQueue
	templates *template.Template
	// modes は投入フォームに出す生成モードです。**生成時と同じ埋め込みテンプレートから
	// 取ります。** フォーム側が別の一覧を持つと、画面に出したモードが worker に無い、
	// という食い違いが起こり得ます。表示名と説明はプロンプトの front matter です。
	modes []assets.Mode
	// models は GEMINI_MODELS です。先頭が既定で、フォームでは選択肢になります。
	models []string
	// bucket と layout で出力先を決めます。**利用者には入力させません。**
	bucket string
	layout domain.StorageLayout
	// repo は履歴の一覧と台本の読み出しです。
	repo ScriptRepository
	// signer は音声の署名付き URL を作ります。バイト列はアプリが配信しません。
	signer remoteio.URLSigner
	// speakers は編集画面の選択肢です。**話者ごとに実在するスタイルだけ**を出すために持ちます。
	// 自由入力にすると、実在しない組み合わせを保存でき、合成時に既定スタイルへ黙って
	// 落ちて指示が無視されます。
	speakers *speaker.Registry
}

// ScriptRepository は、履歴の一覧と台本の読み出しです。
type ScriptRepository interface {
	List(ctx context.Context, limit int) ([]repository.Job, error)
	Load(ctx context.Context, jobID string) (domain.Script, error)
	Save(ctx context.Context, jobID string, script domain.Script) error
	HasAudio(ctx context.Context, jobID string) (bool, error)
	Delete(ctx context.Context, jobID string) error
}

// signedURLExpiry は音声の署名付き URL の有効期限です。
// 再生している最中に切れない程度に取り、リンクを配って回れるほど長くはしません。
const signedURLExpiry = time.Hour

// HandlerOptions は Handler の依存です。
type HandlerOptions struct {
	Queue     domain.TaskQueue
	Templates *template.Template
	Modes     []assets.Mode
	Models    []string
	Bucket    string
	Repo      ScriptRepository
	Signer    remoteio.URLSigner
	Speakers  *speaker.Registry
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
	if opts.Repo == nil {
		return nil, errors.New("リポジトリが指定されていません")
	}
	if opts.Speakers == nil {
		return nil, errors.New("話者一覧が指定されていません")
	}

	return &Handler{
		queue:     opts.Queue,
		templates: opts.Templates,
		modes:     append([]assets.Mode(nil), opts.Modes...),
		models:    append([]string(nil), opts.Models...),
		bucket:    opts.Bucket,
		layout:    domain.NewStorageLayout(),
		repo:      opts.Repo,
		signer:    opts.Signer,
		speakers:  opts.Speakers,
	}, nil
}

// baseView は全画面で共通の値です。ナビが .Path と .DefaultModel を見ます。
type baseView struct {
	CSRFToken string
	// Path は現在地です。ナビのハイライトに使います。
	Path string
	// DefaultModel は GEMINI_MODELS の先頭です。モデル ID は Google の都合で
	// 変わるため、画面に文言で書かず設定から出します。
	DefaultModel string
}

// formView はフォーム画面に渡す値です。
type formView struct {
	baseView
	Modes   []assets.Mode
	Models  []string
	Message string
	Error   string
	Form    domain.Request
}

// base は全画面共通の値を組み立てます。
//
// CSRF トークンは CSRFContextMiddleware が context に入れたものです。
// フォームはこれを hidden で送り返し、Middleware が検証します。
func (h *Handler) base(r *http.Request) baseView {
	return baseView{
		CSRFToken:    auth.CSRFTokenFromContext(r.Context()),
		Path:         r.URL.Path,
		DefaultModel: h.defaultModel(),
	}
}

// defaultModel は一覧の先頭を返します。空の一覧は NewHandler が弾いています。
func (h *Handler) defaultModel() string {
	if len(h.models) == 0 {
		return ""
	}
	return h.models[0]
}

// Home は投入フォームを表示します。
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, formView{
		baseView: h.base(r),
		Modes:    h.modes,
		Models:   h.models,
		Form:     domain.Request{Command: domain.CommandGenerate},
	})
}

// Enqueue はフォームの内容を検証し、Worker 面へ実行を引き渡します。
//
// ここでは合成を待ちません。分単位かかるため、リクエストの中で完了させられないためです。
// 結果は Slack 通知と出力先で受け取ります。
func (h *Handler) Enqueue(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{}, "フォームの解析に失敗しました")
		return
	}

	// **フォームが選べる command は限られます。** synthesize は台本が要り、この画面には
	// それを渡す口がありません。Validate も弾きますが、受け付ける値をここで명示して
	// おくと、画面から来るものと API から来るものの境界がはっきりします。
	command := domain.Command(r.FormValue("command"))
	if command != domain.CommandGenerate && command != domain.CommandGenerateAndSynthesize {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{}, "この画面から実行できない処理です")
		return
	}

	// **出力先はフォームから受け取りません。** ジョブ ID から導くことで、1 ジョブの
	// 成果物が必ず 1 つのプレフィックスにまとまり、あとから一覧・削除できます。
	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, domain.Request{}, "ジョブIDの発行に失敗しました")
		return
	}

	req := domain.Request{
		Command:   command,
		JobID:     jobID,
		InputURI:  r.FormValue("input_uri"),
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      r.FormValue("mode"),
		AIModel:   r.FormValue("ai_model"),
	}

	// worker 側でも Execute の冒頭で検証しますが、投入前に弾けば
	// 「タスクにはなったが必ず失敗する」状態を作らずに済みます。
	if err := req.Validate(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, req, err.Error())
		return
	}

	if err := h.queue.Enqueue(r.Context(), req); err != nil {
		h.renderError(w, r, http.StatusBadGateway, req, err.Error())
		return
	}

	h.render(w, http.StatusAccepted, formView{
		baseView: h.base(r),
		Modes:    h.modes,
		Models:   h.models,
		Message:  fmt.Sprintf("台本の作成を受け付けました（%s）。完了すると履歴に並びます。", req.JobID),
		// **投入した内容をそのまま残します。** 同じソースからモードを変えて
		// もう1本作るのが普通の使い方で、空に戻すと URL を貼り直すことになります。
		// ジョブ ID と出力先は毎回発行し直すため、残っていても次の投入には影響しません。
		Form: req,
	})
}

// acceptedMessage は、受け付けた処理に応じた案内文を返します。
// **どちらを押したかが分かる文面**にします。まとめて作った場合は音声まで待つため、
// 「履歴に並びます」だけでは、次に何を待てばよいのか分かりません。
func acceptedMessage(command domain.Command, jobID string) string {
	if command == domain.CommandGenerateAndSynthesize {
		return fmt.Sprintf("台本と音声の作成を受け付けました（%s）。完了すると通知が届きます。", jobID)
	}
	return fmt.Sprintf("台本の作成を受け付けました（%s）。完了すると履歴に並びます。", jobID)
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, form domain.Request, msg string) {
	h.render(w, status, formView{
		baseView: h.base(r),
		Modes:    h.modes,
		Models:   h.models,
		Error:    msg,
		Form:     form,
	})
}

func (h *Handler) render(w http.ResponseWriter, status int, view formView) {
	h.renderTemplate(w, status, "home.html", view)
}

// renderTemplate は指定のテンプレートを描画します。
func (h *Handler) renderTemplate(w http.ResponseWriter, status int, name string, view any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := h.templates.ExecuteTemplate(w, name, view); err != nil {
		// ヘッダーは送信済みなので、ここでステータスは変えられません。
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
	}
}
