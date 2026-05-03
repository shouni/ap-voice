package builder

import (
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"

	"ap-voice/internal/app"
)

// buildRemoteIO は、I/O コンポーネントを初期化します。
func buildRemoteIO(storage remoteio.IOFactory) (*app.RemoteIO, error) {
	if storage == nil {
		return &app.RemoteIO{
			Writer: remoteio.NewUniversalIOWriter(nil, nil),
		}, nil
	}

	w, err := storage.OutputWriter()
	if err != nil {
		return nil, fmt.Errorf("failed to create writer: %w", err)
	}
	signer, err := storage.URLSigner()
	if err != nil {
		return nil, fmt.Errorf("failed to create url signer: %w", err)
	}

	return &app.RemoteIO{
		Factory: storage,
		Writer:  w,
		Signer:  signer,
	}, nil
}
