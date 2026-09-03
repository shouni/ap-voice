// Package server は、HTTPルーティングとミドルウェアを構成します。
package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/gcp-kit/cloudrun"
	"github.com/shouni/go-serve-kit/secureheaders"
	"github.com/shouni/go-serve-kit/staticfiles"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/domain"
)

// compressionLevel は gzip の圧縮レベルです。
const compressionLevel = 5

// gcsOrigin は、合成音声の実体である GCS のオリジンです。画面は同一オリジンの
// エンドポイントを指しますが、そこから署名付き URL へ 302 するため、送り先を明示します。
const gcsOrigin = "https://storage.googleapis.com"

// NewRouter は、ミドルウェアとルーティングを統合した http.Handler を構築します。
// projectID は Cloud Logging のトレース相関にのみ使用し、空なら相関を行いません。
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	registerMiddleware(r, projectID)
	registerRoutes(r, h)

	return r
}

// registerMiddleware は、標準的なミドルウェアを構成します。
func registerMiddleware(r *chi.Mux, projectID string) {
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

// registerRoutes は、各コンポーネントのハンドラーをルーティングに登録します。
//
// ルートはハンドラーの nil を見て登録を省きます。担当しない面のハンドラーは
// BuildHandlers が nil のままにするため、役割が増えてもここを触らずに済みます。
func registerRoutes(r chi.Router, h *builder.AppHandlers) {
	// パスの選択理由（"/healthz" を使わない）は cloudrun.HealthPath を参照。
	r.Get(cloudrun.HealthPath, cloudrun.Health)

	registerStaticRoutes(r)

	if h == nil {
		slog.Warn("AppHandlers is nil, skipping application routes registration")
		return
	}

	// 認証関連 (OAuth2 フロー)。ログイン自体は保護しません。
	if h.Auth != nil {
		r.Handle("/auth/*", h.Auth.Routes()) // login / callback / logout
	}

	// Web 面。未認証のアクセスは Auth のミドルウェアが /auth/login へ送ります。
	// Auth と Web は AppHandlers.Validate が対であることを保証しているので、
	// ここでは片方の nil だけ見れば足ります。
	r.Group(func(r chi.Router) {
		if h.Web == nil {
			return
		}
		// ブラウザ（セッション + CSRF）と機械（OIDC Bearer）が同じルートを叩きます。
		// 二系統の合成をライブラリに任せるのは、経路ごとに自前で組むと片方だけ
		// 強化されてドリフトするためです。判定順と CSRF の扱いは auth.Protected を参照。
		r.Use(auth.Protected(h.M2M, h.Auth))

		r.Get("/", h.Web.Home)
		r.Get("/modes", h.Web.Modes)
		r.Get("/modes/{mode}", h.Web.ModeDetail)
		r.Get("/speakers", h.Web.Speakers)
		// 読みの確認はどのジョブにも属しません（保存前の行を送るため）。
		r.Post("/reading/preview", h.Web.PreviewReading)

		// ジョブが唯一の主リソースです。投入から削除まで同じ /jobs/{jobID} で指し、
		// 人と機械は同じルートを Accept と本文の形で使い分けます（public-docs の
		// URL 命名規約）。
		r.Route("/jobs", func(r chi.Router) {
			r.Post("/", h.Web.JobCreate)
			r.Get("/", h.Web.JobList)
			r.Get("/{jobID}", h.Web.Job)
			r.Delete("/{jobID}", h.Web.JobDelete)
			r.Get("/{jobID}/audio", h.Web.Audio)
			r.Get("/{jobID}/script", h.Web.Script)
			r.Put("/{jobID}/script", h.Web.ScriptUpdate)
			r.Post("/{jobID}/synthesize", h.Web.Synthesize)
			r.Post("/{jobID}/regenerate", h.Web.Regenerate)
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

		r.Use(auth.Require(h.TaskAuth))
		r.Post(domain.WorkerTaskPath, h.Worker.ProcessTask)
	})
}

// registerStaticRoutes は、埋め込み済みの静的ファイルを /static/* で配信します。
// Cache-Control の判断（自前は短命、vendor は不変）とディレクトリ一覧の抑止は
// go-serve-kit の staticfiles が持ちます。
//
// 認証の外側に置きます。スタイルシートにログインを求める理由が無く、
// 未認証で表示されるログイン画面からも参照されるためです。
func registerStaticRoutes(r chi.Router) {
	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
	if err != nil {
		// 埋め込んだ定数の取り違えなので、リクエストを受ける前に止めます。
		panic(fmt.Sprintf("static assets: %v", err))
	}
	r.Handle("/static/*", files)
}
