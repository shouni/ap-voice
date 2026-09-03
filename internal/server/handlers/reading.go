package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/shouni/go-serve-kit/respond"

	"github.com/shouni/ap-voice/internal/domain"
)

// 読みの確認は、人と機械の両方が使います。画面は編集中の表をそのまま送り、
// 機械は持っている台本を送ります。対応するページを持たないので URL は 1 本ですが、
// 機械専用ではないため /api の下には置きません。

// readingRequest は POST /reading/preview の要求です。
type readingRequest struct {
	// Lines は確かめたい行です。台本をそのまま渡せる形にしてあります。
	Lines []domain.ScriptLine `json:"lines"`
}

// readingLine は 1 行分の読みです。
type readingLine struct {
	Text string `json:"text"`
	// Reading は合成時に実際に読まれるカタカナです。
	Reading string `json:"reading"`
	// Changed は、変換で表記が変わったかどうかです。確かめる価値がある行の目印で、
	// カタカナだけの行は変換しても変わらないため false になります。
	Changed bool `json:"changed"`
}

// readingResponse は POST /reading/preview の応答です。
type readingResponse struct {
	Lines []readingLine `json:"lines"`
}

// PreviewReading は、合成したらどう読まれるかを行ごとに返します。合成はしません。
//
// 読みは自明ではありません。「田中」「同姓同名」のような語がどう読まれるかは、
// 合成して聴くまで分かりませんでした。台本の長さぶんの合成時間を使ってから
// 気付くことになるため、その前に確かめられるようにします。
//
// 意図と違う読みになる語は、その部分をカタカナで書けば直せます。
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
			respond.ErrorJSON(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, readingLine{
			Text: line.Text, Reading: reading, Changed: reading != line.Text,
		})
	}
	respond.JSON(w, r, http.StatusOK, readingResponse{Lines: out})
}
