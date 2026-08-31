// Package server は、HTTPルーティングとミドルウェアを構成します。
package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/gcp-kit/cloudrun"
	"github.com/shouni/go-serve-kit/secureheaders"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/auth"
)

// NewRouter は、ミドルウェアとルーティングを統合した http.Handler を構築します。
// projectID は Cloud Logging のトレース相関にのみ使用し、空なら相関を行いません。
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	setupCommonMiddleware(r, projectID)
	setupRoutes(r, h)

	return r
}

// setupCommonMiddleware は、標準的なミドルウェアを構成します。
func setupCommonMiddleware(r *chi.Mux, projectID string) {
	// トレース相関はログ出力より先に効かせる必要があるため最初に登録する。
	r.Use(cloudlog.TraceMiddleware(projectID))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
	// 画面は日本語 UTF-8（1 文字 3 バイト）なので圧縮がよく効きます。静的ファイルも
	// 同じ経路に乗ります（vendor は immutable なので再圧縮は稀です）。
	r.Use(middleware.Compress(compressionLevel))
	r.Use(secureheaders.Middleware(secureheaders.Config{
		MediaSources: []string{gcsOrigin},
		// Bootstrap の JS が遷移中にインラインスタイルを当てるため。
		AllowInlineStyle: true,
	}))
}

// compressionLevel は gzip の圧縮レベルです。
const compressionLevel = 5

// gcsOrigin は、合成音声の実体である GCS のオリジンです。画面は同一オリジンの
// エンドポイントを指しますが、そこから署名付き URL へ 302 するため、送り先を明示します。
const gcsOrigin = "https://storage.googleapis.com"

// setupRoutes は、各コンポーネントのハンドラーをルーティングに登録します。
//
// ルートはハンドラーの nil を見て登録を省きます。担当しない面のハンドラーは
// BuildHandlers が nil のままにするため、役割が増えてもここを触らずに済みます。
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// "/healthz" は Cloud Run のデフォルトドメイン (*.run.app) 側で予約パス的に扱われ、
	// コンテナまでリクエストが届かず GFE の汎用 404 に置き換えられるため使わない。
	// パスの選択理由（"/healthz" を使わない）は cloudrun.HealthPath を参照。
	r.Get(cloudrun.HealthPath, cloudrun.Health)

	setupStaticRoutes(r)

	if h == nil {
		slog.Warn("AppHandlers is nil, skipping application routes registration")
		return
	}

	// 認証関連 (OAuth2 フロー)。ログイン自体は保護しません。
	if h.Auth != nil {
		r.Route("/auth", func(r chi.Router) {
			r.Get("/login", h.Auth.Login)
			r.Get("/callback", h.Auth.Callback)
			r.Get("/logout", h.Auth.Logout)
		})
	}

	// Web 面。未認証のアクセスは Auth のミドルウェアが /auth/login へ送ります。
	// Auth と Web は AppHandlers.Validate が対であることを保証しているので、
	// ここでは片方の nil だけ見れば足ります。
	r.Group(func(r chi.Router) {
		if h.Web == nil {
			return
		}
		// ブラウザと機械の両方を通します。先に M2M の OIDC を試し、Bearer が
		// 無ければセッション認証へ落とします。人向けの方式を最後に置いているのは、
		// どれも成立しなかったときにログイン画面へ送るためです（JSON を求めて
		// いる相手には 401 が返ります）。
		r.Use(auth.Protected(h.M2M, h.Auth))

		r.Get("/", h.Web.Home)
		r.Post("/", h.Web.Enqueue)
		r.Get("/modes", h.Web.Modes)
		r.Get("/modes/{mode}", h.Web.ModeDetail)

		// ページを持たない操作ですが、画面も叩くので /api の下には置きません。
		r.Post("/preview-reading", h.Web.PreviewReading)

		// 人と機械が同じものを見るため、ルートは 1 本です（表現は handlers/negotiated.go）。
		r.Route("/history", func(r chi.Router) {
			r.Get("/", h.Web.Jobs)
			r.Get("/{jobID}", h.Web.Detail)
			r.Post("/{jobID}/script", h.Web.UpdateScript)
			r.Post("/{jobID}/regenerate", h.Web.Regenerate)
			r.Post("/{jobID}/delete", h.Web.Delete)
			r.Get("/{jobID}/audio", h.Web.Audio)
			r.Get("/{jobID}/script", h.Web.Script)
			r.Get("/{jobID}/status", h.Web.JobStatus)
		})

		// 機械だけが叩く操作です。画面も叩くものはこの外にあります。
		r.Route("/api", func(r chi.Router) {
			r.Get("/speakers", h.Web.APISpeakers)
			r.Route("/jobs", func(r chi.Router) {
				r.Post("/", h.Web.APIEnqueue)
				r.Put("/{jobID}/script", h.Web.APIUpdateScript)
				r.Post("/{jobID}/synthesize", h.Web.APISynthesize)
				// 画面は POST /history/{jobID}/delete を叩きます（実装は同じ Delete です）。
				r.Delete("/{jobID}", h.Web.Delete)
			})
		})
	})

	// Cloud Tasks 専用ルート (Worker 用)。
	// SERVER_ROLE=web のプロセスでは TaskAuth も Worker も nil になるため、
	// このグループごと登録されず /tasks/generate は公開されません。
	// 片方だけが nil になる形は builder.AppHandlers.Validate が起動時に弾くので、
	// ここでは TaskAuth の有無だけを見れば足ります。
	r.Group(func(r chi.Router) {
		if h.TaskAuth == nil {
			return
		}

		// Cloud Tasks からの OIDC トークンを検証 (セッション不要)。
		// 失敗はセッションへフォールバックせず、その場で止まります。
		r.Use(auth.Require(h.TaskAuth))
		r.Post(domain.WorkerTaskPath, h.Worker.ProcessTask)
	})
}

// setupStaticRoutes は、埋め込み済みの静的ファイル（CSS）を /static/* で配信します。
//
// 認証の外側に置きます。スタイルシートにログインを求める理由が無く、
// 未認証で表示されるログイン画面からも参照されるためです。
func setupStaticRoutes(r chi.Router) {
	staticFS, err := fs.Sub(assets.StaticFiles, "static")
	if err != nil {
		slog.Error("static assets are unavailable", "error", err)
		return
	}

	fileServer := http.StripPrefix("/static/", http.FileServer(http.FS(staticFS)))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cacheControlFor(r.URL.Path))
		fileServer.ServeHTTP(w, r)
	}))
}

// vendorPathPrefix より下は第三者製の配布物で、パスにバージョンが入っています
// （assets/static/vendor/bootstrap-5.3.8 など）。更新すれば必ず別の URL になるので、
// 再検証させる理由がありません。
const vendorPathPrefix = "/static/vendor/"

const (
	// ownAssetCacheControl は自前の CSS / JS 用です。URL を変えずに中身が変わるため短命にします。
	ownAssetCacheControl = "public, max-age=300, must-revalidate"
	// vendorCacheControl は vendorPathPrefix 配下用です。
	vendorCacheControl = "public, max-age=31536000, immutable"
)

// cacheControlFor は、静的ファイルのパスに応じた Cache-Control を返します。
//
// //go:embed した FileServer は Last-Modified も ETag も出せない（embed の ModTime が
// ゼロ値のため net/http が両方を省く）ので、期限が切れた時点で必ず全体を取り直します。
// バージョン付きの vendor を分けているのは、その再取得を無くすためです。
func cacheControlFor(path string) string {
	if strings.HasPrefix(path, vendorPathPrefix) {
		return vendorCacheControl
	}
	return ownAssetCacheControl
}
