package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// TestAPISpeakersListsStylesPerSpeaker verifies that only styles that exist for each speaker
// are returned.
//
// Without it, a client would write scripts by guesswork. The styles differ
// per speaker (春日部つむぎ has 1, ずんだもん several), and a non-existent pair
// is rejected on save, so this is the only place to learn which pairs to choose.
func TestSpeakersListsStylesPerSpeaker(t *testing.T) {
	t.Parallel()

	h := builtHandler(t)

	rec := httptest.NewRecorder()
	h.Speakers(rec, httptest.NewRequest(http.MethodGet, "/speakers", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not readable as JSON: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no speakers returned (the table built at construction is empty)")
	}

	styles, ok := got["春日部つむぎ"]
	if !ok {
		t.Fatalf("春日部つむぎ is missing (speakers returned: %d)", len(got))
	}
	if !slices.Contains(styles, "ノーマル") {
		t.Errorf("春日部つむぎ styles = %v, want them to include ノーマル", styles)
	}

	// One case pins that they differ per speaker. Handing everyone the same list would
	// make non-existent pairs selectable again.
	if zundamon := got["ずんだもん"]; len(zundamon) <= len(styles) {
		t.Errorf("ずんだもん = %v, 春日部つむぎ = %v; not narrowed per speaker", zundamon, styles)
	}
}

// TestAPISpeakersServesTheSameTableAsTheEditor verifies that the table handed to the editor
// and the API response are the same thing.
//
// Built separately, one of them would start offering non-existent pairs.
func TestSpeakersServesTheSameTableAsTheEditor(t *testing.T) {
	t.Parallel()

	h := builtHandler(t)

	var fromJSON map[string][]string
	if err := json.Unmarshal([]byte(h.stylesJSON), &fromJSON); err != nil {
		t.Fatalf("the table handed to the page is not readable as JSON: %v", err)
	}

	rec := httptest.NewRecorder()
	h.Speakers(rec, httptest.NewRequest(http.MethodGet, "/speakers", nil))

	var fromAPI map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &fromAPI); err != nil {
		t.Fatalf("the API response is not readable as JSON: %v", err)
	}

	if len(fromJSON) != len(fromAPI) {
		t.Fatalf("speaker counts differ: page %d, API %d", len(fromJSON), len(fromAPI))
	}
	for name, styles := range fromAPI {
		if !slices.Equal(fromJSON[name], styles) {
			t.Errorf("styles for %s differ: page %v, API %v", name, fromJSON[name], styles)
		}
	}
}
