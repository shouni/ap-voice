package runner

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/voicevox"

	"ap-voice/internal/domain"
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
func (pr *PublishRunner) Run(ctx context.Context, req domain.Request) error {
	if req.OutputURI == "" {
		return fmt.Errorf("出力先パス(--output)が指定されていません")
	}
	return pr.publishAudioAndScript(ctx, req.OutputURI, req.Context)
}

// publishAudioAndScript は音声合成とスクリプトのアップロードを実行します。
func (pr *PublishRunner) publishAudioAndScript(ctx context.Context, outputURI, content string) error {
	slog.InfoContext(ctx, "VOICEVOXによる音声合成を開始します。", "output_path", outputURI)
	if err := pr.voicevoxExecutor.Execute(ctx, content, outputURI); err != nil {
		return fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "音声合成が完了しました。", "output_path", outputURI)

	// スクリプトのアップロード
	ext := filepath.Ext(outputURI)
	txtPath := strings.TrimSuffix(outputURI, ext) + ".txt"
	contentReader := strings.NewReader(content)

	slog.InfoContext(ctx, "スクリプトのアップロードを開始します。", "upload_path", txtPath)
	if err := pr.writer.Write(ctx, txtPath, contentReader, "text/plain; charset=utf-8"); err != nil {
		return fmt.Errorf("スクリプトのアップロードに失敗しました (%s): %w", txtPath, err)
	}
	slog.InfoContext(ctx, "スクリプトのアップロードが完了しました。", "upload_path", txtPath)

	return nil
}
