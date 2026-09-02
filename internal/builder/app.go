// Package builder は、設定値から各クライアントと DI コンテナを組み立てます。
package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/shouni/gcp-kit/tasks"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-remote-io/remoteio/gcs"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/adapters"
	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// BuildContainer は外部サービスとの接続を確立し、依存関係を組み立てた app.Container を返します。
func BuildContainer(ctx context.Context, cfg *config.Config) (container *app.Container, err error) {
	var resources []io.Closer
	defer func() {
		if err != nil {
			for _, r := range resources {
				if closeErr := r.Close(); closeErr != nil {
					slog.Warn("failed to close resource during cleanup", "error", closeErr)
				}
			}
		}
	}()

	// 2. I/O Infrastructure
	storage, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS factory: %w", err)
	}

	resources = append(resources, storage)

	store, err := storage.Store()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IO components: %w", err)
	}

	// ジョブ状態の置き場。成果物は GCS のまま、状態だけを Firestore に持ちます。
	// 両ロールで要ります— web は queued を書いて履歴を読み、worker は
	// running / succeeded / failed を書いて再実行ガードのために読みます。
	jobStatus, err := jobfirestore.New(ctx,
		jobfirestore.WithProjectID(cfg.GCP.ProjectID),
		jobfirestore.WithDatabase(cfg.Storage.FirestoreDatabase),
	)
	if err != nil {
		return nil, fmt.Errorf("firestore の初期化に失敗しました: %w", err)
	}
	resources = append(resources, jobStatus)

	jobStatusClient, err := jobStatus.Client()
	if err != nil {
		return nil, fmt.Errorf("firestore クライアントの取得に失敗しました: %w", err)
	}

	httpClient := httpkit.New(
		httpkit.WithTimeout(cfg.HTTP.Timeout),
		httpkit.WithMaxRetries(1),
		httpkit.WithSkipNetworkValidation(true),
	)

	// 3. Notifier
	notifier, err := buildNotifier(httpClient.WithoutRetry(), cfg.Notification.SlackWebhookURL, cfg.Server.ServiceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize notifier: %w", err)
	}

	// 話者一覧は埋め込みリソースなので、外部接続より先に組みます。
	// 壊れていれば起動時に落とし、合成を始めてから気付くことがないようにします。
	speakers, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		return nil, fmt.Errorf("話者一覧の読み込みに失敗しました: %w", err)
	}

	// プロンプトの組み立ても埋め込みリソースだけで作れます。生成する段と
	// カタログの両方が同じものを使うので、ここで 1 つだけ組みます。
	prompt, err := adapters.NewPromptAdapter()
	if err != nil {
		return nil, fmt.Errorf("プロンプトの組み立てに失敗しました: %w", err)
	}

	// 成果物の読み出しは両ロールで使います。web は履歴の表示に、
	// worker は synthesize が保存済み台本を読むために。
	repo, err := repository.NewRepository(store, cfg.Storage.GCSBucket, jobStatusClient)
	if err != nil {
		return nil, fmt.Errorf("リポジトリの初期化に失敗しました: %w", err)
	}

	appCtx := &app.Container{
		Config: cfg,
		// Repository が StatusStore を満たすので、保存先の組み立てはそちら 1 か所です。
		JobStatus:  jobfirestore.NewRecorder[domain.JobStatus](repo),
		Speakers:   speakers,
		Prompt:     prompt,
		Repository: repo,
		Storage:    storage,
		Store:      store,
		HTTPClient: httpClient,
		Notifier:   notifier,
		// Store はファクトリが持つクライアントを包むだけで、寿命はファクトリ側にあります。
		// 組み立てに失敗したときは resources 側が storage を直接閉じるため、
		// Closers は成功して返ったあとの解放だけを受け持ちます。
		Closers: []io.Closer{storage, jobStatus},
	}

	// タスクを投入するのは Web 面だけです。Worker 面は受け取る側なので、組み立てないことで
	// 未使用の Cloud Tasks クライアントと CLOUD_TASKS_QUEUE_ID への依存を持たずに済みます。
	if cfg.Server.Role.ServesWeb() {
		taskURL, wErr := domain.WorkerTaskURL(cfg.Tasks.WorkerURL)
		if wErr != nil {
			return nil, wErr
		}

		queue, qErr := adapters.NewTaskQueueAdapter(ctx, tasks.Config{
			ProjectID:           cfg.GCP.ProjectID,
			LocationID:          cfg.GCP.LocationID,
			QueueID:             cfg.Tasks.QueueID,
			WorkerURL:           taskURL,
			ServiceAccountEmail: cfg.Tasks.CallerServiceAccountEmail,
			Audience:            cfg.Tasks.TaskAudienceURL,
			DispatchDeadline:    cfg.Tasks.DispatchDeadline,
		})
		if qErr != nil {
			return nil, qErr
		}
		appCtx.TaskQueue = queue
		appCtx.Closers = append(appCtx.Closers, queue)
		resources = append(resources, queue)
	}

	// パイプラインを実行するのは Worker 面だけです。Web 面は組み立てないことで、
	// 未使用の Gemini クライアントと VOICEVOX エンジンへの接続を持たずに済みます。
	// VOICEVOX の初期化は /speakers を叩くため、公開側で組むと起動のたびに
	// エンジンのコールドスタートを踏むことにもなります。
	if cfg.Server.Role.ServesWorker() {
		p, err := buildPipeline(ctx, appCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to build pipeline: %w", err)
		}
		appCtx.Pipeline = p
	}

	return appCtx, nil
}

// buildNotifier は、通知機能を初期化します。
// webhookURL が未設定の場合、アダプター側が通知を行わない実装を返します。
func buildNotifier(httpClient httpkit.Poster, webhookURL, serviceURL string) (domain.Notifier, error) {
	if webhookURL == "" {
		slog.Info("Slack Webhook URL が未設定のため、通知は無効化されます。")
	}

	return adapters.NewSlackAdapter(httpClient, webhookURL, serviceURL)
}
