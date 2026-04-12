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
	// defaultMaxParallelSegments はVoicevoxエンジンの負荷テスト結果に基づき8に設定
	defaultMaxParallelSegments = 8
	// defaultSegmentRateLimit はAPIのレートリミット仕様に準拠
	defaultSegmentRateLimit = 500 * time.Millisecond
	defaultSegmentTimeout   = 120 * time.Second
)

// VoiceAdapter は、音声合成する役割を担います。
type VoiceAdapter struct {
	engine ports.EngineRunner
}

// NewVoiceAdapter は、VoiceAdapterを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer) (*VoiceAdapter, error) {
	engine, err := builder.New(
		ctx,
		httpClient,
		writer,
		true,
		ports.WithMaxParallelSegments(defaultMaxParallelSegments),
		ports.WithSegmentRateLimit(defaultSegmentRateLimit),
		ports.WithSegmentTimeout(defaultSegmentTimeout),
	)

	if err != nil {
		return nil, fmt.Errorf("EngineRunnerの初期化に失敗しました: %w", err)
	}

	return &VoiceAdapter{
		engine: engine,
	}, nil
}

// Run は、音声合成を実行します。
func (a *VoiceAdapter) Run(ctx context.Context, outputURI, content string) error {
	return a.engine.Run(ctx, outputURI, content)
}
