// Package runner は、ナレーションスクリプトの生成・整形・出力を実行します。
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
)

// PromptBuilder は、プロンプト文字列を生成する責務を定義します。
type PromptBuilder interface {
	Generate(mode, content string) (string, error)
}

// ContentReader は、指定されたURIからコンテンツを取得するためのインターフェースです。
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

// StructuredGenerator は、ResponseSchema による構造化出力に対応した生成インターフェースです。
//
// go-gemini-client の Generator と同じ形にしています。genai の型を含まないため、
// このパッケージは genai SDK を import せずに済み、モックも1メソッドで書けます。
type StructuredGenerator interface {
	GenerateWithAttachments(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error)
}

// GenerateRunner はスクリプト生成の実行に必要な依存とオプションを保持します。
type GenerateRunner struct {
	reader        ContentReader
	promptBuilder PromptBuilder
	aiClient      StructuredGenerator
	// defaultModel は Request がモデルを指定しなかったときに使うモデル名です。
	// GEMINI_MODELS の先頭で、起動時検証を通っているため必ず空ではありません。
	defaultModel string
}

// NewGenerateRunner は、依存関係を注入して GenerateRunner の新しいインスタンスを生成します。
func NewGenerateRunner(
	reader ContentReader,
	promptBuilder PromptBuilder,
	aiClient StructuredGenerator,
	defaultModel string,
) *GenerateRunner {
	return &GenerateRunner{
		reader:        reader,
		promptBuilder: promptBuilder,
		aiClient:      aiClient,
		defaultModel:  defaultModel,
	}
}

// modelFor は、リクエストが指定したモデル名を返します。
// 指定が無ければ既定モデルを使います。タスクのペイロードは呼び出し元が組み立てるため、
// モデル名が欠けることは十分にあり、そこで生成ごと失敗させる理由はありません。
func (gr *GenerateRunner) modelFor(req domain.Request) string {
	if model := strings.TrimSpace(req.AIModel); model != "" {
		return model
	}
	return gr.defaultModel
}

// Run は、入力ソースからコンテンツを読み込み、AIモデルを使用して構造化ナレーションスクリプトを生成する一連の処理を実行します。
func (gr *GenerateRunner) Run(ctx context.Context, req domain.Request) ([]domain.ScriptLine, error) {
	if req.InputURI == "" {
		return nil, errors.New("入力ソース(InputURI)が指定されていません")
	}
	content, err := gr.readContent(ctx, req.InputURI)
	if err != nil {
		return nil, err
	}
	model := gr.modelFor(req)
	slog.Info("処理開始", "mode", req.Mode, "model", model, "input_size", len(content))
	slog.Info("AIによるスクリプト生成を開始します...")

	prompt, err := gr.promptBuilder.Generate(req.Mode, content)
	if err != nil {
		return nil, err
	}

	generatedResponse, err := gr.aiClient.GenerateWithAttachments(ctx, model, prompt, nil, gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   scriptResponseSchema(),
	})
	if err != nil {
		return nil, fmt.Errorf("スクリプト生成に失敗しました: %w", err)
	}

	var lines []domain.ScriptLine
	if err := json.Unmarshal([]byte(generatedResponse.Text), &lines); err != nil {
		return nil, fmt.Errorf("AI応答のJSONデコードに失敗しました: %w", err)
	}
	slog.Info("AI スクリプト生成完了", "line_count", len(lines))

	return lines, nil
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
