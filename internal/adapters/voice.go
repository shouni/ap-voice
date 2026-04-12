package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/builder"
	"github.com/shouni/go-voicevox/ports"
)

const (
	defaultMaxParallelSegments = 10
	defaultSegmentTimeout      = 180 * time.Second
	defaultSegmentRateLimit    = 1000 * time.Millisecond
)

// NewVoiceAdapter は、ports.EngineRunnerを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer) (ports.EngineRunner, error) {
	// 1. ctxの初期化
	engineRunner, err := builder.New(
		ctx,
		httpClient,
		writer,
		true,
		ports.WithMaxParallelSegments(defaultMaxParallelSegments),
		ports.WithSegmentTimeout(defaultSegmentTimeout),
		ports.WithSegmentRateLimit(defaultSegmentRateLimit),
	)

	if err != nil {
		return nil, fmt.Errorf("EngineRunnerの初期化に失敗しました: %w", err)
	}
	return engineRunner, nil
}
