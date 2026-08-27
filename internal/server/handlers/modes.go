package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/shouni/ap-voice/assets"
)

// samplePlaceholder は、詳細でプロンプトを組み立てるときの仮の入力です。
// 実物の記事を入れる必要はありません。見せたいのは差し込み位置と指示文だからです。
const samplePlaceholder = "（ここに入力ソースの本文が入ります）"

// sampleRecipe は、入力の型が違うモードを組み立てるための仮のレシピです。
//
// 楽曲紹介だけは ap-comp の recipe.json を読みます。素のテキストを渡すと
// デコードで落ちるため、形の合う値を用意します。
const sampleRecipe = `{
  "title": "（曲名）", "theme": "（テーマ）", "mood": "（ムード）", "tempo": 120,
  "key": "C major", "instruments": ["（編成）"],
  "sections": [{"name": "Intro", "duration": 8}],
  "lyrics": {"hook": "（フック）", "narrative": "（物語）", "keywords": ["（キーワード）"], "lyrics": "（歌詞）"}
}`

// modesView はモード一覧に渡す値です。
type modesView struct {
	baseView
	Modes []assets.Mode
}

// modeDetailView は 1 モードの詳細に渡す値です。
type modeDetailView struct {
	baseView
	Mode assets.Mode
	// Prompt は組み立て済みの本文です。失敗した場合は空になり、Error が入ります。
	Prompt string
	Error  string
}

// ModeDetail は、1 モードのプロンプト本文を見せます。
//
// **生成側と同じ組み立てを通します。** 共通部品も展開されるので、ここに見えている
// ものがそのまま Gemini へ渡ります。出力が期待と違ったとき、指示文のどこが
// そう言わせたのかを追うための画面です。
func (h *Handler) ModeDetail(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "mode")

	// **一覧に載っているキーだけを受け付けます。** 未知のキーは Generate が
	// ErrUnknownMode で弾きますが、そこまで行くと 500 相当の見え方になります。
	mode, ok := h.findMode(key)
	if !ok {
		http.Error(w, "そのモードはありません", http.StatusNotFound)
		return
	}

	view := modeDetailView{baseView: h.base(r), Mode: mode}

	// **入力の型は front matter が持っています。** 以前は素のテキストで試して
	// 失敗したらレシピで試し直していましたが、当てずっぽうだと本当の失敗も
	// 「型が違っただけ」に見えて隠れます。どちらを渡すかは最初から分かります。
	sample := samplePlaceholder
	if mode.NeedsRecipe() {
		sample = sampleRecipe
	}
	prompt, err := h.renderer.Generate(key, sample)
	if err != nil {
		slog.WarnContext(r.Context(), "プロンプトの組み立てに失敗しました", "mode", key, "error", err)
		view.Error = err.Error()
	}
	view.Prompt = prompt

	h.renderTemplate(w, http.StatusOK, "mode_detail.html", view)
}

// findMode は、キーに対応するモードを探します。
func (h *Handler) findMode(key string) (assets.Mode, bool) {
	for _, mode := range h.modes {
		if mode.Key == key {
			return mode, true
		}
	}
	return assets.Mode{}, false
}
