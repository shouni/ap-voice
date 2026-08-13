package adapters

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/shouni/go-gemini-client/music"
	"github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
)

// TemplateData はプロンプトのテンプレートに渡すデータ構造です。
type TemplateData struct {
	InputText string
	// Recipe は promoMode のときだけ非 nil です。楽曲レシピを素の JSON 文字列のまま
	// 渡すと、AI が読み違えても気付けないうえ、テンプレートから項目を指せません。
	Recipe *music.Recipe
}

// promoMode は、入力を楽曲レシピ（ap-comp が出力する recipe.json）として解釈する
// 唯一のモードです。ほかのモードは入力を素のテキストとして扱います。
//
// モードは prompts/<mode>.md を置くだけで増える仕組みなので、ここに名前が要るのは
// 「入力の型が違う」モードだけです。文章を渡すモードを足すときはここを触りません。
const promoMode = "promo"

// styleFuncName は、テンプレートから話者ごとの実在スタイルを引く関数名です。
//
//	{{ styles "ずんだもん" }}  →  「ノーマル」「あまあま」…
//
// 実在しない組み合わせを提示すると、選ばれた分は既定スタイルへ落ちて指示が黙って
// 無視されます。台本側で候補を手書きすると必ずずれるので、話者一覧から引きます。
const styleFuncName = "styles"

// promptBuilder は、フォーマット済みのプロンプトを作成するためのインターフェースです。
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter は、さまざまなモードとデータに基づいてプロンプトを生成する役割を担います。
type PromptAdapter struct {
	scriptBuilder promptBuilder
}

// NewPromptAdapter は動的に読み込んだテンプレートを使用して PromptAdapter を構築します。
func NewPromptAdapter(speakers *speaker.Registry) (*PromptAdapter, error) {
	templates, err := assets.LoadPrompts()
	if err != nil {
		return nil, err
	}
	builder, err := prompts.NewBuilder(templates, prompts.WithFuncs(template.FuncMap{
		styleFuncName: styleLister(speakers),
	}))
	if err != nil {
		return nil, fmt.Errorf("ビルダーの構築に失敗: %w", err)
	}

	return &PromptAdapter{
		scriptBuilder: builder,
	}, nil
}

// Generate は指定されたモードとコンテンツに基づいてプロンプト文字列を生成します。
func (p *PromptAdapter) Generate(mode, content string) (string, error) {
	data := TemplateData{
		InputText: content,
	}
	if mode == promoMode {
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

// styleLister は、話者名からその話者が実際に持つスタイルの一覧を組み立てる関数を返します。
//
// 一覧に無い話者はエラーにします。テンプレートの書き間違いは、黙って空欄になるより
// プロンプト生成の時点で落ちたほうが原因が分かります。
func styleLister(speakers *speaker.Registry) func(string) (string, error) {
	return func(name string) (string, error) {
		styles, ok := speakers.StylesFor(name)
		if !ok {
			return "", fmt.Errorf("話者 %q は話者一覧にありません", name)
		}

		quoted := make([]string, len(styles))
		for i, style := range styles {
			quoted[i] = `"` + style + `"`
		}
		return strings.Join(quoted, "、"), nil
	}
}

// decodeRecipe は、入力を ap-comp の recipe.json として解釈します。
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
