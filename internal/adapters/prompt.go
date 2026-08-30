package adapters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shouni/go-gemini-client/music"
	"github.com/shouni/go-prompt-kit/prompts"

	"github.com/shouni/ap-voice/assets"
)

// TemplateData はプロンプトのテンプレートに渡すデータ構造です。
type TemplateData struct {
	InputText string
	// Recipe は input: "recipe" のモードのときだけ非 nil です。楽曲レシピを素の JSON 文字列のまま
	// 渡すと、AI が読み違えても気付けないうえ、テンプレートから項目を指せません。
	Recipe *music.Recipe
}

// promptBuilder は、フォーマット済みのプロンプトを作成するためのインターフェースです。
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter は、さまざまなモードとデータに基づいてプロンプトを生成する役割を担います。
type PromptAdapter struct {
	scriptBuilder promptBuilder
	// recipeModes は、入力を楽曲レシピとして解釈するモードです。
	//
	// モード名をここに書きません。prompts/<mode>.md の front matter が
	// `input: "recipe"` を持つかどうかで決まるため、モードを足すのは
	// ファイルを置くだけで済みます。画面のタブも同じ front matter を読むので、
	// 「どのモードがレシピ入力か」の答えが 2 箇所に分かれません。
	recipeModes map[string]bool
}

// NewPromptAdapter は動的に読み込んだテンプレートを使用して PromptAdapter を構築します。
func NewPromptAdapter() (*PromptAdapter, error) {
	templates, err := assets.LoadPrompts()
	if err != nil {
		return nil, err
	}
	// WithTrimPartials を付けるのは、partial を本文の途中から参照しているためです。
	// ファイル末尾の改行がそのまま残ると、差し込んだ位置に空行が入り、続きの
	// 箇条書きが別のリストとして切れます（_writing / _length / _title が該当）。
	builder, err := prompts.NewBuilder(templates, prompts.WithTrimPartials())
	if err != nil {
		return nil, fmt.Errorf("ビルダーの構築に失敗: %w", err)
	}

	modes, err := assets.LoadModes()
	if err != nil {
		return nil, err
	}
	recipeModes := make(map[string]bool, 1)
	for _, m := range assets.FilterModes(modes, assets.InputRecipe) {
		recipeModes[m.Key] = true
	}

	return &PromptAdapter{
		scriptBuilder: builder,
		recipeModes:   recipeModes,
	}, nil
}

// Generate は指定されたモードとコンテンツに基づいてプロンプト文字列を生成します。
func (p *PromptAdapter) Generate(mode, content string) (string, error) {
	data := TemplateData{
		InputText: content,
	}
	if p.recipeModes[mode] {
		recipe, err := decodeRecipe(content)
		if err != nil {
			return "", err
		}
		data.Recipe = recipe
	}
	prompt, err := p.scriptBuilder.Build(mode, data)
	if err != nil {
		return "", fmt.Errorf("プロンプトテンプレートの実行に失敗: %w", err)
	}
	return prompt, nil
}

// decodeRecipe は、入力を music.Recipe 形式の recipe.json として解釈します。
//
// 素の JSON 文字列のままプロンプトへ流し込むこともできますが、それだと入力の壊れに
// 生成が終わるまで気付けません。曲名すら無いレシピから宣伝台本は作れないので、
// Gemini を呼ぶ前に落とします。
func decodeRecipe(content string) (*music.Recipe, error) {
	var recipe music.Recipe
	if err := json.Unmarshal([]byte(content), &recipe); err != nil {
		return nil, fmt.Errorf("楽曲レシピのJSONデコードに失敗しました: %w", err)
	}
	if strings.TrimSpace(recipe.Title) == "" {
		return nil, fmt.Errorf("楽曲レシピに title がありません")
	}
	return &recipe, nil
}
