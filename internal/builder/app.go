package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/gcs"

	"ap-voice/internal/adapters"
	"ap-voice/internal/app"
	"ap-voice/internal/config"
	"ap-voice/internal/domain"
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
	var storage *gcs.GCSClientFactory
	if requiresGCS(cfg) {
		storage, err = gcs.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create GCS factory: %w", err)
		}
		resources = append(resources, storage)
	}
	rio, err := buildRemoteIO(storage)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IO components: %w", err)
	}

	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = config.DefaultHTTPTimeout
	}

	httpClient := httpkit.New(
		timeout,
		httpkit.WithMaxRetries(1),
		httpkit.WithSkipNetworkValidation(true),
	)

	// 3. Notifier
	notifier, err := buildNotifier(httpClient, cfg.SlackWebhookURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize notifier: %w", err)
	}

	appCtx := &app.Container{
		Config:     cfg,
		RemoteIO:   rio,
		HTTPClient: httpClient,
		Notifier:   notifier,
	}

	p, err := buildPipeline(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to build pipeline: %w", err)
	}
	appCtx.Pipeline = p

	return appCtx, nil
}

// buildNotifier は、通知機能を初期化します。
func buildNotifier(httpClient httpkit.Requester, webhookURL string) (domain.Notifier, error) {
	if webhookURL == "" {
		slog.Info("Slack Webhook URL が未設定のため、通知は無効化されます。")
		return &adapters.NoopNotifier{}, nil
	}

	return adapters.NewSlackAdapter(httpClient, webhookURL)
}

func requiresGCS(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return remoteio.IsGCSURI(cfg.InputFile) || remoteio.IsGCSURI(cfg.OutputFile)
}
