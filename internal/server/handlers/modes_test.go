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
// 一覧に要るのは front matter だけです。7 モード分の本文は合わせて 1 万字を
// 超えるため、読むか分からないものを毎回組み立てるとページが重くなるだけです。
// 本文が要るのは詳細で、MCP のように機械が読む場合も、索引と本文を別々に
// 取れる方が無駄がありません。
func TestModesListDoesNotAssemblePrompts(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "tech_solo", Label: "技術解説・ひとり語り"},
		assets.Mode{Key: "music_promo",
			Label: "楽曲紹介", Input: assets.InputRecipe},
	)

	body := render(t, h.Modes, "/modes")

	if len(renderer.got) != 0 {
		t.Errorf("一覧でプロンプトを %d 回組み立てています", len(renderer.got))
	}
	// 入力の型も一覧に出ます。作成画面のどのタブに並ぶかがこれで決まるためです。
	for _, want := range []string{"tech_solo", "楽曲紹介", `href="/modes/tech_solo"`, "テキスト", "楽曲レシピ"} {
		if !strings.Contains(body, want) {
			t.Errorf("一覧に %q が出ていません", want)
		}
	}
}

// TestModeDetailPicksTheSampleFromFrontMatter は、入力の型に合った仮の入力が
// 一度で選ばれることを検証します。
//
// 以前は素のテキストで試し、失敗したらレシピで試し直していました。
// 当てずっぽうだと、本当の組み立て失敗も「型が違っただけ」に見えて隠れます。
// どちらを渡すかは front matter の input が最初から知っています。
func TestModeDetailPicksTheSampleFromFrontMatter(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{recipeOnly: "music_promo"}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "music_promo",
			Label: "楽曲紹介", Input: assets.InputRecipe},
	)

	body := renderDetailFor(t, h, "music_promo")

	if !strings.Contains(body, "組み立て済み: music_promo") {
		t.Error("本文が出ていません")
	}
	if strings.Contains(body, "組み立てに失敗しました") {
		t.Error("エラー表示になっています")
	}
	// 1 回だけです。型が分かっているので、外して試す必要がありません。
	if len(renderer.got) != 1 {
		t.Errorf("呼び出し回数 = %d, want 1", len(renderer.got))
	}
}

// TestModeDetailShowsTheInputKind は、詳細に入力の型が出ることを検証します。
//
// 型が作成画面のどのタブに出るかを決めるため、カタログを見た人が
// 「このモードはどこから投げるのか」を判断できる必要があります。
func TestModeDetailShowsTheInputKind(t *testing.T) {
	t.Parallel()

	renderer := &stubRenderer{recipeOnly: "music_promo"}
	h := testModesHandler(t, renderer,
		assets.Mode{Key: "music_promo",
			Label: "楽曲紹介", Input: assets.InputRecipe},
		assets.Mode{Key: "tech_solo", Label: "ひとり語り"},
	)

	recipe := renderDetailFor(t, h, "music_promo")
	if !strings.Contains(recipe, "楽曲レシピ") || !strings.Contains(recipe, "ジョブID") {
		t.Error("レシピ入力であることが詳細に出ていません")
	}

	text := renderDetailFor(t, h, "tech_solo")
	if !strings.Contains(text, "入力ソース") {
		t.Error("テキスト入力であることが詳細に出ていません")
	}
	if strings.Contains(text, "ジョブID") {
		t.Error("テキスト入力なのにジョブIDの案内が出ています")
	}
}

// TestModeDetailShowsFailureWithoutBreakingThePage は、組み立てに失敗しても
// そのモードの説明は読めることを検証します。
func TestModeDetailShowsFailureWithoutBreakingThePage(t *testing.T) {
	t.Parallel()

	h := testModesHandler(t, &brokenRenderer{},
		assets.Mode{Key: "tech_solo",
			Label: "技術解説・ひとり語り", Direction: "ずんだもんが 1 人で語ります"},
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
		assets.Mode{Key: "tech_solo", Label: "技術解説"},
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
