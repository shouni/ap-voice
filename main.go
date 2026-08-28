// ap-voice は、ドキュメントをナレーション音声へ変換するオーケストレータサービスです。
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

// logLevelEnvKey はログ出力レベルを指定する環境変数名です。
const logLevelEnvKey = "LOG_LEVEL"

func main() {
	// ロガーの設定（LOG_LEVEL 対応・Cloud Logging 互換の構造化ログ）。
	// 出力フォーマットは cloudlog、context 属性の付与は slogctx が担い、
	// 両者の組み立てだけをアプリ側で行う。
	level := slogctx.ParseLevel(os.Getenv(logLevelEnvKey))
	slog.SetDefault(slog.New(cloudlog.NewHandler(os.Stdout, level)))

	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run はアプリケーションの初期化とサーバー起動を行います。defer によるクリーンアップが
// os.Exit で無視されないよう、終了コードの決定は main 側に委ねます。
func run() error {
	// シグナルに反応するコンテキストの作成
	// これにより、SIGINT/SIGTERM受信時に ctx.Done() が閉じる
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 設定のロードとバリデーション
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Config load failed", "error", err)
		return err
	}
	if err := cfg.ValidateEssentialConfig(); err != nil {
		slog.Error("Config validation failed", "error", err)
		return err
	}

	// サーバーの実行
	if err := server.Run(ctx, cfg); err != nil {
		slog.Error("Application failed", "error", err)
		return err
	}
	return nil
}
