package assets

import (
	"strings"
	"testing"
)

// TestLoadPromptsStripsFrontMatter verifies that no front matter remains in the prompt bodies.
//
// This is the test that matters most. Front matter is description, not prompt, so if it were
// passed along the YAML would slip into the head of the instructions, and since generation still
// succeeds, reading the output would not reveal it.
func TestLoadPromptsStripsFrontMatter(t *testing.T) {
	t.Parallel()

	// go-prompt-kit's frontmatter does the splitting itself, so all this checks is
	// "does any bundled prompt still carry front matter".
	const frontMatterDelim = "---"

	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts() error = %v", err)
	}
	if len(prompts) == 0 {
		t.Fatal("no prompts were loaded")
	}

	for mode, body := range prompts {
		// It checks for a remaining front matter block rather than the "---" delimiter.
		// The partial _input.md's body starts with "--- 元文章 ---", so
		// simply forbidding a "---" start would reject a correct partial too.
		if strings.HasPrefix(body, frontMatterDelim+"\n") {
			t.Errorf("%s: front matter remains in the body", mode)
		}
		for _, key := range []string{"label:", "direction:", "use_when:"} {
			if strings.Contains(body, key) {
				t.Errorf("%s: front matter key %q remains in the body", mode, key)
			}
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s: the body is empty", mode)
		}
	}
}

// TestLoadModesHasMetadata verifies that every bundled prompt has a description.
//
// Without a display name it shows by key, so the page does not break, but that is a safety net
// for whoever adds one later, not the intended state.
func TestLoadModesHasMetadata(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}
	if len(modes) == 0 {
		t.Fatal("no modes were loaded")
	}

	for _, mode := range modes {
		if mode.Label == "" {
			t.Errorf("%s: no label", mode.Key)
		}
		if mode.Direction == "" {
			t.Errorf("%s: no direction", mode.Key)
		}
		if mode.UseWhen == "" {
			t.Errorf("%s: no use_when", mode.Key)
		}
		if mode.Order == 0 {
			t.Errorf("%s: no order", mode.Key)
		}
	}
}

// TestLoadModesIsSorted verifies that the options come in the same order every time.
// Passing a map through as is changes the order on every render.
func TestLoadModesIsSorted(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	for i := 1; i < len(modes); i++ {
		prev, cur := modes[i-1], modes[i]
		if prev.sortOrder() > cur.sortOrder() {
			t.Errorf("order is not ascending: %s(%d) followed by %s(%d)",
				prev.Key, prev.Order, cur.Key, cur.Order)
		}
		// Even with duplicate orders, the sequence must be the same every time.
		if prev.sortOrder() == cur.sortOrder() && prev.Key >= cur.Key {
			t.Errorf("ties are not ordered stably: %s followed by %s", prev.Key, cur.Key)
		}
	}
}

// TestLoadModesOrderIsUnique verifies that no two bundled modes share an order.
//
// A tie is broken by key so the page does not break, but that catches a typo; it is not
// the intended order. A duplicate introduced while renumbering is caught here.
func TestLoadModesOrderIsUnique(t *testing.T) {
	t.Parallel()

	modes, err := LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	seen := make(map[int]string, len(modes))
	for _, mode := range modes {
		if prev, ok := seen[mode.Order]; ok {
			t.Errorf("order %d is shared by %s and %s", mode.Order, prev, mode.Key)
			continue
		}
		seen[mode.Order] = mode.Key
	}
}

// TestLoadModesMatchesPrompts verifies that modes and prompts are one-to-one.
// It prevents a mode shown on the page from being missing on the worker.
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

	// Partials are not modes. The counts are compared excluding partials.
	var bodies int
	for key := range prompts {
		if !isPartial(key) {
			bodies++
		}
	}
	if len(modes) != bodies {
		t.Fatalf("%d modes against %d prompts", len(modes), bodies)
	}
	for _, mode := range modes {
		if _, ok := prompts[mode.Key]; !ok {
			t.Errorf("no prompt for %s", mode.Key)
		}
	}
}

// TestPartialsAreLoadedButNotOffered verifies that shared partials reach the builder but are
// not offered as options.
//
// Getting either wrong breaks things differently. Mixed into the modes, users would see an option
// called "_writing"; not handed to the builder, the body's {{template "_writing" .}}
// could not resolve and generation would fail.
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
		t.Fatal("no shared partials were loaded")
	}

	for _, mode := range modes {
		if isPartial(mode.Key) {
			t.Errorf("a partial is offered as an option: %s", mode.Key)
		}
	}
}

// TestModeDisplayName verifies that a mode without a display name shows by its key.
func TestModeDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode Mode
		want string
	}{
		{name: "has a display name", mode: Mode{Key: "promo", ModeMetadata: ModeMetadata{Label: "楽曲紹介"}}, want: "楽曲紹介"},
		{name: "no display name", mode: Mode{Key: "promo"}, want: "promo"},
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

// TestSplitFrontMatter verifies where the split falls.
