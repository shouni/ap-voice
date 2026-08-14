package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"
	"github.com/shouni/go-voicevox/voicevox"

	"github.com/shouni/ap-voice/internal/domain"
)

// voicevoxWavContentType と voicevoxWavCacheControl は、Engine.Run が返す WAV バイト列を
// GCS へ保存する際に設定するメタデータです。go-voicevox はクラウドストレージに依存せず
// バイト列を返すだけなので、こうしたHTTP/CDN寄りの既定値は呼び出し側であるここで持つ。
const (
	voicevoxWavContentType  = "audio/wav"
	voicevoxWavCacheControl = "public, max-age=1800"
)

// 合成の流量に関わる3つの値。**スループットを決めているのはレート制限**で、
// 並列数は「同時に処理待ちで居られる上限」です。
//
//	スループット = min(1/レート制限, 並列数 ÷ 1セグメントの所要時間)
//
// Cloud Run の max_instance_request_concurrency（= 1）とは別の軸です。あちらは
// 1インスタンスが同時に受けるジョブ数で、ここは1ジョブ内のセグメント数です。
const (
	// defaultMaxParallelSegments は 1 ジョブ内で同時に投げるセグメント数です。
	//
	// 下げるとレート制限に届かなくなります（1セグメント4秒なら 8÷4 = 秒2件で
	// ちょうど釣り合う）。一方でエンジンは 4 vCPU なので、増やしても総スループットは
	// 変わらず待ち行列が伸びるだけです。**効いてくるとすればエンジン側のメモリ**で、
	// 同時に抱える合成の数だけバッファが積まれます。OOM が出たらここを 4（エンジンの
	// vCPU 数）へ下げるのが最初の一手です。
	defaultMaxParallelSegments = 8
	// defaultSegmentRateLimit はセグメントの投入間隔です（秒2件）。
	//
	// **VOICEVOX に API のレート制限はありません。** 自前で立てたエンジンで、
	// サイドカー構成では同一インスタンス内にいます。外部仕様への準拠ではなく、
	// エンジンを叩きすぎないための自主的な絞りです。
	defaultSegmentRateLimit = 500 * time.Millisecond
	// defaultSegmentTimeout はセグメント1件あたりの上限です。
	// サイドカーは起動時から待ち受けているため、コールドスタート分の余裕は不要です。
	defaultSegmentTimeout = 120 * time.Second
)

// VoiceAdapter は、音声合成する役割を担います。
type VoiceAdapter struct {
	engine voicevox.Engine
	writer remoteio.Writer
}

// NewVoiceAdapter は、VoiceAdapterを初期化します。
//
// apiURL には VOICEVOX エンジンの URL を渡します。空文字の場合は go-voicevox が
// http://localhost:50021 へ落とすため、ローカル実行とサイドカー構成のどちらでも
// そのまま動きます。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, apiURL string, speakers *speaker.Registry, writer remoteio.Writer) (*VoiceAdapter, error) {
	engine, err := voicevox.New(
		ctx,
		httpClient,
		apiURL,
		true,
		speakers,
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
