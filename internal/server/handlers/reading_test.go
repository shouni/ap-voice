package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestPreviewReadingMarksChangedLines は、変換で表記が変わった行に印が付くことを
// 検証します。
//
// 確かめる価値がある行の目印です。台本が 30 行あっても、変わったのが 2 行なら
// そこだけ読めば済みます。印が無いと全行を目で追うことになります。
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
		t.Fatalf("応答のデコードに失敗しました: %v", err)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("行数 = %d, want 2", len(got.Lines))
	}
	if got.Lines[0].Reading != "スイメン" || !got.Lines[0].Changed {
		t.Errorf("変わった行に印がありません: %+v", got.Lines[0])
	}
	if got.Lines[1].Changed {
		t.Errorf("変わっていない行に印が付いています: %+v", got.Lines[1])
	}
	// 元のテキストも返します。並べて読めないと、どこが変わったか分かりません。
	if got.Lines[0].Text != "水面" {
		t.Errorf("元のテキストが欠けています: %+v", got.Lines[0])
	}
}

// TestPreviewReadingRejectsEmptyAndOversized は、入力の境界を検証します。
func TestPreviewReadingRejectsEmptyAndOversized(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &savingRepo{})
	h.reading = fakeReading{}

	if rec := postJSON(t, h.PreviewReading, "/preview-reading", `{"lines":[]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("空: status = %d, want 400", rec.Code)
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
		t.Errorf("上限超過: status = %d, want 400", rec.Code)
	}
}
