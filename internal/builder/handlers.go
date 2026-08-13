package builder

import (
	"errors"
	"fmt"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/domain"
)

// AppHandlers は生成されたすべての HTTP ハンドラーを保持する構造体です。
// server パッケージはこの構造体を受け取ってルーティングを行います。
type AppHandlers struct {
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
	return nil
}

// BuildHandlers は各ハンドラーの依存関係を SERVER_ROLE に応じて組み立て、
// AppHandlers 構造体を返します。担当しない面のハンドラーは nil のままにし、
// router 側でルート登録ごと省かれるようにします。
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	h := &AppHandlers{}
	role := appCtx.Config.Server.Role

	if role.ServesWorker() {
		// audience と許可する caller SA の両方が揃わないと検証は常に失敗する
		// （fail-closed）ため、起動時に構成を確かめておきます。
		taskAuth := auth.NewTaskVerifier(
			appCtx.Config.Tasks.TaskAudienceURL,
			appCtx.Config.Tasks.AllowedServiceAccounts,
		)
		if !taskAuth.Configured() {
			return nil, fmt.Errorf("cloud Tasks の OIDC 検証を構成できません: TASK_AUDIENCE_URL と ALLOWED_TASK_SERVICE_ACCOUNTS が必要です")
		}
		h.TaskAuth = taskAuth
		h.Worker = worker.NewHandler[domain.Request](appCtx.Pipeline)
	}

	if err := h.Validate(); err != nil {
		return nil, err
	}

	return h, nil
}
