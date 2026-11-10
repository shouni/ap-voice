package adapters

import (
	"context"
	"fmt"

	"github.com/shouni/gcp-kit/tasks"

	"github.com/shouni/ap-voice/internal/domain"
)

// TaskQueueAdapter enqueues runs to Cloud Tasks.
type TaskQueueAdapter struct {
	enqueuer *tasks.Enqueuer[domain.Request]
}

// NewTaskQueueAdapter builds the Cloud Tasks Enqueuer.
//
// It is Cloud Tasks, not this process, that mints and attaches the OIDC token.
// cfg.ServiceAccountEmail says "issue it as this SA".
func NewTaskQueueAdapter(ctx context.Context, cfg tasks.Config) (*TaskQueueAdapter, error) {
	enqueuer, err := tasks.NewEnqueuer[domain.Request](ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the task queue: %w", err)
	}
	return &TaskQueueAdapter{enqueuer: enqueuer}, nil
}

// Enqueue hands the run over to the worker side.
func (a *TaskQueueAdapter) Enqueue(ctx context.Context, req domain.Request) error {
	if err := a.enqueuer.Enqueue(ctx, req); err != nil {
		return fmt.Errorf("failed to enqueue the task: %w", err)
	}
	return nil
}

// Close releases the Cloud Tasks client.
func (a *TaskQueueAdapter) Close() error {
	return a.enqueuer.Close()
}
