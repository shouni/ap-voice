package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-gemini-client/gemini"

	"prototypus-ai-doc-go/internal/config"
)

const (
	// defaultLocationID はデフォルトのロケーションIDです。
	defaultLocationID = "global"

	// defaultInitialDelay リトライのデフォルトの遅延期間を指定します。
	defaultInitialDelay = 30 * time.Second
)

// NewAIAdapter は Gemini APIと通信するためのクライアントを初期化します。
func NewAIAdapter(ctx context.Context, cfg *config.Config) (*gemini.Client, error) {
	clientConfig := gemini.Config{
		InitialDelay: defaultInitialDelay,
	}

	// GeminiAPIKeyが設定されている場合は優先して使用し、
	// 設定されていない場合はGCPのProjectIDを使用したVertex AI経由の認証を試みる。
	if cfg.GeminiAPIKey != "" {
		clientConfig.APIKey = cfg.GeminiAPIKey
	} else if cfg.ProjectID != "" {
		clientConfig.ProjectID = cfg.ProjectID
		clientConfig.LocationID = defaultLocationID
	} else {
		return nil, fmt.Errorf("GEMINI_API_KEY or GCP_PROJECT_ID is not set")
	}

	aiClient, err := gemini.NewClient(ctx, clientConfig)

	if err != nil {
		return nil, fmt.Errorf("AIクライアントの初期化に失敗しました: %w", err)
	}
	return aiClient, nil
}
