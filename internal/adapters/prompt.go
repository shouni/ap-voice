package adapters

import (
	"fmt"

	"github.com/shouni/go-prompt-kit/prompts"

	"ap-voice/assets"
)

// templateData はプロンプトのテンプレートに渡すデータ構造です。
type templateData struct {
	InputText string
}

// promptBuilder は、フォーマット済みのプロンプトを作成するためのインターフェースです。
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter は、さまざまなモードとデータに基づいてプロンプトを生成する役割を担います。
type PromptAdapter struct {
	scriptBuilder promptBuilder
}

// NewPromptAdapter は動的に読み込んだテンプレートを使用して PromptAdapter を構築します。
func NewPromptAdapter() (*PromptAdapter, error) {
	templates, err := assets.LoadPrompts()
	if err != nil {
		return nil, err
	}
	builder, err := prompts.NewBuilder(templates)
	if err != nil {
		return nil, fmt.Errorf("ビルダーの構築に失敗: %w", err)
	}

	return &PromptAdapter{
		scriptBuilder: builder,
	}, nil
}

// Generate は指定されたモードとコンテンツに基づいてプロンプト文字列を生成します。
func (pa *PromptAdapter) Generate(mode, content string) (string, error) {
	data := templateData{
		InputText: content,
	}
	prompt, err := pa.scriptBuilder.Build(mode, data)
	if err != nil {
		return "", fmt.Errorf("プロンプトテンプレートの実行に失敗: %w", err)
	}
	return prompt, nil
}
