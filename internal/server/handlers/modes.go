package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/go-serve-kit/respond"
)

// samplePlaceholder is the stand-in input used when the detail page assembles a prompt.
// No real article is needed: what is shown is where the input goes and the instructions.
const samplePlaceholder = "（ここに入力ソースの本文が入ります）"

// sampleRecipe is a stand-in recipe for building modes whose input type differs.
//
// Only the music promotion mode reads a music recipe (music.Recipe). Plain text would fail
// to decode, so a value of the right shape is provided.
const sampleRecipe = `{
  "title": "（曲名）", "theme": "（テーマ）", "mood": "（ムード）", "tempo": 120,
  "key": "C major", "instruments": ["（編成）"],
  "sections": [{"name": "Intro", "duration": 8}],
  "lyrics": {"hook": "（フック）", "narrative": "（物語）", "keywords": ["（キーワード）"], "lyrics": "（歌詞）"}
}`

// modesView is the value passed to the mode listing.
type modesView struct {
	baseView
	Modes []assets.Mode
}

// modeDetailView is the value passed to one mode's detail.
type modeDetailView struct {
	baseView
	Mode assets.Mode
	// Prompt is the assembled text. On failure it is empty and Error is set.
	Prompt string
	Error  string
}

// ModeDetail shows one mode's prompt text.
//
// It goes through the same assembly as generation. Partials are expanded too, so what is
// shown here is exactly what reaches Gemini. It is the page for tracing which part of the
// instructions made the output differ from what was expected.
func (h *Handler) ModeDetail(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "mode")

	// Only keys in the listing are accepted. Generate rejects an unknown key with
	// ErrUnknownMode, but by then it looks like a 500.
	mode, ok := h.findMode(key)
	if !ok {
		http.Error(w, "そのモードはありません", http.StatusNotFound)
		return
	}

	view := modeDetailView{baseView: h.base(r), Mode: mode}

	// The input type is in the front matter. It used to try plain text and, on failure,
	// retry with a recipe, but guessing makes a real failure look like
	// "just the wrong type" and hides it. Which one to pass is known from the start.
	sample := samplePlaceholder
	if mode.NeedsRecipe() {
		sample = sampleRecipe
	}
	prompt, err := h.renderer.Generate(key, sample)
	if err != nil {
		slog.WarnContext(r.Context(), "failed to assemble the prompt", "mode", key, "error", err)
		view.Error = err.Error()
	}
	view.Prompt = prompt

	h.renderTemplate(w, http.StatusOK, "mode_detail.html", &view)
}

// findMode finds the mode for a key.
func (h *Handler) findMode(key string) (assets.Mode, bool) {
	for _, mode := range h.modes {
		if mode.Key == key {
			return mode, true
		}
	}
	return assets.Mode{}, false
}

// Modes returns the instruction modes that can be chosen.
func (h *Handler) Modes(w http.ResponseWriter, r *http.Request) {
	if respond.WantsJSON(w, r) {
		modes := make([]apiMode, 0, len(h.modes))
		for _, mode := range h.modes {
			modes = append(modes, apiMode{
				Key: mode.Key, Label: mode.DisplayName(),
				Direction: mode.Direction, UseWhen: mode.UseWhen,
			})
		}
		respond.JSON(w, r, http.StatusOK, modes)
		return
	}
	h.renderTemplate(w, http.StatusOK, "modes.html", &modesView{baseView: h.base(r), Modes: h.modes})
}

// apiMode is one entry of the mode listing.
type apiMode struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	UseWhen   string `json:"use_when"`
}
