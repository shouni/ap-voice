package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-voice/assets"
)

// stubRenderer is a PromptRenderer that records the modes and inputs it is given.
type stubRenderer struct {
	// recipeOnly is a mode that rejects plain text and accepts only a recipe.
	recipeOnly string
	got        []string
}

func (s *stubRenderer) Generate(mode, content string) (string, error) {
	s.got = append(s.got, mode)
	if mode == s.recipeOnly && !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return "", errors.New("failed to decode the music recipe JSON")
	}
	return "組み立て済み: " + mode, nil
}

// testModesHandler returns a Handler with the given modes and assembly.
func testModesHandler(t *testing.T, renderer PromptRenderer, modes ...assets.Mode) *Handler {
	t.Helper()
	return &Handler{templates: parseTemplates(t), renderer: renderer, modes: modes}
}

// TestModesListDoesNotAssemblePrompts verifies that the listing does not assemble prompts.
//
// The listing needs only the front matter. The bodies of 7 modes add up to more than 10,000
// characters, so assembling what may never be read every time only makes the page heavier.
// The detail page needs the body, and for machine readers such as MCP, fetching the index and
// the bodies separately wastes less.
func TestModesListDoesNotAssemblePrompts(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "tech_solo", Label: "技術解説・ひとり語り"},
		assets.Mode{Key: "music_promo",
			Label: "楽曲紹介", Input: assets.InputRecipe},
	)

	body := render(t, h.Modes, "/modes")

	if len(renderer.got) != 0 {
		t.Errorf("the listing assembles prompts %d times", len(renderer.got))
	}
	// The input type is listed too, since it decides which tab of the create page the mode appears on.
	for _, want := range []string{"tech_solo", "楽曲紹介", `href="/modes/tech_solo"`, "テキスト", "楽曲レシピ"} {
		if !strings.Contains(body, want) {
			t.Errorf("the listing lacks %q", want)
		}
	}
}

// TestModeDetailPicksTheSampleFromFrontMatter verifies that a stand-in input matching the input
// type is picked in one go.
//
// It used to try plain text and, on failure, retry with a recipe.
// Guessing makes a real assembly failure look like "just the wrong type" and hides it.
// The front matter's input knows from the start which one to pass.
func TestModeDetailPicksTheSampleFromFrontMatter(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{recipeOnly: "music_promo"}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "music_promo",
			Label: "楽曲紹介", Input: assets.InputRecipe},
	)

	body := renderDetailFor(t, h, "music_promo")

	if !strings.Contains(body, "組み立て済み: music_promo") {
		t.Error("the body is not shown")
	}
	if strings.Contains(body, "組み立てに失敗しました") {
		t.Error("an error is shown")
	}
	// Just once. The type is known, so there is no need to try and miss.
	if len(renderer.got) != 1 {
		t.Errorf("call count = %d, want 1", len(renderer.got))
	}
}

// TestModeDetailShowsTheInputKind verifies that the detail page shows the input type.
//
// The type decides which tab of the create page it appears on, so whoever reads the catalogue
// needs to be able to tell "where is this mode submitted from".
func TestModeDetailShowsTheInputKind(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{recipeOnly: "music_promo"}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "music_promo",
			Label: "楽曲紹介", Input: assets.InputRecipe},
		assets.Mode{Key: "tech_solo", Label: "ひとり語り"},
	)

	recipe := renderDetailFor(t, h, "music_promo")
	if !strings.Contains(recipe, "楽曲レシピ") || !strings.Contains(recipe, "ジョブID") {
		t.Error("the detail page does not say it takes a recipe")
	}

	text := renderDetailFor(t, h, "tech_solo")
	if !strings.Contains(text, "入力ソース") {
		t.Error("the detail page does not say it takes text")
	}
	if strings.Contains(text, "ジョブID") {
		t.Error("a text-input mode shows the job ID guidance")
	}
}

// TestModeDetailShowsFailureWithoutBreakingThePage verifies that the mode's description stays
// readable even when assembly fails.
func TestModeDetailShowsFailureWithoutBreakingThePage(t *testing.T) {
	t.Parallel()

	h := testModesHandler(t, &brokenRenderer{},
		assets.Mode{Key: "tech_solo",
			Label: "技術解説・ひとり語り", Direction: "ずんだもんが 1 人で語ります"},
	)

	body := renderDetailFor(t, h, "tech_solo")
	if !strings.Contains(body, "ずんだもんが 1 人で語ります") {
		t.Error("the description has vanished too")
	}
	if !strings.Contains(body, "組み立てに失敗しました") {
		t.Error("no failure is shown")
	}
}

// TestModeDetailRejectsUnknownKey verifies that a key not in the listing is a 404.
// Generate rejects unknown modes too, but by then it looks like a 500.
func TestModeDetailRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	h := testModesHandler(t, &stubRenderer{},
		assets.Mode{Key: "tech_solo", Label: "技術解説"},
	)

	rec := httptest.NewRecorder()
	h.ModeDetail(rec, withModeParam(httptest.NewRequest("GET", "/modes/存在しない", nil), "存在しない"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

type brokenRenderer struct{}

func (brokenRenderer) Generate(string, string) (string, error) {
	return "", errors.New("the template is broken")
}

// render calls the handler and returns the body.
func render(t *testing.T, h http.HandlerFunc, path string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// withModeParam returns a request carrying chi's URL parameter.
func withModeParam(r *http.Request, mode string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("mode", mode)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}

// renderDetailFor renders the given mode's detail and returns the body.
func renderDetailFor(t *testing.T, h *Handler, mode string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ModeDetail(rec, withModeParam(httptest.NewRequest("GET", "/modes/"+mode, nil), mode))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
