package adapters

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/ap-voice/internal/domain"
)

// TaskQueueAdapter は Cloud Tasks へ実行を投入します。
type TaskQueueAdapter struct {
	enqueuer *tasks.Enqueuer[domain.Request]
}

// NewTaskQueueAdapter は、Cloud Tasks の Enqueuer を組み立てます。
//
// OIDC トークンを生成して付与するのは Cloud Tasks であり、このプロセスではありません。
// cfg.ServiceAccountEmail は「この SA として発行せよ」という指定です。
func NewTaskQueueAdapter(ctx context.Context, cfg tasks.Config) (*TaskQueueAdapter, error) {
	enqueuer, err := tasks.NewEnqueuer[domain.Request](ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("タスクキューの初期化に失敗しました: %w", err)
	}
	return &TaskQueueAdapter{enqueuer: enqueuer}, nil
}

// Enqueue は、実行を Worker 面へ引き渡します。
func (a *TaskQueueAdapter) Enqueue(ctx context.Context, req domain.Request) error {
	if err := a.enqueuer.Enqueue(ctx, req); err != nil {
		return fmt.Errorf("タスクの投入に失敗しました: %w", err)
	}
	return nil
}

// Close は Cloud Tasks クライアントを解放します。
func (a *TaskQueueAdapter) Close() error {
	return a.enqueuer.Close()
}
