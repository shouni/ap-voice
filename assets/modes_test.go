package assets

import (
	"strings"
	"testing"
)

// TestLoadPromptsStripsFrontMatter は、プロンプト本文に front matter が
// 残らないことを検証します。
//
// ここが一番効くテストです。front matter は説明であってプロンプトではないので、
// 残ったまま渡すと YAML が指示文の先頭に紛れ込み、しかも生成は成功してしまうため
// 出力を読んでも気付けません。
func TestLoadPromptsStripsFrontMatter(t *testing.T) {
	t.Parallel()

	// 切り離し自体は go-prompt-kit の frontmatter が担うため、ここで見るのは
	// 「同梱しているプロンプトに front matter が残っていないか」だけです。
	const frontMatterDelim = "---"

	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}
	if len(prompts) == 0 {
		t.Fatal("プロンプトが 1 つも読み込まれていません")
	}

	for mode, body := range prompts {
		// 区切り「---」ではなく、front matter のブロックが残っていないかを見ます。
		// 部品 _input.md の本文は "--- 元文章 ---" で始まるため、
		// 単に "---" 始まりを禁じると正しい部品まで落ちます。
		if strings.HasPrefix(body, frontMatterDelim+"\n") {
			t.Errorf("%s: front matter が本文に残っています", mode)
		}
		for _, key := range []string{"label:", "direction:", "use_when:"} {
			if strings.Contains(body, key) {
				t.Errorf("%s: 本文に front matter の %q が残っています", mode, key)
			}
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s: 本文が空です", mode)
		}
	}
}

// TestLoadModesHasMetadata は、同梱するプロンプトがすべて説明を持つことを検証します。
//
// 表示名が無くてもキーで出るため画面は壊れませんが、それは後から足した人を
// 助けるための保険であって、既定の状態ではありません。
func TestLoadModesHasMetadata(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}
	if len(modes) == 0 {
		t.Fatal("モードが 1 つも読み込まれていません")
	}

	for _, mode := range modes {
		if mode.Label == "" {
			t.Errorf("%s: label がありません", mode.Key)
		}
		if mode.Direction == "" {
			t.Errorf("%s: direction がありません", mode.Key)
		}
		if mode.UseWhen == "" {
			t.Errorf("%s: use_when がありません", mode.Key)
		}
		if mode.Order == 0 {
			t.Errorf("%s: order がありません", mode.Key)
		}
	}
}

// TestLoadModesIsSorted は、選択肢の並びが毎回同じことを検証します。
// map をそのまま流すと描画のたびに順序が変わります。
func TestLoadModesIsSorted(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	for i := 1; i < len(modes); i++ {
		prev, cur := modes[i-1], modes[i]
		if prev.sortOrder() > cur.sortOrder() {
			t.Errorf("order が昇順ではありません: %s(%d) の次に %s(%d)",
				prev.Key, prev.Order, cur.Key, cur.Order)
		}
		// order が重複していても、並びは毎回同じでなければなりません。
		if prev.sortOrder() == cur.sortOrder() && prev.Key >= cur.Key {
			t.Errorf("同順位の並びが安定していません: %s の次に %s", prev.Key, cur.Key)
		}
	}
}

// TestLoadModesOrderIsUnique は、同梱するモードの order が重なっていないことを検証します。
//
// 重なってもキー順で決まるので画面は壊れませんが、それは書き間違いの受け皿であって、
// 意図した並びではありません。番号を振り直した拍子の重複はここで気付けます。
func TestLoadModesOrderIsUnique(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	seen := make(map[int]string, len(modes))
	for _, mode := range modes {
		if prev, ok := seen[mode.Order]; ok {
			t.Errorf("order %d が %s と %s で重複しています", mode.Order, prev, mode.Key)
			continue
		}
		seen[mode.Order] = mode.Key
	}
}

// TestLoadModesMatchesPrompts は、モードとプロンプトが 1 対 1 であることを検証します。
// 画面に出したモードが worker に無い、という食い違いを防ぎます。
func TestLoadModesMatchesPrompts(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}

	// 部品はモードになりません。数を突き合わせるのは部品を除いた分です。
	var bodies int
	for key := range prompts {
		if !isPartial(key) {
			bodies++
		}
	}
	if len(modes) != bodies {
		t.Fatalf("モード %d 件に対しプロンプトは %d 件です", len(modes), bodies)
	}
	for _, mode := range modes {
		if _, ok := prompts[mode.Key]; !ok {
			t.Errorf("%s に対応するプロンプトがありません", mode.Key)
		}
	}
}

// TestPartialsAreLoadedButNotOffered は、共通部品がビルダーには渡り、
// 選択肢には出ないことを検証します。
//
// どちらか片方でも間違うと壊れ方が違います。モードに混ざれば利用者に
// 「_writing」という選択肢が見え、ビルダーに渡らなければ本文の
// {{template "_writing" .}} が解決できず生成が落ちます。
func TestPartialsAreLoadedButNotOffered(t *testing.T) {
	t.Parallel()

	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}
	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	var partials int
	for key := range prompts {
		if isPartial(key) {
			partials++
		}
	}
	if partials == 0 {
		t.Fatal("共通部品が 1 つも読み込まれていません")
	}

	for _, mode := range modes {
		if isPartial(mode.Key) {
			t.Errorf("部品が選択肢に出ています: %s", mode.Key)
		}
	}
}

// TestModeDisplayName は、表示名が無いモードがキーで出ることを検証します。
func TestModeDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode Mode
		want string
	}{
		{name: "表示名がある", mode: Mode{Key: "promo", ModeMetadata: ModeMetadata{Label: "楽曲紹介"}}, want: "楽曲紹介"},
		{name: "表示名が無い", mode: Mode{Key: "promo"}, want: "promo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mode.DisplayName(); got != tt.want {
				t.Errorf("DisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSplitFrontMatter は、切り出しの境目を検証します。
