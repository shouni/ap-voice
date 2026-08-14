package assets

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-prompt-kit/resource"
	"gopkg.in/yaml.v3"
)

// frontMatterDelim は front matter の区切りです。
const frontMatterDelim = "---"

// isPartial は、そのファイルが**モードではなく部品**かどうかを返します。
//
// 接頭辞は go-prompt-kit の既定（prompts.DefaultPartialPrefix = "_"）に合わせます。
// ビルダー側は同じ規則で Build の対象から外すため、**判定を二重に書かない**ために
// ライブラリの定数を参照します。モード名はジャンル接頭辞（tech_ / news_ …）を
// 持つので、"_" 始まりと衝突しません。
func isPartial(key string) bool {
	return strings.HasPrefix(key, prompts.DefaultPartialPrefix)
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

// LoadPrompts は埋め込まれたプロンプトの**本文だけ**を読み込みます。
//
// front matter は説明であってプロンプトではないので、ここで落とします。
// 残したまま渡すと YAML が指示文の先頭に紛れ込みます。
//
// **部品（_ 始まり）も含めて返します。** モード本文が {{template "_writing" .}} で
// 参照するため、ビルダーには全部渡す必要があります。選択肢に出さないのは
// LoadModes の役目です。
func LoadPrompts() (map[string]string, error) {
	raw, err := resource.Load(PromptFiles, promptDir, "")
	if err != nil {
		return nil, err
	}

	bodies := make(map[string]string, len(raw))
	for mode, content := range raw {
		_, body := splitFrontMatter(content)
		bodies[mode] = body
	}
	return bodies, nil
}

// LoadModes は、各プロンプトの front matter を読み、キー順に並べて返します。
//
// 並びを固定するのは、map の走査順がそのまま選択肢の順になると、
// 描画のたびに並びが変わるためです。
func LoadModes() ([]Mode, error) {
	raw, err := resource.Load(PromptFiles, promptDir, "")
	if err != nil {
		return nil, err
	}

	modes := make([]Mode, 0, len(raw))
	for key, content := range raw {
		if isPartial(key) {
			continue
		}
		front, _ := splitFrontMatter(content)

		var meta ModeMetadata
		if front != "" {
			// **黙って無視しません。** 書き間違えた説明が空欄になるだけだと、
			// 画面を開くまで気付けません。
			if err := yaml.Unmarshal([]byte(front), &meta); err != nil {
				return nil, fmt.Errorf("prompts/%s.md の front matter が読めません: %w", key, err)
			}
		}

		modes = append(modes, Mode{Key: key, ModeMetadata: meta})
	}

	sort.Slice(modes, func(i, j int) bool { return modes[i].Key < modes[j].Key })
	return modes, nil
}

// splitFrontMatter は、先頭の "---\nYAML\n---\n" を本文から切り離します。
// front matter が無ければ front は空文字、body は content そのものになります。
func splitFrontMatter(content string) (front, body string) {
	// 改行コードの差でブロックを見失わないよう先に揃えます。
	normalized := strings.ReplaceAll(content, "\r\n", "\n")

	opening := frontMatterDelim + "\n"
	if !strings.HasPrefix(normalized, opening) {
		return "", content
	}

	rest := normalized[len(opening):]
	closing := "\n" + frontMatterDelim
	idx := strings.Index(rest, closing+"\n")
	if idx < 0 {
		// 終端がファイル末尾にある場合（本文が空のプロンプト）。
		if !strings.HasSuffix(rest, closing) {
			return "", content
		}
		return rest[:len(rest)-len(closing)], ""
	}

	return rest[:idx], strings.TrimPrefix(rest[idx+len(closing):], "\n")
}
