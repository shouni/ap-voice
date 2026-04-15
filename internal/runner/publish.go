package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ap-voice/internal/domain"

	"github.com/shouni/go-remote-io/remoteio"
)

// PublishRunner は、スクリプトの公開処理を実行する具象構造体です。
type PublishRunner struct {
	voice  domain.Voice
	signer remoteio.URLSigner
}

// NewPublishRunner は PublishRunner の新しいインスタンスを作成します。
func NewPublishRunner(voice domain.Voice, signer remoteio.URLSigner) *PublishRunner {
	return &PublishRunner{
		voice:  voice,
		signer: signer,
	}
}

// Run は公開処理のパイプライン全体を実行します。
func (r *PublishRunner) Run(ctx context.Context, outputURI string, content string) (string, error) {
	if outputURI == "" {
		return "", errors.New("出力先パス(outputURI)が指定されていません")
	}

	slog.InfoContext(ctx, "音声合成を開始します。", "output_path", outputURI)
	if err := r.voice.UploadWav(ctx, outputURI, content); err != nil {
		return "", fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "音声合成が完了しました。", "output_path", outputURI)

	slog.InfoContext(ctx, "スクリプトのアップロードを開始します。", "output_path", outputURI)
	if err := r.voice.UploadScript(ctx, outputURI, content); err != nil {
		return "", fmt.Errorf("スクリプトのアップロードに失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "スクリプトのアップロードが完了しました。", "output_path", outputURI)

	publicURL, err := r.buildPublicURL(ctx, outputURI)
	if err != nil {
		slog.WarnContext(ctx, "公開URLの生成に失敗したため、URLなしで通知を続行します。", "output_path", outputURI, "error", err)
		return "", nil
	}

	return publicURL, nil
}

func (r *PublishRunner) buildPublicURL(ctx context.Context, outputURI string) (string, error) {
	if r.signer == nil {
		return "", nil
	}
	return r.signer.GenerateSignedURL(ctx, outputURI, "GET", time.Hour)
}
