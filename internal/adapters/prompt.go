package adapters

import (
	"github.com/shouni/go-prompt-kit/prompts"

	"prototypus-ai-doc-go/assets"
)

// NewPromptAdapter は動的に読み込んだテンプレートを使用して Builder を構築します。
func NewPromptAdapter() (*prompts.Builder, error) {
	templates, err := assets.LoadPrompts()
	if err != nil {
		return nil, err
	}
	return prompts.NewBuilder(templates)
}
