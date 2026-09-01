package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/internal/domain"
)

// minInputContentLength は、生成に進める入力テキストの最小長です。
//
// config ではなく、使う段が持ちます。デプロイ先が決める値ではない（env もありません）
// うえ、この 1 つのために pipeline が config を import していました。これより短い
// 入力からは、何を読ませたいのかが決まりません。
const minInputContentLength = 10

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
// genai-kit の gemini.Generator と同じ形にしています。genai の型を含まないため、
// このパッケージは genai SDK を import せずに済み、モックも1メソッドで書けます。
type StructuredGenerator interface {
	Generate(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error)
}

// ScriptStep はスクリプト生成の実行に必要な依存とオプションを保持します。
type ScriptStep struct {
	reader        ContentReader
	promptBuilder PromptBuilder
	aiClient      StructuredGenerator
	// defaultModel は Request がモデルを指定しなかったときに使うモデル名です。
	// GEMINI_MODELS の先頭で、起動時検証を通っているため必ず空ではありません。
	defaultModel string
	// schema は話者一覧から組んだレスポンススキーマです。実行のたびに組み直す理由が
	// ないため、構築時に1度だけ作ります。
	schema *gemini.Schema
}

// NewScriptStep は、依存関係を注入して ScriptStep の新しいインスタンスを生成します。
func NewScriptStep(
	reader ContentReader,
	promptBuilder PromptBuilder,
	aiClient StructuredGenerator,
	defaultModel string,
	speakers *speaker.Registry,
) *ScriptStep {
	return &ScriptStep{
		reader:        reader,
		promptBuilder: promptBuilder,
		aiClient:      aiClient,
		defaultModel:  defaultModel,
		schema:        scriptResponseSchema(speakers),
	}
}

// modelFor は、リクエストが指定したモデル名を返します。
// 指定が無ければ既定モデルを使います。タスクのペイロードは呼び出し元が組み立てるため、
// モデル名が欠けることは十分にあり、そこで生成ごと失敗させる理由はありません。
func (gr *ScriptStep) modelFor(req domain.Request) string {
	if model := strings.TrimSpace(req.AIModel); model != "" {
		return model
	}
	return gr.defaultModel
}

// Run は、入力ソースからコンテンツを読み込み、AIモデルを使用して構造化ナレーションスクリプトを生成する一連の処理を実行します。
func (gr *ScriptStep) Run(ctx context.Context, req domain.Request) (domain.Script, error) {
	if req.InputURI == "" {
		return domain.Script{}, errors.New("入力ソース(InputURI)が指定されていません")
	}
	content, err := gr.readContent(ctx, req.InputURI)
	if err != nil {
		return domain.Script{}, err
	}
	model := gr.modelFor(req)
	slog.Info("処理開始", "mode", req.Mode, "model", model, "input_size", len(content))
	slog.Info("AIによるスクリプト生成を開始します...")

	prompt, err := gr.promptBuilder.Generate(req.Mode, content)
	if err != nil {
		return domain.Script{}, err
	}

	generatedResponse, err := gr.aiClient.Generate(ctx, model, prompt, nil, gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   gr.schema,
	})
	if err != nil {
		return domain.Script{}, fmt.Errorf("スクリプト生成に失敗しました: %w", err)
	}

	var script domain.Script
	if err := json.Unmarshal([]byte(generatedResponse.Text), &script); err != nil {
		return domain.Script{}, fmt.Errorf("AI応答のJSONデコードに失敗しました: %w", err)
	}
	slog.Info("AI スクリプト生成完了", "line_count", len(script.Lines), "title", script.Title)

	return script, nil
}

// readContent は、指定されたソースURLからコンテンツを取得します。
func (gr *ScriptStep) readContent(ctx context.Context, sourceURL string) (string, error) {
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
	if len(trimmedContent) < minInputContentLength {
		return "", fmt.Errorf("入力されたコンテンツが短すぎます")
	}
	return trimmedContent, nil
}
