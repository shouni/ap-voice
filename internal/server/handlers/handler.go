// Package handlers は、Web 面の HTTP ハンドラーを実装します。
package handlers

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/shouni/gcp-kit/auth/session"

	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// jobIDPrefix は発行するジョブ ID の接頭辞です（voice-{日付}-{時刻}-{hex12}）。
const jobIDPrefix = "voice"

// Signer は、ハンドラが必要とする署名機能だけを表します。
// remoteio.Store がそのまま満たします。
type Signer interface {
	SignURL(ctx context.Context, name, method string, expires time.Duration) (string, error)
}

// Handler は Web 面のハンドラーです。
type Handler struct {
	queue     domain.TaskQueue
	templates map[string]*template.Template
	// modes は投入フォームに出す生成モードです。**生成時と同じ埋め込みテンプレートから
	// 取ります。** フォーム側が別の一覧を持つと、画面に出したモードが worker に無い、
	// という食い違いが起こり得ます。表示名と説明はプロンプトの front matter です。
	modes []assets.Mode
	// models は GEMINI_MODELS です。先頭が既定で、フォームでは選択肢になります。
	models []string
	// bucket と layout で出力先を決めます。**利用者には入力させません。**
	bucket string
	layout domain.StorageLayout
	// musicBucket は、楽曲紹介モードの入力を楽曲生成サービスのジョブ ID から解決するために使います。
	// **動画生成サービスと同じ規則です**（gs://<musicBucket>/music/<jobID>/recipe.json）。
	musicBucket string
	// repo は履歴の一覧と台本の読み出しです。
	repo ScriptRepository
	// signer は音声の署名付き URL を作ります。バイト列はアプリが配信しません。
	signer Signer
	// status は投入時に queued を記録します。**投入より先に書きます** —
	// Worker は配信されたタスクより先に状態を読むため、順序が逆だと
	// 1 つ前の記録を読んでしまいます（ap-story が実際に踏んだ順序です）。
	status *jobfirestore.Recorder[domain.JobStatus]
	// reading は、合成前に読みを確かめるために使います。
	reading ReadingConverter
	// renderer はカタログでプロンプト本文を見せるために使います。
	// **生成時と同じ組み立て**を通すので、画面に出るものと Gemini へ渡るものが一致します。
	renderer PromptRenderer
	// speakers は編集画面の選択肢です。**話者ごとに実在するスタイルだけ**を出すために持ちます。
	// 自由入力にすると、実在しない組み合わせを保存でき、合成時に既定スタイルへ黙って
	// 落ちて指示が無視されます。
	speakers *speaker.Registry
}

// ReadingConverter は、テキストが合成時にどう読まれるかを返します。
// go-voicevox が合成の直前に通すのと同じ変換です。
type ReadingConverter interface {
	ConvertToReading(text string) (string, error)
}

// PromptRenderer は、モードのプロンプト本文を組み立てます。
// 生成側（ScriptStep）が使うのと同じ実装を渡します。
type PromptRenderer interface {
	Generate(mode, content string) (string, error)
}

// ScriptRepository は、履歴の一覧と台本の読み出しです。
type ScriptRepository interface {
	List(ctx context.Context, page, perPage int) ([]repository.Job, jobfirestore.PageMeta, error)
	Load(ctx context.Context, jobID string) (domain.Script, error)
	SaveScript(ctx context.Context, jobID string, script domain.Script) error
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
	HasAudio(ctx context.Context, jobID string) (bool, error)
	Delete(ctx context.Context, jobID string) error
}

// signedURLExpiry は音声の署名付き URL の有効期限です。
// 再生している最中に切れない程度に取り、リンクを配って回れるほど長くはしません。
const signedURLExpiry = time.Hour

// HandlerOptions は Handler の依存です。
type HandlerOptions struct {
	Queue domain.TaskQueue
	// Templates は画面ごとのテンプレートセットです（キーは "history.html" などの
	// ファイル名）。全画面を 1 セットに入れられないのは、各画面が同じ名前で
	// content / title / scripts を define するためです。assets.ParsePages を使います。
	Templates map[string]*template.Template
	Modes     []assets.Mode
	Models    []string
	Bucket    string
	// MusicBucket は楽曲紹介モードの入力解決に使います。未設定でも起動はします
	// （そのタブがジョブ ID を受け付けなくなるだけで、他のモードは動きます）。
	MusicBucket string
	Repo        ScriptRepository
	Signer      Signer
	Speakers    *speaker.Registry
	Renderer    PromptRenderer
	Reading     ReadingConverter
	JobStatus   *jobfirestore.Recorder[domain.JobStatus]
}

// NewHandler は Handler を生成します。
func NewHandler(opts HandlerOptions) (*Handler, error) {
	if opts.Queue == nil {
		return nil, errors.New("TaskQueue が指定されていません")
	}
	if len(opts.Templates) == 0 {
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
	if opts.Renderer == nil {
		return nil, errors.New("プロンプトの組み立てが指定されていません")
	}
	if opts.Reading == nil {
		return nil, errors.New("読み変換が指定されていません")
	}

	return &Handler{
		queue:       opts.Queue,
		templates:   opts.Templates,
		modes:       append([]assets.Mode(nil), opts.Modes...),
		models:      append([]string(nil), opts.Models...),
		bucket:      opts.Bucket,
		musicBucket: opts.MusicBucket,
		layout:      domain.NewStorageLayout(),
		repo:        opts.Repo,
		signer:      opts.Signer,
		speakers:    opts.Speakers,
		renderer:    opts.Renderer,
		reading:     opts.Reading,
		status:      opts.JobStatus,
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

// base は全画面共通の値を組み立てます。
//
// CSRF トークンは CSRFContextMiddleware が context に入れたものです。
// フォームはこれを hidden で送り返し、Middleware が検証します。
func (h *Handler) base(r *http.Request) baseView {
	return baseView{
		CSRFToken:    session.CSRFTokenFromContext(r.Context()),
		Path:         r.URL.Path,
		DefaultModel: h.defaultModel(),
	}
}

// recordQueued は、投入を記録します。**enqueue より先に呼びます。**
//
// Cloud Tasks は数十ミリ秒で届くため、順序が逆だと Worker が書いた running を
// あとから queued で上書きしかねません。記録できなくても投入は続けます。
func (h *Handler) recordQueued(ctx context.Context, req domain.Request) {
	if h.status == nil {
		return
	}
	h.status.Record(ctx, req.JobID, domain.JobStatus{
		JobID:   req.JobID,
		Command: string(req.Command),
		State:   jobfirestore.StateQueued,
		Mode:    req.Mode,
		// **一覧はこれで並べ替えます。** 入れないと全件がゼロ値になり、
		// 新しい順が成立しません。作り直しでは CarryOver が最初の投入時刻を
		// 引き継ぐので、履歴の位置は動きません。
		QueuedAt: time.Now().UTC(),
	}, func(next, prev *domain.JobStatus) {
		// 作り直しでは、前回の成果物の在り処とモードを残します。
		next.CarryFrom(prev)
	})
}

// defaultModel は一覧の先頭を返します。空の一覧は NewHandler が弾いています。
func (h *Handler) defaultModel() string {
	if len(h.models) == 0 {
		return ""
	}
	return h.models[0]
}

// renderTemplate は指定の画面を描画します。name は画面のファイル名です。
//
// 描き始める前にバッファへ書き出します。途中で失敗したときヘッダーは送信済みで
// ステータスを変えられず、壊れた HTML が残ってしまうためです。
func (h *Handler) renderTemplate(w http.ResponseWriter, status int, name string, view any) {
	tmpl, ok := h.templates[name]
	if !ok {
		slog.Error("画面テンプレートが見つかりません", "page", name)
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, assets.PageTemplate, view); err != nil {
		slog.Error("画面の描画に失敗しました", "page", name, "error", err)
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
