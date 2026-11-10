// ap-voice is an orchestrator service that turns documents into narration audio.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/go-utils/slogctx"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/server"
)

// logLevelEnvKey is the name of the environment variable that sets the log level.
const logLevelEnvKey = "LOG_LEVEL"

func main() {
	// Set up the logger (honours LOG_LEVEL; structured logs compatible with Cloud Logging).
	// cloudlog owns the output format and slogctx attaches context attributes;
	// the app only puts the two together.
	level := slogctx.ParseLevel(os.Getenv(logLevelEnvKey))
	slog.SetDefault(slog.New(cloudlog.NewHandler(os.Stdout, level)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run initialises the application and starts the server. The exit code is left to main
// so that the deferred cleanups are not skipped by os.Exit.
func run() error {
	// Create a context that reacts to signals.
	// With it, ctx.Done() closes on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Load and validate the configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Config load failed", "error", err)
		return err
	}
	if err := cfg.ValidateEssentialConfig(); err != nil {
		slog.Error("Config validation failed", "error", err)
		return err
	}

	// Run the server
	if err := server.Run(ctx, cfg); err != nil {
		slog.Error("Application failed", "error", err)
		return err
	}
	return nil
}
