package adapters

import (
	"strings"
	"testing"

	"github.com/shouni/ap-voice/assets"
)

// TestPromptAdapterBuildsEveryMode は、同梱するすべてのモードが実際に組み立てられることを
// 検証します。
//
// 部品の参照は実行時にしか解決されません。{{template "_clarity" .}} の綴りを 1 文字
// 間違えても go build は通り、そのモードを選んだ人が生成を走らせるまで誰も気付けません。
// モードを足す側から見ると、テストを書かなくてもここで拾われるのが要点です。
func TestPromptAdapterBuildsEveryMode(t *testing.T) {
	t.Parallel()

	adapter, err := NewPromptAdapter()
	if err != nil {
		t.Fatalf("NewPromptAdapter() error = %v", err)
	}
	modes, err := assets.LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}
	if len(modes) == 0 {
		t.Fatal("モードが 1 つも読み込まれていません")
	}

	const (
		text  = "これは組み立ての検証に使う元文章です。"
		title = "テスト楽曲"
	)
	// レシピ入力のモードは素のテキストではデコードで落ちるため、最小のレシピを渡します。
	recipe := `{"title":"` + title + `"}`

	for _, mode := range modes {
		t.Run(mode.Key, func(t *testing.T) {
			t.Parallel()

			input, want := text, text
			if mode.NeedsRecipe() {
				// レシピのモードは _input を使わず、項目を展開して埋め込みます。
				input, want = recipe, title
			}

			got, err := adapter.Generate(mode.Key, input)
			if err != nil {
				t.Fatalf("Generate(%q) error = %v", mode.Key, err)
			}
			if !strings.Contains(got, want) {
				t.Errorf("%s: 入力がプロンプトに入っていません", mode.Key)
			}
			// 展開されなかったテンプレート記法は Build がエラーにしないため、
			// 書き損じが素通りしないようここで見ます。
			if strings.Contains(got, "{{") {
				t.Errorf("%s: テンプレート記法が展開されずに残っています", mode.Key)
			}
			// すべてのモードが _writing を通ることの確認です。style の固定は
			// 1 モードでも抜けると、そのモードだけ指示が届かない状態になります。
			if !strings.Contains(got, "ノーマル") {
				t.Errorf("%s: 表記の共通ルールが入っていません", mode.Key)
			}
		})
	}
}
