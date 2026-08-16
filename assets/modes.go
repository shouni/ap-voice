package assets

import (
	"fmt"
	"maps"
	"sort"
	"sync"

	"github.com/shouni/go-prompt-kit/frontmatter"
	"github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-prompt-kit/resource"
	"go.yaml.in/yaml/v3"
)

// isPartial は、そのファイルが**モードではなく部品**かどうかを返します。
//
// **判定を二重に書きません。** ビルダーが Build の対象から外すのと同じ関数を呼びます。
// 自前で "_" 始まりを見ていた頃は、再帰読み込みで "en/_writing" のようなキーになった
// 場合にライブラリ側とずれる状態でした。モード名はジャンル接頭辞（tech_ / news_ …）を
// 持つので、"_" 始まりと衝突しません。
func isPartial(key string) bool {
	return prompts.IsPartial(key, prompts.DefaultPartialPrefix)
}

// ModeMetadata は、プロンプト冒頭の front matter に書くモードの説明です。
// ap-comp と同じ方式で、**説明の置き場をプロンプト自身にします。**
//
// 画面側に一覧を持たせない理由は、モードの追加が prompts/<mode>.md を置くだけで
// 済む仕組みだからです。説明を別ファイルに分けると、モードを足した人が説明を
// 書き忘れても誰も気付かず、選択肢だけが増えていきます。
type ModeMetadata struct {
	// Label は選択肢に出す名前です。キー（ファイル名）は英字で、誰が喋るのかまでは
	// 読み取れないため、日本語の表示名を別に持ちます。
	Label string `yaml:"label"`
	// Direction は何を作るモードなのかの一行説明です。
	Direction string `yaml:"direction"`
	// UseWhen は、どういう入力・題材のときに選ぶかです。
	UseWhen string `yaml:"use_when"`
	// Input は、そのモードが受け取る入力の種類です。空なら InputText 扱いです。
	//
	// **ジャンルとは別の軸です。** ファイル名の接頭辞（tech_ / news_ / comedy_ …）は
	// ジャンルを表しますが、入力の型はそれと直交します — comedy_manzai と tech_solo は
	// ジャンルが違っても同じ素のテキストを受け取ります。ディレクトリで分けると
	// 軸を 1 本しか表せないため、ここに書きます。
	Input string `yaml:"input"`
}

// 入力の種類。**画面のタブと、プロンプトへ渡すデータの型が、これで決まります。**
const (
	// InputText は素のテキスト（Web 記事・文書）を受け取るモードです。
	InputText = "text"
	// InputRecipe は ap-comp の recipe.json を受け取るモードです。
	// 素のテキストを渡すと、生成へ進む前にデコードで落ちます。
	InputRecipe = "recipe"
)

// InputKind は、そのモードが受け取る入力の種類を返します。
// front matter に input が無いモードは素のテキスト扱いです。
func (m Mode) InputKind() string {
	if m.Input == "" {
		return InputText
	}
	return m.Input
}

// NeedsRecipe は、そのモードが recipe.json を要求するかを返します。
func (m Mode) NeedsRecipe() bool { return m.InputKind() == InputRecipe }

// FilterModes は、指定した入力種別のモードだけを返します。
//
// **画面のタブごとの選択肢はこれで作ります。** 素のテキストのタブに
// recipe.json のモードを出すと、選べるのに必ず失敗する組み合わせになります。
func FilterModes(modes []Mode, kind string) []Mode {
	out := make([]Mode, 0, len(modes))
	for _, m := range modes {
		if m.InputKind() == kind {
			out = append(out, m)
		}
	}
	return out
}

// Mode は、フォームに出す 1 モードです。
type Mode struct {
	// Key は prompts/<key>.md のファイル名で、そのまま worker へ渡る値です。
	Key string
	ModeMetadata
}

// DisplayName は選択肢に表示する名前です。
//
// front matter が無いプロンプトを置いてもキーで表示され、**選択肢自体は消えません。**
// 説明の書き忘れで動くはずのモードが画面から消えるほうが困るためです。
func (m Mode) DisplayName() string {
	if m.Label != "" {
		return m.Label
	}
	return m.Key
}

// promptSet は、埋め込みプロンプトの本文と front matter を分けて保持します。
type promptSet struct {
	bodies map[string]string
	fronts map[string]string
}

// loadPromptSet は、プロンプトの読み込みと front matter の切り離しを最初の呼び出しで
// 1度だけ行います。本文（LoadPrompts）と説明（LoadModes）は別の入口ですが、
// 出どころは同じディレクトリです。記憶しないと、アダプターと画面の組み立てで
// 同じファイルを何度も読み直すことになります。
//
// 返す promptSet の中のマップは共有されるため、書き換えないでください
// （LoadPrompts と LoadModes は、いずれも新しい入れ物へ写して使います）。
var loadPromptSet = sync.OnceValues(func() (promptSet, error) {
	raw, err := resource.Load(PromptFiles, promptDir)
	if err != nil {
		return promptSet{}, err
	}

	bodies, fronts := frontmatter.SplitMap(raw)
	return promptSet{bodies: bodies, fronts: fronts}, nil
})

// LoadPrompts は埋め込まれたプロンプトの**本文だけ**を読み込みます。
//
// front matter は説明であってプロンプトではないので、ここで落とします。
// 残したまま渡すと YAML が指示文の先頭に紛れ込みます。
//
// **部品（_ 始まり）も含めて返します。** モード本文が {{template "_writing" .}} で
// 参照するため、ビルダーには全部渡す必要があります。選択肢に出さないのは
// LoadModes の役目です。
func LoadPrompts() (map[string]string, error) {
	set, err := loadPromptSet()
	if err != nil {
		return nil, err
	}
	return maps.Clone(set.bodies), nil
}

// LoadModes は、各プロンプトの front matter を読み、キー順に並べて返します。
//
// 並びを固定するのは、map の走査順がそのまま選択肢の順になると、
// 描画のたびに並びが変わるためです。
func LoadModes() ([]Mode, error) {
	set, err := loadPromptSet()
	if err != nil {
		return nil, err
	}

	// 部品は選択肢に出さないので、説明の解析対象からも先に外します。
	// 記憶したマップは共有されるため、削るのではなく別の入れ物へ写します。
	fronts := maps.Clone(set.fronts)
	maps.DeleteFunc(fronts, func(key, _ string) bool { return isPartial(key) })

	// **黙って無視しません。** 書き間違えた説明が空欄になるだけだと、
	// 画面を開くまで気付けません。
	metas, err := frontmatter.DecodeMap[ModeMetadata](fronts, yaml.Unmarshal)
	if err != nil {
		return nil, fmt.Errorf("prompts の front matter が読めません: %w", err)
	}

	modes := make([]Mode, 0, len(metas))
	for key, meta := range metas {
		modes = append(modes, Mode{Key: key, ModeMetadata: meta})
	}

	sort.Slice(modes, func(i, j int) bool { return modes[i].Key < modes[j].Key })
	return modes, nil
}
