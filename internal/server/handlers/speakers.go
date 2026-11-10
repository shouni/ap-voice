package handlers

import (
	"net/http"

	"github.com/shouni/go-serve-kit/respond"
)

// Speakers returns the styles each speaker can use (GET /speakers).
//
// Without it, a client would be writing scripts by guesswork.
// Each speaker has different styles (春日部つむぎ has 1, ずんだもん has 8),
// and a pair that does not exist is rejected on save. This hands over what is needed to choose a pair.
func (h *Handler) Speakers(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, r, http.StatusOK, h.styles)
}
