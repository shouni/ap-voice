package handlers

import (
	"net/http"

	"github.com/shouni/go-serve-kit/respond"
)

// Speakers は、話者ごとに使えるスタイルを返します（GET /speakers）。
//
// これが無いと、クライアントは当てずっぽうで台本を書くことになります。
// 話者ごとに持つスタイルは違い（春日部つむぎは 1 つ、ずんだもんは 8 つ）、
// 実在しない組み合わせは保存時に弾かれます。組を選ぶ材料をここで渡します。
func (h *Handler) Speakers(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, r, http.StatusOK, h.styles)
}
