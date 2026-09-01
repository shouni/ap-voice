// Package adapters は、外部サービス（Gemini・Slack 等）との接続を実装します。
package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/ap-voice/internal/config"
)

const (
	// defaultVertexLocationID は Vertex AI のデフォルトロケーションです。
	defaultVertexLocationID = "global"
	// defaultInitialDelay リトライのデフォルトの遅延期間を指定します。
	defaultInitialDelay = 30 * time.Second
)

// NewAIAdapter は Vertex AI と通信するためのクライアントを初期化します。
func NewAIAdapter(ctx context.Context, gcp config.GCPConfig) (*gemini.Client, error) {
	if gcp.ProjectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT_ID is not set")
	}

	clientConfig := gemini.Config{
		InitialDelay: defaultInitialDelay,
		ProjectID:    gcp.ProjectID,
		LocationID:   defaultVertexLocationID,
	}

	aiClient, err := gemini.New(ctx, clientConfig)

	if err != nil {
		return nil, fmt.Errorf("AIクライアントの初期化に失敗しました: %w", err)
	}
	return aiClient, nil
}
