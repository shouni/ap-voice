package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/config"
)

// defaultShutdownTimeout はデフォルトのシャットダウン猶予時間です。
const defaultShutdownTimeout = 30 * time.Second

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

	srv := &http.Server{
		Addr:              ":" + cfg.Server.Port,
		Handler:           NewRouter(h, cfg.GCP.ProjectID),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("🚀 Server starting...",
			"port", cfg.Server.Port,
			"role", cfg.Server.Role,
			"service_url", cfg.Server.ServiceURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	// シグナル処理は呼び出し元から渡されたコンテキストに委譲する
	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case <-ctx.Done():
		slog.Info("⚠️ Shutdown signal received via context, starting graceful shutdown...")
		return gracefulShutdown(srv, cfg.Server.ShutdownTimeout)
	}
}

// gracefulShutdown は、サーバーを安全に停止させます。
func gracefulShutdown(srv *http.Server, cfgTimeout time.Duration) error {
	timeout := cfgTimeout
	if timeout == 0 {
		timeout = defaultShutdownTimeout
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Graceful shutdown failed, forcing close", "error", err)
		if closeErr := srv.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("subsequent server close also failed: %w", closeErr))
		}
		return fmt.Errorf("graceful shutdown failed, server was forcibly closed: %w", err)
	}

	slog.Info("✅ Server gracefully stopped")
	return nil
}
