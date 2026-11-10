// Package adapters implements the connections to external services (Gemini, Slack and so on).
package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/ap-voice/internal/config"
)

const (
	// defaultVertexLocationID is the default Vertex AI location.
	defaultVertexLocationID = "global"
	// defaultInitialDelay is the default initial delay between retries.
	defaultInitialDelay = 30 * time.Second
)

// NewAIAdapter initialises the client that talks to Vertex AI.
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
		return nil, fmt.Errorf("failed to initialize the AI client: %w", err)
	}
	return aiClient, nil
}
