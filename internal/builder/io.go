package builder

import (
	"context"
	"fmt"
	"log/slog"

	"ap-voice/internal/app"

	"github.com/shouni/go-remote-io/remoteio/gcs"
)

// buildRemoteIO は、GCS ベースの I/O コンポーネントを初期化します。
func buildRemoteIO(ctx context.Context) (*app.RemoteIO, error) {
	factory, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS factory: %w", err)
	}

	defer func() {
		if err != nil {
			if closeErr := factory.Close(); closeErr != nil {
				slog.Warn("failed to close GCS factory during cleanup", "error", closeErr)
			}
		}
	}()

	w, err := factory.Writer()
	if err != nil {
		return nil, fmt.Errorf("failed to create writer: %w", err)
	}

	return &app.RemoteIO{
		Factory: factory,
		Writer:  w,
	}, nil
}
