package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/gcp-kit/cloudrun"

	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/config"
)

// Run builds the server, starts it and manages its lifecycle.
func Run(ctx context.Context, cfg *config.Config) error {
	appCtx, err := builder.BuildContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to build application context: %w", err)
	}
	defer func() {
		slog.Info("♻️ Closing application context...")
		appCtx.Close()
	}()

	h, err := builder.BuildHandlers(appCtx)
	if err != nil {
		return fmt.Errorf("failed to build handlers: %w", err)
	}

	slog.InfoContext(ctx, "🚀 Server starting...",
		"port", cfg.Server.Port,
		"role", cfg.Server.Role,
		"service_url", cfg.Server.ServiceURL)

	// cloudrun owns startup, waiting for signals and graceful shutdown (forcing a close once the grace period runs out).
	return cloudrun.Serve(ctx, cloudrun.Config{
		Port:    cfg.Server.Port,
		Handler: NewRouter(h, cfg.GCP.ProjectID),
	})
}
