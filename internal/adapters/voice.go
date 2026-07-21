package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/voicevox"

	"ap-voice/internal/domain"
)

// voicevoxWriter は、go-remote-io の remoteio.Writer を go-voicevox の voicevox.Writer に適合させます。
// go-voicevox 自体はクラウドストレージ等の外部I/Oライブラリに依存しないため、
// GCS 等への出力が必要な呼び出し側でこの変換を担う。
type voicevoxWriter struct {
	writer remoteio.Writer
}

func (w voicevoxWriter) Write(ctx context.Context, path string, contentReader io.Reader, opts ...voicevox.WriteOption) error {
	cfg := voicevox.NewWriteConfig(opts...)
	remoteOpts := make([]remoteio.WriteOption, 0, 3)
	if cfg.ContentType != "" {
		remoteOpts = append(remoteOpts, remoteio.WithContentType(cfg.ContentType))
	}
	if cfg.Inline {
		remoteOpts = append(remoteOpts, remoteio.WithInline())
	}
	if cfg.CacheControl != "" {
		remoteOpts = append(remoteOpts, remoteio.WithCacheControl(cfg.CacheControl))
	}

	return w.writer.Write(ctx, path, contentReader, remoteOpts...)
}

const (
	// defaultMaxParallelSegments はVoicevoxエンジンの負荷テスト結果に基づき8に設定
	defaultMaxParallelSegments = 8
	// defaultSegmentRateLimit はAPIのレートリミット仕様に準拠
	defaultSegmentRateLimit = 500 * time.Millisecond
	defaultSegmentTimeout   = 120 * time.Second
)

// VoiceAdapter は、音声合成する役割を担います。
type VoiceAdapter struct {
	engine voicevox.Engine
	writer remoteio.Writer
}

// NewVoiceAdapter は、VoiceAdapterを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer) (*VoiceAdapter, error) {
	engine, err := voicevox.New(
		ctx,
		httpClient,
		voicevoxWriter{writer: writer},
		"",
		true,
		voicevox.WithMaxParallelSegments(defaultMaxParallelSegments),
		voicevox.WithSegmentRateLimit(defaultSegmentRateLimit),
		voicevox.WithSegmentTimeout(defaultSegmentTimeout),
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
func (a *VoiceAdapter) UploadWav(ctx context.Context, outputURI string, lines []domain.ScriptLine) error {
	return a.engine.RunScript(ctx, outputURI, toVoicevoxLines(lines))
}

// UploadScript は指定されたURIの拡張子を.jsonに変更してスクリプトをアップロードします。
func (a *VoiceAdapter) UploadScript(ctx context.Context, outputURI string, lines []domain.ScriptLine) error {
	ext := filepath.Ext(outputURI)
	jsonPath := strings.TrimSuffix(outputURI, ext) + ".json"

	body, err := json.MarshalIndent(lines, "", "  ")
	if err != nil {
		return fmt.Errorf("スクリプトのJSONエンコードに失敗しました: %w", err)
	}

	return a.writer.Write(ctx, jsonPath, bytes.NewReader(body), remoteio.WithContentType("application/json; charset=utf-8"))
}

// toVoicevoxLines は、ドメイン層の ScriptLine を go-voicevox の ScriptLine に変換します。
func toVoicevoxLines(lines []domain.ScriptLine) []voicevox.ScriptLine {
	out := make([]voicevox.ScriptLine, len(lines))
	for i, line := range lines {
		out[i] = voicevox.ScriptLine{
			Speaker:   line.Speaker,
			Style:     line.Style,
			Direction: line.Direction,
			Text:      line.Text,
		}
	}
	return out
}
