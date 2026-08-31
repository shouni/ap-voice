package handlers

// 選択肢を返す API です。
//
// 投入する側は、何を指定できるのかを知る手段が要ります。ブラウザは
// <select> を見て選べますが、JSON で叩く呼び出し側（MCP サーバー経由のエージェント）は
// 一覧を取れないと当てずっぽうになり、実在しない値を送って 400 を繰り返します。

import (
	"net/http"

	"github.com/shouni/go-serve-kit/respond"
)

// APISpeakers は、話者ごとに使えるスタイルを返します。
//
// これが無いと、クライアントは当てずっぽうで台本を書くことになります。
// 話者ごとに持つスタイルは違い（春日部つむぎは 1 つ、ずんだもんは 8 つ）、
// 実在しない組み合わせは保存時に弾かれます。組を選ぶ材料をここで渡します。
func (h *Handler) APISpeakers(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, r, http.StatusOK, h.styles)
}

// apiMode は、モード一覧の 1 件です。
type apiMode struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	UseWhen   string `json:"use_when"`
}
