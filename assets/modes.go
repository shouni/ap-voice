package assets

import (
	"fmt"
	"maps"
	"math"
	"sort"
	"sync"

	"github.com/shouni/go-prompt-kit/frontmatter"
	"github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-prompt-kit/resource"
	"go.yaml.in/yaml/v3"
)

// isPartial reports whether the file is a partial rather than a mode.
//
// The check is not written twice: it calls the same function the builder uses to exclude files from Build.
// Back when it checked for a leading "_" by hand, a key such as "en/_writing" from recursive loading
// would have disagreed with the library. Mode names carry a genre prefix (tech_ / news_ …),
// so they do not collide with a leading "_".
func isPartial(key string) bool {
	return prompts.IsPartial(key, prompts.DefaultPartialPrefix)
}

// ModeMetadata is the mode description written in the front matter at the top of the prompt.
// As in the sibling services, the description lives in the prompt itself.
//
// The page holds no list of its own because adding a mode is meant to be just placing
// prompts/<mode>.md. With descriptions in a separate file, if whoever added a mode forgot the
// description nobody would notice, and only the options would grow.
type ModeMetadata struct {
	// Order is the order among the options. It is numbered from 1 in steps of 10 (so inserting
	// between two does not require renumbering the neighbours).
	//
	// This is why file names carry no number. The file name is the mode's key as is,
	// i.e. the value on Request.Mode and the /modes/{mode} URL. Renaming to reorder would
	// make stored jobs and outside callers (the keys MCP's list_voice_modes returns) lose
	// their target all at once. Order is a display concern, so it sits with label rather than
	// in the identifier.
	Order int `yaml:"order"`
	// Label is the name shown among the options. The key (file name) is ASCII and does not say
	// who speaks, so a separate Japanese display name is kept.
	Label string `yaml:"label"`
	// Direction is a one-line description of what the mode makes.
	Direction string `yaml:"direction"`
	// UseWhen says for which inputs and subjects to choose it.
	UseWhen string `yaml:"use_when"`
	// Input is the kind of input the mode takes. Empty is treated as InputText.
	//
	// It is a different axis from the genre. The file name prefix (tech_ / news_ / comedy_ …)
	// is the genre, while the input type is orthogonal to it — comedy_manzai and tech_solo
	// take the same plain text despite different genres. Directories could express
	// only one axis, so it is written here.
	Input string `yaml:"input"`
}

// Input kinds. They decide the page's tab and the type of data passed to the prompt.
const (
	// InputText is a mode that takes plain text (web articles, documents).
	InputText = "text"
	// InputRecipe is a mode that takes a music recipe (music.Recipe).
	// Given plain text, it fails at decoding before reaching generation.
	InputRecipe = "recipe"
)

// InputKind returns the kind of input the mode takes.
// A mode whose front matter has no input is treated as plain text.
func (m Mode) InputKind() string {
	if m.Input == "" {
		return InputText
	}
	return m.Input
}

// NeedsRecipe reports whether the mode requires a recipe.json.
func (m Mode) NeedsRecipe() bool { return m.InputKind() == InputRecipe }

// FilterModes returns only the modes of the given input kind.
//
// The options for each page tab are built with it. Showing recipe.json modes on the plain text
// tab would offer combinations that are selectable but always fail.
func FilterModes(modes []Mode, kind string) []Mode {
	out := make([]Mode, 0, len(modes))
	for _, m := range modes {
		if m.InputKind() == kind {
			out = append(out, m)
		}
	}
	return out
}

// Mode is one mode shown on the form.
type Mode struct {
	// Key is the file name of prompts/<key>.md, and the value passed to the worker as is.
	Key string
	ModeMetadata
}

// sortOrder is the rank used for sorting. Modes without an order go last.
//
// Last rather than first because a mode with a forgotten order being appended does less harm
// than it jumping ahead of the existing order. Like DisplayName falling back to the key,
// forgetting it does not make the option disappear.
func (m Mode) sortOrder() int {
	if m.Order == 0 {
		return math.MaxInt
	}
	return m.Order
}

// DisplayName is the name shown among the options.
//
// A prompt without front matter is still shown by its key, so the option does not disappear.
// A working mode vanishing from the page over a forgotten description would be worse.
func (m Mode) DisplayName() string {
	if m.Label != "" {
		return m.Label
	}
	return m.Key
}

// promptSet holds the embedded prompts' bodies and front matter separately.
type promptSet struct {
	bodies map[string]string
	fronts map[string]string
}

// loadPromptSet loads the prompts and splits off the front matter once, on the first call.
// The bodies (LoadPrompts) and descriptions (LoadModes) are separate entry points, but
// they come from the same directory. Without memoising, the adapter and the page assembly
// would read the same files again and again.
//
// The maps in the returned promptSet are shared, so do not modify them
// (LoadPrompts and LoadModes both copy into new containers before use).
var loadPromptSet = sync.OnceValues(func() (promptSet, error) {
	raw, err := resource.Load(PromptFiles, promptDir)
	if err != nil {
		return promptSet{}, err
	}

	bodies, fronts := frontmatter.SplitMap(raw)
	return promptSet{bodies: bodies, fronts: fronts}, nil
})

// LoadPrompts loads only the bodies of the embedded prompts.
//
// Front matter is description, not prompt, so it is dropped here.
// Passed along, the YAML would slip into the head of the instructions.
//
// Partials (leading _) are returned too. Mode bodies reference them with {{template "_writing" .}},
// so the builder must receive all of them. Keeping them out of the options is
// LoadModes's job.
func LoadPrompts() (map[string]string, error) {
	set, err := loadPromptSet()
	if err != nil {
		return nil, err
	}
	return maps.Clone(set.bodies), nil
}

// LoadModes reads each prompt's front matter and returns them sorted by order.
//
// The order is fixed because, if the map's iteration order became the options' order,
// it would change on every render. Ties in order are sorted by key.
func LoadModes() ([]Mode, error) {
	set, err := loadPromptSet()
	if err != nil {
		return nil, err
	}

	// Partials are not offered as options, so they are excluded from description parsing first.
	// The memoised map is shared, so it is copied into another container rather than trimmed.
	fronts := maps.Clone(set.fronts)
	maps.DeleteFunc(fronts, func(key, _ string) bool { return isPartial(key) })

	// It is not silently ignored. If a mistyped description just came out blank,
	// it would go unnoticed until the page was opened.
	metas, err := frontmatter.DecodeMap[ModeMetadata](fronts, yaml.Unmarshal)
	if err != nil {
		return nil, fmt.Errorf("cannot read the prompts' front matter: %w", err)
	}

	modes := make([]Mode, 0, len(metas))
	for key, meta := range metas {
		modes = append(modes, Mode{Key: key, ModeMetadata: meta})
	}

	sort.Slice(modes, func(i, j int) bool {
		if oi, oj := modes[i].sortOrder(), modes[j].sortOrder(); oi != oj {
			return oi < oj
		}
		// The last resort when order cannot settle the sequence. Without it,
		// modes that forgot their order would swap places on every render.
		return modes[i].Key < modes[j].Key
	})
	return modes, nil
}
