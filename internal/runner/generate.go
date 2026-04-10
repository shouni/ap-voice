package runner

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"ap-voice/internal/config"
	"ap-voice/internal/domain"

	"github.com/shouni/go-gemini-client/gemini"
)

// TemplateData はプロンプトテンプレートに渡すデータ構造です。
type TemplateData struct {
	InputText string
}

// ContentReader は、指定されたURIからコンテンツを取得するためのインターフェースです。
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
	io.Closer
}

// GenerateRunner は generate コマンドの実行に必要な依存とオプションを保持します。
type GenerateRunner struct {
	options       *config.Config
	reader        ContentReader
	promptBuilder domain.PromptBuilder
	aiClient      gemini.ContentGenerator
}

// NewGenerateRunner は、依存関係を注入して GenerateRunner の新しいインスタンスを生成します。
func NewGenerateRunner(
	options *config.Config,
	reader ContentReader,
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
	if gr.options.URL == "" {
		return "", fmt.Errorf("入力ソース(--url)が指定されていません")
	}
	inputContent, err := gr.readContent(ctx, gr.options.URL)
	if err != nil {
		return "", err
	}
	slog.Info("処理開始", "mode", gr.options.Mode, "model", gr.options.AIModel, "input_size", len(inputContent))
	slog.Info("AIによるスクリプト生成を開始します...")

	data := TemplateData{
		InputText: inputContent,
	}
	promptContent, err := gr.promptBuilder.Build(gr.options.Mode, data)
	if err != nil {
		return "", err
	}

	generatedResponse, err := gr.aiClient.GenerateContent(ctx, gr.options.AIModel, promptContent)
	if err != nil {
		return "", fmt.Errorf("スクリプト生成に失敗しました: %w", err)
	}
	slog.Info("AI スクリプト生成完了", "script_length", len(generatedResponse.Text))

	return generatedResponse.Text, nil
}

func (gr *GenerateRunner) readContent(ctx context.Context, sourceURL string) (string, error) {
	stream, err := gr.reader.Open(ctx, sourceURL)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		_ = stream.Close()
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("failed to consume source: %w", err)
	}

	return strings.TrimSpace(string(body)), nil
}
