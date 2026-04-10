package adapters

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/voicevox"
)

// NewVoiceAdapter は、voicevox Executorを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer, voicevoxOutput string) (voicevox.EngineExecutor, error) {
	if voicevoxOutput == "" {
		slog.Info("voicevoxの出力先が未指定のため、エンジンエクゼキュータをスキップします。")
		return nil, nil
	}

	executor, err := voicevox.NewEngineExecutor(ctx, httpClient, writer, true)
	if err != nil {
		return nil, fmt.Errorf("voicevoxエンジンエクゼキュータの初期化に失敗しました: %w", err)
	}
	return executor, nil
}
