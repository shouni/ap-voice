package adapters

import (
	"strings"
	"testing"

	"github.com/shouni/ap-voice/assets"
)

// TestPromptAdapterBuildsEveryMode verifies that every bundled mode actually builds.
//
// References to partials are resolved only at run time. Misspell {{template "_clarity" .}} by one
// character and go build still passes; nobody notices until someone picks that mode and runs a generation.
// For whoever adds a mode, the point is that it is caught here without writing a test.
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
		t.Fatal("no modes were loaded")
	}

	const (
		text  = "これは組み立ての検証に使う元文章です。"
		title = "テスト楽曲"
	)
	// Recipe-input modes fail to decode plain text, so they are given a minimal recipe.
	recipe := `{"title":"` + title + `"}`

	for _, mode := range modes {
		t.Run(mode.Key, func(t *testing.T) {
			t.Parallel()

			input, want := text, text
			if mode.NeedsRecipe() {
				// Recipe modes do not use _input; they expand the fields and embed them.
				input, want = recipe, title
			}

			got, err := adapter.Generate(mode.Key, input)
			if err != nil {
				t.Fatalf("Generate(%q) error = %v", mode.Key, err)
			}
			if !strings.Contains(got, want) {
				t.Errorf("%s: the input is not in the prompt", mode.Key)
			}
			// Build does not fail on template syntax that was not expanded,
			// so it is checked here to keep a typo from slipping through.
			if strings.Contains(got, "{{") {
				t.Errorf("%s: template syntax was left unexpanded", mode.Key)
			}
			// Checks that every mode goes through _writing. If even one mode misses the style pin,
			// that mode alone would not receive the instruction.
			if !strings.Contains(got, "ノーマル") {
				t.Errorf("%s: the shared writing rules are missing", mode.Key)
			}
		})
	}
}
