package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"ap-voice/internal/domain"
)

// PublishRunner は、スクリプトの公開処理を実行する具象構造体です。
type PublishRunner struct {
	voice domain.Voice
}

// NewPublishRunner は PublishRunner の新しいインスタンスを作成します。
func NewPublishRunner(voice domain.Voice) *PublishRunner {
	return &PublishRunner{
		voice: voice,
	}
}

// Run は公開処理のパイプライン全体を実行します。
func (r *PublishRunner) Run(ctx context.Context, outputURI string, content string) error {
	if outputURI == "" {
		return errors.New("出力先パス(outputURI)が指定されていません")
	}

	slog.InfoContext(ctx, "スクリプトのアップロードを開始します。")
	if err := r.voice.UploadScript(ctx, outputURI, content); err != nil {
		return fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "スクリプトのアップロードが完了しました。")

	slog.InfoContext(ctx, "音声合成を開始します。", "output_path", outputURI)
	if err := r.voice.UploadWav(ctx, outputURI, content); err != nil {
		return fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "音声合成が完了しました。", "output_path", outputURI)

	return nil
}
