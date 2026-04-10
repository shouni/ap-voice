package builder

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-remote-io/remoteio/gcs"

	"ap-voice/internal/app"
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

	r, err := factory.Reader()
	if err != nil {
		return nil, fmt.Errorf("failed to create input reader: %w", err)
	}
	w, err := factory.Writer()
	if err != nil {
		return nil, fmt.Errorf("failed to create output writer: %w", err)
	}

	return &app.RemoteIO{
		Factory: factory,
		Reader:  r,
		Writer:  w,
	}, nil
}
