package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/ap-voice/assets"
)

// stubRenderer は、渡されたモードと入力を記録する PromptRenderer です。
type stubRenderer struct {
	// recipeOnly は、素のテキストを拒みレシピだけを受け付けるモードです。
	recipeOnly string
	got        []string
}

func (s *stubRenderer) Generate(mode, content string) (string, error) {
	s.got = append(s.got, mode)
	if mode == s.recipeOnly && !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return "", errors.New("楽曲レシピのJSONデコードに失敗しました")
	}
	return "組み立て済み: " + mode, nil
}

// TestModesRetriesWithRecipeForRecipeOnlyModes は、素のテキストで組み立てられない
// モードがレシピ形式で試し直されることを検証します。
//
// **カタログにモード名を書かないための仕組みです。** 入力の型が違うモードは
// adapters が 1 箇所だけ名指ししており、画面側にも書くと片方だけ古くなります。
func TestModesRetriesWithRecipeForRecipeOnlyModes(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{recipeOnly: "music_promo"}
	h := &Handler{
		templates: parseTemplates(t),
		renderer:  renderer,
		modes: []assets.Mode{
			{Key: "tech_solo", ModeMetadata: assets.ModeMetadata{Label: "技術解説・ひとり語り"}},
			{Key: "music_promo", ModeMetadata: assets.ModeMetadata{Label: "楽曲紹介"}},
		},
	}

	body := renderModes(t, h)

	// レシピ専用モードも本文が出ていること（再試行が効いている）。
	if !strings.Contains(body, "組み立て済み: music_promo") {
		t.Errorf("レシピ専用モードの本文が出ていません")
	}
	if strings.Contains(body, "組み立てに失敗しました") {
		t.Errorf("再試行が効かずエラー表示になっています")
	}
	// 1 回目（素のテキスト）と 2 回目（レシピ）で 2 度呼ばれます。
	if got := strings.Count(strings.Join(renderer.got, ","), "music_promo"); got != 2 {
		t.Errorf("music_promo の呼び出し回数 = %d, want 2", got)
	}
}

// TestModesKeepsListingWhenOneModeFails は、1 つ組み立てられなくても
// 一覧が出ることを検証します。**画面ごと 500 になるより、どれが壊れているかが
// 分かる方が役に立ちます。**
func TestModesKeepsListingWhenOneModeFails(t *testing.T) {
	t.Parallel()

	h := &Handler{
		templates: parseTemplates(t),
		renderer:  &brokenRenderer{},
		modes: []assets.Mode{
			{Key: "tech_solo", ModeMetadata: assets.ModeMetadata{Label: "技術解説・ひとり語り"}},
		},
	}

	body := renderModes(t, h)
	if !strings.Contains(body, "tech_solo") {
		t.Error("失敗したモードが一覧から消えています")
	}
	if !strings.Contains(body, "組み立てに失敗しました") {
		t.Error("失敗の表示がありません")
	}
}

type brokenRenderer struct{}

func (brokenRenderer) Generate(string, string) (string, error) {
	return "", errors.New("テンプレートが壊れています")
}

// renderModes はカタログ画面を描画して本文を返します。
func renderModes(t *testing.T, h *Handler) string {
	t.Helper()

	rec := httptest.NewRecorder()
	h.Modes(rec, httptest.NewRequest("GET", "/modes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
