package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/gcp-kit/cloudrun"

	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/config"
)

// Run はサーバーの構築、起動、およびライフサイクル管理を行います。
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

	// 起動・シグナル待ち・正常停止（猶予を超えたら強制クローズ）は cloudrun が持ちます。
	return cloudrun.Serve(ctx, cloudrun.Config{
		Port:    cfg.Server.Port,
		Handler: NewRouter(h, cfg.GCP.ProjectID),
	})
}
