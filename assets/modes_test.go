package assets

import (
	"strings"
	"testing"
)

// TestLoadPromptsStripsFrontMatter は、プロンプト本文に front matter が
// 残らないことを検証します。
//
// **ここが一番効くテストです。** front matter は説明であってプロンプトではないので、
// 残ったまま渡すと YAML が指示文の先頭に紛れ込み、しかも生成は成功してしまうため
// 出力を読んでも気付けません。
func TestLoadPromptsStripsFrontMatter(t *testing.T) {
	t.Parallel()

	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}
	if len(prompts) == 0 {
		t.Fatal("プロンプトが 1 つも読み込まれていません")
	}

	for mode, body := range prompts {
		// 区切り「---」ではなく、**front matter のブロック**が残っていないかを見ます。
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
// 表示名が無くてもキーで出るため画面は壊れませんが、それは**後から足した人を
// 助けるための保険**であって、既定の状態ではありません。
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
		if modes[i-1].Key >= modes[i].Key {
			t.Errorf("並びが昇順ではありません: %s の次に %s", modes[i-1].Key, modes[i].Key)
		}
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

// TestPartialsAreLoadedButNotOffered は、共通部品が**ビルダーには渡り、
// 選択肢には出ない**ことを検証します。
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
func TestSplitFrontMatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantFront string
		wantBody  string
	}{
		{
			name:      "front matter がある",
			content:   "---\nlabel: \"名前\"\n---\n本文です。\n",
			wantFront: "label: \"名前\"",
			wantBody:  "本文です。\n",
		},
		{
			name:      "front matter が無い",
			content:   "本文だけです。\n",
			wantFront: "",
			wantBody:  "本文だけです。\n",
		},
		{
			name:      "CRLF でも切り出せる",
			content:   "---\r\nlabel: \"名前\"\r\n---\r\n本文です。\r\n",
			wantFront: "label: \"名前\"",
			wantBody:  "本文です。\n",
		},
		{
			// 見出しとして "---" を使った本文を front matter と誤認しないこと。
			name:      "途中の区切り線は無視する",
			content:   "本文です。\n---\nまだ本文です。\n",
			wantFront: "",
			wantBody:  "本文です。\n---\nまだ本文です。\n",
		},
		{
			name:      "終端がファイル末尾",
			content:   "---\nlabel: \"名前\"\n---",
			wantFront: "label: \"名前\"",
			wantBody:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			front, body := splitFrontMatter(tt.content)
			if front != tt.wantFront {
				t.Errorf("front = %q, want %q", front, tt.wantFront)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}
