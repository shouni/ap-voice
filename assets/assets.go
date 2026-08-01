// Package assets は、ナレーション生成に使うプロンプトを埋め込みリソースとして提供します。
package assets

import (
	"embed"

	"github.com/shouni/go-prompt-kit/resource"
)

const (
	promptDir    = "prompts"
	promptPrefix = "prompt_"
)

// PromptFiles は埋め込まれたプロンプトファイル群です。
//
//go:embed prompts/prompt_*.md
var PromptFiles embed.FS

// LoadPrompts は埋め込まれたプロンプトファイルを読み込みます。
func LoadPrompts() (map[string]string, error) {
	return resource.Load(PromptFiles, promptDir, promptPrefix)
}
