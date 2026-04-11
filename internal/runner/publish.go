package runner

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/voicevox"
)

// PublishRunner は、スクリプトの公開処理を実行する具象構造体です。
type PublishRunner struct {
	voicevoxExecutor voicevox.EngineExecutor
	writer           remoteio.Writer
}

// NewPublishRunner は PublishRunner の新しいインスタンスを作成します。
func NewPublishRunner(voicevoxExecutor voicevox.EngineExecutor, writer remoteio.Writer) *PublishRunner {
	return &PublishRunner{
		voicevoxExecutor: voicevoxExecutor,
		writer:           writer,
	}
}

// Run は公開処理のパイプライン全体を実行します。
func (r *PublishRunner) Run(ctx context.Context, outputURI string, content string) error {
	if outputURI == "" {
		return fmt.Errorf("出力先パス(--output)が指定されていません")
	}
	return r.publishAudioAndScript(ctx, outputURI, content)
}

// publishAudioAndScript は音声合成とスクリプトのアップロードを実行します。
func (r *PublishRunner) publishAudioAndScript(ctx context.Context, outputURI, content string) error {
	slog.InfoContext(ctx, "VOICEVOXによる音声合成を開始します。", "output_path", outputURI)
	if err := r.voicevoxExecutor.Execute(ctx, content, outputURI); err != nil {
		return fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "音声合成が完了しました。", "output_path", outputURI)

	// スクリプトのアップロード
	ext := filepath.Ext(outputURI)
	txtPath := strings.TrimSuffix(outputURI, ext) + ".txt"
	contentReader := strings.NewReader(content)

	slog.InfoContext(ctx, "スクリプトのアップロードを開始します。", "upload_path", txtPath)
	if err := r.writer.Write(ctx, txtPath, contentReader, "text/plain; charset=utf-8"); err != nil {
		return fmt.Errorf("スクリプトのアップロードに失敗しました (%s): %w", txtPath, err)
	}
	slog.InfoContext(ctx, "スクリプトのアップロードが完了しました。", "upload_path", txtPath)

	return nil
}
