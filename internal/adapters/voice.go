package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"
	"github.com/shouni/go-voicevox/voicevox"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
)

// voicevoxWavContentType and voicevoxWavCacheControl are the metadata set when the WAV bytes Engine.Run
// returns are stored to GCS. go-voicevox does not depend on cloud storage and only returns bytes,
// so HTTP/CDN-oriented defaults like these belong here, on the calling side.
const (
	voicevoxWavContentType  = "audio/wav"
	voicevoxWavCacheControl = "public, max-age=1800"
)

// VoiceAdapter is responsible for synthesising audio.
type VoiceAdapter struct {
	engine voicevox.Engine
	writer remoteio.Writer
	layout domain.StorageLayout
}

// NewVoiceAdapter initialises a VoiceAdapter.
//
// Throughput settings come from cfg. They depend on the size of the engine, so instead of constants
// here they are taken from env (see config.VoicevoxConfig).
func NewVoiceAdapter(ctx context.Context, httpClient voicevox.Requester, cfg config.VoicevoxConfig, speakers *speaker.Registry, writer remoteio.Writer) (*VoiceAdapter, error) {
	// Options that affect reading come from readingOptions. Passing the same single list as the reading
	// preview keeps the reading that was checked from diverging from the reading that is synthesised.
	opts := append(readingOptions(),
		voicevox.WithMaxParallelSegments(cfg.MaxParallelSegments),
		voicevox.WithSegmentRateLimit(cfg.SegmentRateLimit),
		voicevox.WithSegmentTimeout(cfg.SegmentTimeout),
	)
	engine, err := voicevox.New(ctx, httpClient, cfg.APIURL, speakers, opts...)

	if err != nil {
		return nil, fmt.Errorf("failed to initialize the EngineRunner: %w", err)
	}

	return &VoiceAdapter{
		layout: domain.NewStorageLayout(),
		engine: engine,
		writer: writer,
	}, nil
}

// UploadWav runs synthesis and stores the resulting WAV at the given URI.
func (a *VoiceAdapter) UploadWav(ctx context.Context, outputURI string, lines []domain.ScriptLine) error {
	wavBytes, err := a.engine.Run(ctx, toVoicevoxLines(lines))
	if err != nil {
		return fmt.Errorf("speech synthesis failed: %w", err)
	}

	return a.writer.Write(ctx, outputURI, bytes.NewReader(wavBytes),
		remoteio.WithContentType(voicevoxWavContentType),
		remoteio.WithInline(),
		remoteio.WithCacheControl(voicevoxWavCacheControl),
	)
}

// UploadScript stores the script at the location derived from the audio's output URI.
//
// domain.StorageLayout owns the derivation. Rewriting the extension here would duplicate
// the rule the reader (repository) uses.
func (a *VoiceAdapter) UploadScript(ctx context.Context, outputURI string, script domain.Script) error {
	jsonPath := a.layout.ScriptURIFor(outputURI)

	body, err := json.MarshalIndent(script, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the script as JSON: %w", err)
	}

	return a.writer.Write(ctx, jsonPath, bytes.NewReader(body), remoteio.WithContentType("application/json; charset=utf-8"))
}

// toVoicevoxLines converts domain ScriptLines to go-voicevox ScriptLines.
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
