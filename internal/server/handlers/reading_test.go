package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestPreviewReadingMarksChangedLines verifies that lines whose spelling changed in conversion
// are marked.
//
// The mark points at the lines worth checking. Even with 30 lines in a script, if only 2 changed,
// reading those is enough. Without the mark every line has to be read by eye.
func TestPreviewReadingMarksChangedLines(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &savingRepo{})
	h.reading = fakeReading{}

	rec := postJSON(t, h.PreviewReading, "/preview-reading",
		`{"lines":[{"text":"水面"},{"text":"カタカナ"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got readingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(got.Lines))
	}
	if got.Lines[0].Reading != "スイメン" || !got.Lines[0].Changed {
		t.Errorf("changed line is not marked: %+v", got.Lines[0])
	}
	if got.Lines[1].Changed {
		t.Errorf("unchanged line is marked: %+v", got.Lines[1])
	}
	// The original text is returned too. Without it side by side, you cannot see what changed.
	if got.Lines[0].Text != "水面" {
		t.Errorf("original text is missing: %+v", got.Lines[0])
	}
}

// TestPreviewReadingRejectsEmptyAndOversized verifies the input boundaries.
func TestPreviewReadingRejectsEmptyAndOversized(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &savingRepo{})
	h.reading = fakeReading{}

	if rec := postJSON(t, h.PreviewReading, "/preview-reading", `{"lines":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty: status = %d, want 400", rec.Code)
	}

	var b strings.Builder
	b.WriteString(`{"lines":[`)
	for i := 0; i <= maxScriptLines; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"text":"あ"}`)
	}
	b.WriteString(`]}`)
	if rec := postJSON(t, h.PreviewReading, "/preview-reading", b.String()); rec.Code != http.StatusBadRequest {
		t.Errorf("over the limit: status = %d, want 400", rec.Code)
	}
}
