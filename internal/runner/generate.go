package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"

	"ap-voice/internal/config"
	"ap-voice/internal/domain"
)

// GenerateRunner は generate コマンドの実行に必要な依存とオプションを保持します。
type GenerateRunner struct {
	options       *config.Config
	reader        domain.ContentReader
	promptBuilder domain.PromptBuilder
	aiClient      gemini.ContentGenerator
}

// NewGenerateRunner は、依存関係を注入して GenerateRunner の新しいインスタンスを生成します。
func NewGenerateRunner(
	options *config.Config,
	reader domain.ContentReader,
	promptBuilder domain.PromptBuilder,
	aiClient gemini.ContentGenerator,
) *GenerateRunner {
	return &GenerateRunner{
		options:       options,
		reader:        reader,
		promptBuilder: promptBuilder,
		aiClient:      aiClient,
	}
}

// Run は、入力ソースからコンテンツを読み込み、AIモデルを使用してナレーションスクリプトを生成する一連の処理を実行します。
func (gr *GenerateRunner) Run(ctx context.Context) (string, error) {
	if gr.options.InputFile == "" {
		return "", fmt.Errorf("入力ソース(--url)が指定されていません")
	}
	content, err := gr.readContent(ctx, gr.options.InputFile)
	if err != nil {
		return "", err
	}
	slog.Info("処理開始", "mode", gr.options.Mode, "model", gr.options.AIModel, "input_size", len(content))
	slog.Info("AIによるスクリプト生成を開始します...")

	prompt, err := gr.promptBuilder.Generate(gr.options.Mode, content)
	if err != nil {
		return "", err
	}

	generatedResponse, err := gr.aiClient.GenerateContent(ctx, gr.options.AIModel, prompt)
	if err != nil {
		return "", fmt.Errorf("スクリプト生成に失敗しました: %w", err)
	}
	slog.Info("AI スクリプト生成完了", "script_length", len(generatedResponse.Text))

	return generatedResponse.Text, nil
}

// readContent は、指定されたソースURLからコンテンツを取得します。
func (gr *GenerateRunner) readContent(ctx context.Context, sourceURL string) (string, error) {
	stream, err := gr.reader.Open(ctx, sourceURL)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			slog.WarnContext(ctx, "ストリームのクローズに失敗しました", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("コンテンツの読み込みに失敗しました: %w", err)
	}

	trimmedContent := strings.TrimSpace(string(body))
	if len(trimmedContent) < config.MinInputContentLength {
		return "", fmt.Errorf("入力されたコンテンツが短すぎます")
	}
	return trimmedContent, nil
}
