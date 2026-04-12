package adapters

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
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
	engine ports.Engine
	writer remoteio.Writer
}

// NewVoiceAdapter は、VoiceAdapterを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer) (*VoiceAdapter, error) {
	engine, err := builder.NewEngine(
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
		writer: writer,
	}, nil
}

// UploadWav は、音声合成を実行します。
func (a *VoiceAdapter) UploadWav(ctx context.Context, outputURI, content string) error {
	return a.engine.Run(ctx, outputURI, content)
}

// UploadScript は指定されたURIの拡張子を.txtに変更してスクリプトをアップロードします。
func (a *VoiceAdapter) UploadScript(ctx context.Context, outputURI string, content string) error {
	ext := filepath.Ext(outputURI)
	txtPath := strings.TrimSuffix(outputURI, ext) + ".txt"
	contentReader := strings.NewReader(content)

	return a.writer.Write(ctx, txtPath, contentReader, "text/plain; charset=utf-8")
}
