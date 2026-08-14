package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"
	"github.com/shouni/go-voicevox/voicevox"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
)

// voicevoxWavContentType と voicevoxWavCacheControl は、Engine.Run が返す WAV バイト列を
// GCS へ保存する際に設定するメタデータです。go-voicevox はクラウドストレージに依存せず
// バイト列を返すだけなので、こうしたHTTP/CDN寄りの既定値は呼び出し側であるここで持つ。
const (
	voicevoxWavContentType  = "audio/wav"
	voicevoxWavCacheControl = "public, max-age=1800"
)

// VoiceAdapter は、音声合成する役割を担います。
type VoiceAdapter struct {
	engine voicevox.Engine
	writer remoteio.Writer
}

// NewVoiceAdapter は、VoiceAdapterを初期化します。
//
// 流量の設定は cfg が持ちます。エンジンの大きさで変わる値なので、ここに定数を
// 置かず env から受けます（config.VoicevoxConfig 参照）。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, cfg config.VoicevoxConfig, speakers *speaker.Registry, writer remoteio.Writer) (*VoiceAdapter, error) {
	engine, err := voicevox.New(
		ctx,
		httpClient,
		cfg.APIURL,
		true,
		speakers,
		voicevox.WithMaxParallelSegments(cfg.MaxParallelSegments),
		voicevox.WithSegmentRateLimit(cfg.SegmentRateLimit),
		voicevox.WithSegmentTimeout(cfg.SegmentTimeout),
	)

	if err != nil {
		return nil, fmt.Errorf("EngineRunnerの初期化に失敗しました: %w", err)
	}

	return &VoiceAdapter{
		engine: engine,
		writer: writer,
	}, nil
}

// UploadWav は、音声合成を実行し、結果のWAVを指定されたURIへ保存します。
func (a *VoiceAdapter) UploadWav(ctx context.Context, outputURI string, lines []domain.ScriptLine) error {
	wavBytes, err := a.engine.Run(ctx, toVoicevoxLines(lines))
	if err != nil {
		return fmt.Errorf("音声合成に失敗しました: %w", err)
	}

	return a.writer.Write(ctx, outputURI, bytes.NewReader(wavBytes),
		remoteio.WithContentType(voicevoxWavContentType),
		remoteio.WithInline(),
		remoteio.WithCacheControl(voicevoxWavCacheControl),
	)
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
			Speaker: line.Speaker,
			Style:   line.Style,
			Text:    line.Text,
		}
	}
	return out
}
