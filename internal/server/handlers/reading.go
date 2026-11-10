package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shouni/go-serve-kit/respond"

	"github.com/shouni/ap-voice/internal/domain"
)

// Reading checks are used by both people and machines. The page sends the table being edited as is,
// and a machine sends the script it has. There is no matching page, so there is one URL, but
// it is not machine-only, so it does not sit under /api.

// readingRequest is the request of POST /reading/preview.
type readingRequest struct {
	// Lines are the lines to check. The shape accepts a script as is.
	Lines []domain.ScriptLine `json:"lines"`
}

// readingLine is the reading of one line.
type readingLine struct {
	Text string `json:"text"`
	// Reading is the katakana actually read at synthesis.
	Reading string `json:"reading"`
	// Changed reports whether conversion changed the spelling. It marks the lines worth checking;
	// a katakana-only line is unchanged by conversion, so it is false.
	Changed bool `json:"changed"`
}

// readingResponse is the response of POST /reading/preview.
type readingResponse struct {
	Lines []readingLine `json:"lines"`
}

// PreviewReading returns, per line, how it would be read if synthesised. It does not synthesise.
//
// Readings are not obvious. How words like 「田中」 or 「同姓同名」 are read could not be
// known until synthesised and heard. Since that would be noticed only after spending the script's
// length in synthesis time, this lets it be checked beforehand.
//
// A word read other than intended can be fixed by writing that part in katakana.
func (h *Handler) PreviewReading(w http.ResponseWriter, r *http.Request) {
	var body readingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "JSONの解釈に失敗しました: "+err.Error())
		return
	}
	if len(body.Lines) == 0 {
		respond.ErrorJSON(w, r, http.StatusBadRequest, "lines が空です")
		return
	}
	if len(body.Lines) > maxScriptLines {
		respond.ErrorJSON(w, r, http.StatusBadRequest,
			fmt.Sprintf("行が多すぎます（%d 行、上限 %d 行）", len(body.Lines), maxScriptLines))
		return
	}

	out := make([]readingLine, 0, len(body.Lines))
	for _, line := range body.Lines {
		reading, err := h.reading.ConvertToReading(line.Text)
		if err != nil {
			respond.ServerErrorJSON(w, r, http.StatusInternalServerError, err, "読み変換に失敗しました")
			return
		}
		out = append(out, readingLine{
			Text: line.Text, Reading: reading, Changed: reading != line.Text,
		})
	}
	respond.JSON(w, r, http.StatusOK, readingResponse{Lines: out})
}
