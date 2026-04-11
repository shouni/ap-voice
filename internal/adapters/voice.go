package adapters

import (
	"context"
	"fmt"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/voicevox"
)

// NewVoiceAdapter は、voicevox Executorを初期化します。
func NewVoiceAdapter(ctx context.Context, httpClient httpkit.Requester, writer remoteio.Writer) (voicevox.EngineExecutor, error) {
	executor, err := voicevox.NewEngineExecutor(ctx, httpClient, writer, true)
	if err != nil {
		return nil, fmt.Errorf("voicevoxエンジンエクゼキュータの初期化に失敗しました: %w", err)
	}
	return executor, nil
}
