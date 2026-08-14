// Package server は、HTTPルーティングとミドルウェアを構成します。
package server

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/cloudlog"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/builder"
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
}

// setupRoutes は、各コンポーネントのハンドラーをルーティングに登録します。
//
// ルートはハンドラーの nil を見て登録を省きます。担当しない面のハンドラーは
// BuildHandlers が nil のままにするため、役割が増えてもここを触らずに済みます。
func setupRoutes(r chi.Router, h *builder.AppHandlers) {
	// "/healthz" は Cloud Run のデフォルトドメイン (*.run.app) 側で予約パス的に扱われ、
	// コンテナまでリクエストが届かず GFE の汎用 404 に置き換えられるため使わない。
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

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
		r.Use(h.Auth.Middleware)
		r.Get("/", h.Web.Home)
		r.Post("/", h.Web.Enqueue)

		// 台本ができたら履歴に並び、詳細から音声を作ります。
		r.Route("/history", func(r chi.Router) {
			r.Get("/", h.Web.History)
			r.Get("/{jobID}", h.Web.Detail)
			r.Post("/{jobID}/synthesize", h.Web.Synthesize)
			r.Get("/{jobID}/audio", h.Web.Audio)
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

		// Cloud Tasks からの OIDC トークンを検証 (セッション不要)
		r.Use(h.TaskAuth.Middleware)
		r.Method(http.MethodPost, "/tasks/generate", h.Worker)
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
		w.Header().Set("Cache-Control", "public, max-age=300, must-revalidate")
		fileServer.ServeHTTP(w, r)
	}))
}
