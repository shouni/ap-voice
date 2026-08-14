package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

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

// testModesHandler は、指定のモードと組み立てを持つ Handler を返します。
func testModesHandler(t *testing.T, renderer PromptRenderer, modes ...assets.Mode) *Handler {
	t.Helper()
	return &Handler{templates: parseTemplates(t), renderer: renderer, modes: modes}
}

// TestModesListDoesNotAssemblePrompts は、一覧がプロンプトを組み立てないことを
// 検証します。
//
// **一覧に要るのは front matter だけです。** 7 モード分の本文は合わせて 1 万字を
// 超えるため、読むか分からないものを毎回組み立てるとページが重くなるだけです。
// 本文が要るのは詳細で、MCP のように機械が読む場合も、索引と本文を別々に
// 取れる方が無駄がありません。
func TestModesListDoesNotAssemblePrompts(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "tech_solo", ModeMetadata: assets.ModeMetadata{Label: "技術解説・ひとり語り"}},
		assets.Mode{Key: "music_promo", ModeMetadata: assets.ModeMetadata{Label: "楽曲紹介"}},
	)

	body := render(t, h.Modes, "/modes")

	if len(renderer.got) != 0 {
		t.Errorf("一覧でプロンプトを %d 回組み立てています", len(renderer.got))
	}
	for _, want := range []string{"tech_solo", "楽曲紹介", `href="/modes/tech_solo"`} {
		if !strings.Contains(body, want) {
			t.Errorf("一覧に %q が出ていません", want)
		}
	}
}

// TestModeDetailRetriesWithRecipe は、素のテキストで組み立てられないモードが
// レシピ形式で試し直されることを検証します。
//
// **画面にモード名を書かないための仕組みです。** 入力の型が違うモードは
// adapters が 1 箇所だけ名指ししており、こちらにも書くと片方だけ古くなります。
func TestModeDetailRetriesWithRecipe(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{recipeOnly: "music_promo"}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "music_promo", ModeMetadata: assets.ModeMetadata{Label: "楽曲紹介"}},
	)

	body := renderDetailFor(t, h, "music_promo")

	if !strings.Contains(body, "組み立て済み: music_promo") {
		t.Error("本文が出ていません")
	}
	if strings.Contains(body, "組み立てに失敗しました") {
		t.Error("再試行が効かずエラー表示になっています")
	}
	// 1 回目（素のテキスト）と 2 回目（レシピ）で 2 度呼ばれます。
	if len(renderer.got) != 2 {
		t.Errorf("呼び出し回数 = %d, want 2", len(renderer.got))
	}
}

// TestModeDetailShowsFailureWithoutBreakingThePage は、組み立てに失敗しても
// そのモードの説明は読めることを検証します。
func TestModeDetailShowsFailureWithoutBreakingThePage(t *testing.T) {
	t.Parallel()

	h := testModesHandler(t, &brokenRenderer{},
		assets.Mode{Key: "tech_solo", ModeMetadata: assets.ModeMetadata{
			Label: "技術解説・ひとり語り", Direction: "ずんだもんが 1 人で語ります",
		}},
	)

	body := renderDetailFor(t, h, "tech_solo")
	if !strings.Contains(body, "ずんだもんが 1 人で語ります") {
		t.Error("説明まで消えています")
	}
	if !strings.Contains(body, "組み立てに失敗しました") {
		t.Error("失敗の表示がありません")
	}
}

// TestModeDetailRejectsUnknownKey は、一覧に無いキーを 404 にすることを検証します。
// Generate も未知のモードを弾きますが、そこまで行くと 500 相当の見え方になります。
func TestModeDetailRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	h := testModesHandler(t, &stubRenderer{},
		assets.Mode{Key: "tech_solo", ModeMetadata: assets.ModeMetadata{Label: "技術解説"}},
	)

	rec := httptest.NewRecorder()
	h.ModeDetail(rec, withModeParam(httptest.NewRequest("GET", "/modes/存在しない", nil), "存在しない"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

type brokenRenderer struct{}

func (brokenRenderer) Generate(string, string) (string, error) {
	return "", errors.New("テンプレートが壊れています")
}

// render はハンドラーを呼んで本文を返します。
func render(t *testing.T, h http.HandlerFunc, path string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// withModeParam は、chi の URL パラメータを載せたリクエストを返します。
func withModeParam(r *http.Request, mode string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("mode", mode)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}

// renderDetailFor は、指定モードの詳細を描画して本文を返します。
func renderDetailFor(t *testing.T, h *Handler, mode string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ModeDetail(rec, withModeParam(httptest.NewRequest("GET", "/modes/"+mode, nil), mode))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
