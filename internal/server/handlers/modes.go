package handlers

import (
	"log/slog"
	"net/http"

	"github.com/shouni/ap-voice/assets"
)

// samplePlaceholder は、カタログでプロンプトを組み立てるときの仮の入力です。
// 実物の記事を入れる必要はありません。見せたいのは差し込み位置と指示文だからです。
const samplePlaceholder = "（ここに入力ソースの本文が入ります）"

// sampleRecipe は、楽曲紹介モードを組み立てるための仮のレシピです。
//
// **このモードだけ入力の型が違います**（ap-comp の recipe.json を読みます）。
// 素のテキストを渡すとデコードで落ちるため、カタログでは形の合う値を与えます。
const sampleRecipe = `{
  "title": "（曲名）", "theme": "（テーマ）", "mood": "（ムード）", "tempo": 120,
  "key": "C major", "instruments": ["（編成）"],
  "sections": [{"name": "Intro", "duration": 8}],
  "lyrics": {"hook": "（フック）", "narrative": "（物語）", "keywords": ["（キーワード）"], "lyrics": "（歌詞）"}
}`

// modeCard は、カタログに並べる 1 モードです。
type modeCard struct {
	assets.Mode
	// Prompt は組み立て済みのプロンプト本文です。組み立てに失敗した場合は空になり、
	// 代わりに Error が入ります。**カタログのために一覧全体を落としません。**
	Prompt string
	Error  string
}

// modesView はカタログ画面に渡す値です。
type modesView struct {
	baseView
	Modes []modeCard
}

// Modes は、選べるモードを一覧にします。
//
// 投入フォームの選択肢は 1 つずつしか読めず、説明も 1 行です。**どのモードが
// 何に向くかを見比べる場所**が別に要ります。プロンプト本文まで見せるのは、
// 出力が期待と違ったときに、指示文のどこがそう言わせたのかを追えるようにするためです。
func (h *Handler) Modes(w http.ResponseWriter, r *http.Request) {
	cards := make([]modeCard, 0, len(h.modes))
	for _, mode := range h.modes {
		card := modeCard{Mode: mode}

		// **モード名をここに書きません。** 入力の型が違うモードは
		// adapters 側が 1 箇所だけ名指ししており、二重に持つと片方だけ古くなります。
		// 素のテキストで組み立てられなければ、レシピ形式で試し直します。
		prompt, err := h.renderer.Generate(mode.Key, samplePlaceholder)
		if err != nil {
			prompt, err = h.renderer.Generate(mode.Key, sampleRecipe)
		}
		if err != nil {
			// 1 つ組み立てられなくても一覧は出します。どのモードが壊れているかが
			// 分かる方が、画面ごと 500 になるより役に立ちます。
			slog.WarnContext(r.Context(), "カタログのプロンプト組み立てに失敗しました", "mode", mode.Key, "error", err)
			card.Error = err.Error()
		}
		card.Prompt = prompt

		cards = append(cards, card)
	}

	h.renderTemplate(w, http.StatusOK, "modes.html", modesView{baseView: h.base(r), Modes: cards})
}
