package builder

import (
	"errors"
	"fmt"

	"html/template"
	"net/url"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"
	"github.com/shouni/netarmor/securenet"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/server/handlers"
)

const defaultSessionName = "ap-voice-session"

// AppHandlers は生成されたすべての HTTP ハンドラーを保持する構造体です。
// server パッケージはこの構造体を受け取ってルーティングを行います。
type AppHandlers struct {
	// Auth は Google OAuth のログインとセッションを扱います。Web 面だけが持ちます。
	Auth *auth.Handler
	// Web は投入フォームと実行受付です。
	Web *handlers.Handler
	// Worker は Cloud Tasks から届いた domain.Request を実行します。
	// ペイロードのデコードとリトライ可否の判断は gcp-kit/worker が持つため、
	// このアプリ側に HTTP を意識したコードは要りません。
	Worker *worker.Handler[domain.Request]
	// TaskAuth は Cloud Tasks からの OIDC を検証します。OAuth 設定を必要としないため、
	// Web 面を持たない Worker プロセスでも構築できます。
	TaskAuth *auth.TaskVerifier
}

// Validate は、組み立て結果が役割として筋の通った形になっていることを確かめます。
//
// TaskAuth と Worker は「Cloud Tasks の検証」と「その先の処理」で対になっており、
// 片方だけが nil なのは DI の不整合です。router.go は nil を見てルート登録を省くため、
// 放置すると /tasks/generate が黙って 404 になるだけで、原因が設定なのか実装なのか
// リクエストからは区別できません。ルーターが 404 を返す前に起動を失敗させます。
func (h *AppHandlers) Validate() error {
	if (h.TaskAuth == nil) != (h.Worker == nil) {
		return errors.New("TaskAuth と Worker は同時に構成する必要があります")
	}
	// Web と Auth も対です。Auth が無いとフォームが認証なしで公開されます。
	if (h.Auth == nil) != (h.Web == nil) {
		return errors.New("認証ハンドラーと Web ハンドラーは同時に構成する必要があります")
	}
	return nil
}

// BuildHandlers は各ハンドラーの依存関係を SERVER_ROLE に応じて組み立て、
// AppHandlers 構造体を返します。担当しない面のハンドラーは nil のままにし、
// router 側でルート登録ごと省かれるようにします。
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	h := &AppHandlers{}
	role := appCtx.Config.Server.Role

	if role.ServesWeb() {
		if err := buildWebHandlers(appCtx, h); err != nil {
			return nil, err
		}
	}

	if role.ServesWorker() {
		// audience と許可する caller SA の両方が揃わないと検証は常に失敗する
		// （fail-closed）ため、起動時に構成を確かめておきます。
		taskAuth := auth.NewTaskVerifier(
			appCtx.Config.Tasks.TaskAudienceURL,
			appCtx.Config.Tasks.AllowedServiceAccounts,
		)
		if !taskAuth.Configured() {
			return nil, fmt.Errorf("タスク受信の OIDC 検証を構成できません: TASK_AUDIENCE_URL と ALLOWED_TASK_SERVICE_ACCOUNTS が必要です")
		}
		h.TaskAuth = taskAuth
		h.Worker = worker.NewHandler[domain.Request](appCtx.Pipeline)
	}

	if err := h.Validate(); err != nil {
		return nil, err
	}

	return h, nil
}

// buildWebHandlers は Web 面（OAuth と投入フォーム）のハンドラーを組み立てます。
func buildWebHandlers(appCtx *app.Container, h *AppHandlers) error {
	authHandler, err := createAuthHandler(appCtx.Config)
	if err != nil {
		return fmt.Errorf("認証ハンドラーの初期化に失敗しました: %w", err)
	}

	tmpl, err := template.ParseFS(assets.Templates, "templates/*.html")
	if err != nil {
		return fmt.Errorf("テンプレートの読み込みに失敗しました: %w", err)
	}

	// フォームの選択肢は生成時と同じ埋め込みから取ります。別の一覧を持つと、
	// 画面に出したモードが worker に無い、という食い違いが起こり得ます。
	// 表示名と説明も同じファイルの front matter から来ます。
	modes, err := assets.LoadModes()
	if err != nil {
		return fmt.Errorf("生成モードの読み込みに失敗しました: %w", err)
	}

	webHandler, err := handlers.NewHandler(handlers.HandlerOptions{
		Queue:     appCtx.TaskQueue,
		Templates: tmpl,
		Modes:     modes,
		Models:    appCtx.Config.AI.GeminiModels,
		Bucket:    appCtx.Config.Storage.GCSBucket,
		Repo:      appCtx.Repository,
		Signer:    appCtx.RemoteIO.Signer,
		Speakers:  appCtx.Speakers,
	})
	if err != nil {
		return fmt.Errorf("投入フォームのハンドラー初期化に失敗しました: %w", err)
	}

	h.Auth = authHandler
	h.Web = webHandler
	return nil
}

// createAuthHandler は、Google OAuth の認証ハンドラーを初期化します。
func createAuthHandler(cfg *config.Config) (*auth.Handler, error) {
	redirectURL, err := url.JoinPath(cfg.Server.ServiceURL, "/auth/callback")
	if err != nil {
		return nil, fmt.Errorf("リダイレクトURLの構築に失敗しました: %w", err)
	}

	return auth.NewHandler(auth.Config{
		ClientID:          cfg.Auth.GoogleClientID,
		ClientSecret:      cfg.Auth.GoogleClientSecret,
		RedirectURL:       redirectURL,
		SessionAuthKey:    cfg.Auth.SessionSecret,
		SessionEncryptKey: cfg.Auth.SessionEncryptKey,
		SessionName:       defaultSessionName,
		IsSecureCookie:    securenet.IsSecureServiceURL(cfg.Server.ServiceURL),
		AllowedEmails:     cfg.Auth.AllowedEmails,
		AllowedDomains:    cfg.Auth.AllowedDomains,
	})
}
