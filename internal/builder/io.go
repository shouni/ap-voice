package builder

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/gcs"
	"github.com/shouni/go-web-reader/pkg/reader"

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

	r, err := reader.New(
		reader.WithGCSFactory(func(ctx context.Context) (remoteio.ReadWriteFactory, error) {
			return factory, nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize content reader: %w", err)
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
