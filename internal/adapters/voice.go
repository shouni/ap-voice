package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/voicevox"
)

const (
	defaultMaxParallelSegments = 5
	defaultSegmentTimeout      = 180 * time.Second
	defaultSegmentRateLimit    = 1000 * time.Millisecond
)

// NewVoiceAdapter は、voicevox Executorを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer) (voicevox.EngineExecutor, error) {
	// 1. Executorの初期化
	executor, err := voicevox.NewEngineExecutor(
		ctx,
		httpClient,
		writer,
		true,
		voicevox.WithMaxParallelSegments(defaultMaxParallelSegments),
		voicevox.WithSegmentTimeout(defaultSegmentTimeout),
		voicevox.WithSegmentRateLimit(defaultSegmentRateLimit),
	)

	if err != nil {
		return nil, fmt.Errorf("voicevoxエンジンエクゼキュータの初期化に失敗しました: %w", err)
	}
	return executor, nil
}
