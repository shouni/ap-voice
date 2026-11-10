package adapters

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/music"
	"github.com/shouni/go-prompt-kit/prompts"

	"github.com/shouni/ap-voice/assets"
)

// TemplateData is the data passed to the prompt templates.
type TemplateData struct {
	InputText string
	// Recipe is non-nil only for modes with input: "recipe". Passing the music recipe as a raw JSON
	// string means a misreading by the AI goes unnoticed, and templates cannot refer to its fields.
	Recipe *music.Recipe
}

// promptBuilder is the interface for producing formatted prompts.
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter generates prompts from the various modes and data.
type PromptAdapter struct {
	scriptBuilder promptBuilder
	// recipeModes are the modes whose input is interpreted as a music recipe.
	//
	// No mode names are written here. It is decided by whether the front matter of prompts/<mode>.md
	// has `input: "recipe"`, so adding a mode is just placing a file. The page's tabs read
	// the same front matter, so the answer to "which modes take a recipe" does not split
	// into two places.
	recipeModes map[string]bool
}

// NewPromptAdapter builds a PromptAdapter from the dynamically loaded templates.
func NewPromptAdapter() (*PromptAdapter, error) {
	templates, err := assets.LoadPrompts()
	if err != nil {
		return nil, err
	}
	// WithTrimPartials is set because partials are referenced from the middle of the body.
	// If a file's trailing newline survived, a blank line would appear where it is inserted, and the
	// bullet list that follows would be cut into a separate list (_writing / _length / _title are affected).
	builder, err := prompts.NewBuilder(templates, prompts.WithTrimPartials())
	if err != nil {
		return nil, fmt.Errorf("failed to build the builder: %w", err)
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

// Generate produces the prompt string for the given mode and content.
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
		return "", fmt.Errorf("failed to execute the prompt template: %w", err)
	}
	return prompt, nil
}

// decodeRecipe interprets the input as a recipe.json in music.Recipe form.
//
// It could be poured into the prompt as a raw JSON string, but then broken input would go
// unnoticed until generation finished. A promotion script cannot be made from a recipe without
// even a title, so it fails before Gemini is called.
func decodeRecipe(content string) (*music.Recipe, error) {
	var recipe music.Recipe
	if err := json.Unmarshal([]byte(content), &recipe); err != nil {
		return nil, fmt.Errorf("failed to decode the music recipe JSON: %w", err)
	}
	if strings.TrimSpace(recipe.Title) == "" {
		return nil, fmt.Errorf("the music recipe has no title")
	}
	return &recipe, nil
}
