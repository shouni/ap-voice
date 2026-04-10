package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/shouni/go-http-kit/httpkit"

	"ap-voice/internal/app"
	"ap-voice/internal/config"
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

	rio, err := buildRemoteIO(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IO components: %w", err)
	}
	resources = append(resources, rio)

	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = config.DefaultHTTPTimeout
	}

	httpClient := httpkit.New(
		timeout,
		httpkit.WithMaxRetries(1),
		httpkit.WithSkipNetworkValidation(true),
	)

	appCtx := &app.Container{
		Config:     cfg,
		RemoteIO:   rio,
		HTTPClient: httpClient,
	}

	p, err := buildPipeline(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build pipeline: %w", err)
	}
	appCtx.Pipeline = p

	return appCtx, nil
}
