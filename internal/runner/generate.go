package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"

	"ap-voice/internal/config"
	"ap-voice/internal/domain"
)

// PromptBuilder は、プロンプト文字列を生成する責務を定義します。
type PromptBuilder interface {
	Generate(mode, content string) (string, error)
}

// ContentReader は、指定されたURIからコンテンツを取得するためのインターフェースです。
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

// GenerateRunner は generate コマンドの実行に必要な依存とオプションを保持します。
type GenerateRunner struct {
	reader        ContentReader
	promptBuilder PromptBuilder
	aiClient      gemini.ContentGenerator
}

// NewGenerateRunner は、依存関係を注入して GenerateRunner の新しいインスタンスを生成します。
func NewGenerateRunner(
	reader ContentReader,
	promptBuilder PromptBuilder,
	aiClient gemini.ContentGenerator,
) *GenerateRunner {
	return &GenerateRunner{
		reader:        reader,
		promptBuilder: promptBuilder,
		aiClient:      aiClient,
	}
}

// Run は、入力ソースからコンテンツを読み込み、AIモデルを使用してナレーションスクリプトを生成する一連の処理を実行します。
func (gr *GenerateRunner) Run(ctx context.Context, req domain.Request) (string, error) {
	if req.InputURI == "" {
		return "", errors.New("入力ソース(InputURI)が指定されていません")
	}
	content, err := gr.readContent(ctx, req.InputURI)
	if err != nil {
		return "", err
	}
	slog.Info("処理開始", "mode", req.Mode, "model", req.AIModel, "input_size", len(content))
	slog.Info("AIによるスクリプト生成を開始します...")

	prompt, err := gr.promptBuilder.Generate(req.Mode, content)
	if err != nil {
		return "", err
	}

	generatedResponse, err := gr.aiClient.GenerateContent(ctx, req.AIModel, prompt)
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
