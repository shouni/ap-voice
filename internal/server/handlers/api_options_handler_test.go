package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
)

// passthroughRenderer と passthroughReading は、選択肢の検証に関係しない依存を
// 埋めるだけのものです（プロンプトと読みはここでは見ません）。
type passthroughRenderer struct{}

func (passthroughRenderer) Generate(_, content string) (string, error) { return content, nil }

type passthroughReading struct{}

func (passthroughReading) ConvertToReading(text string) (string, error) { return text, nil }

// builtHandler は、実物の NewHandler を通した Handler を返します。
//
// 他のテストのように構造体を直に組むと、構築時にしか行わない組み立て
// （話者ごとのスタイル表）が空のまま通ってしまいます。
func builtHandler(t *testing.T) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	modes, err := assets.LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	h, err := NewHandler(HandlerOptions{
		Queue:     &capturingQueue{},
		Templates: parseTemplates(t),
		Modes:     modes,
		Models:    []string{"gemini-test"},
		Bucket:    "test",
		Repo:      &savingRepo{},
		Speakers:  registry,
		Renderer:  passthroughRenderer{},
		Reading:   passthroughReading{},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return h
}

// TestAPISpeakersListsStylesPerSpeaker は、話者ごとに実在するスタイルだけが
// 返ることを検証します。
//
// これが無いと、クライアントは当てずっぽうで台本を書くことになります。持つ
// スタイルは話者ごとに違い（春日部つむぎは 1 つ、ずんだもんは複数）、実在しない
// 組み合わせは保存時に弾かれるので、組を選ぶ材料がここにしかありません。
func TestAPISpeakersListsStylesPerSpeaker(t *testing.T) {
	t.Parallel()

	h := builtHandler(t)

	rec := httptest.NewRecorder()
	h.APISpeakers(rec, httptest.NewRequest(http.MethodGet, "/api/speakers", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答が JSON として読めません: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("話者が 1 人も返っていません（構築時の組み立てが空のまま）")
	}

	styles, ok := got["春日部つむぎ"]
	if !ok {
		t.Fatalf("春日部つむぎ がいません（返った話者数: %d）", len(got))
	}
	if !slices.Contains(styles, "ノーマル") {
		t.Errorf("春日部つむぎ のスタイル = %v, want ノーマル を含む", styles)
	}

	// 話者ごとに違うことを 1 件で押さえます。同じ一覧を全員へ配ると、
	// 実在しない組み合わせを選べる状態に戻ります。
	if zundamon := got["ずんだもん"]; len(zundamon) <= len(styles) {
		t.Errorf("ずんだもん = %v, 春日部つむぎ = %v。話者ごとに絞れていません", zundamon, styles)
	}
}

// TestAPISpeakersServesTheSameTableAsTheEditor は、編集画面へ渡す対応表と
// API の応答が同じものであることを検証します。
//
// 別々に組むと、片方だけが実在しない組み合わせを出すようになります。
func TestAPISpeakersServesTheSameTableAsTheEditor(t *testing.T) {
	t.Parallel()

	h := builtHandler(t)

	var fromJSON map[string][]string
	if err := json.Unmarshal([]byte(h.stylesJSON), &fromJSON); err != nil {
		t.Fatalf("画面へ渡す対応表が JSON として読めません: %v", err)
	}

	rec := httptest.NewRecorder()
	h.APISpeakers(rec, httptest.NewRequest(http.MethodGet, "/api/speakers", nil))

	var fromAPI map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &fromAPI); err != nil {
		t.Fatalf("API の応答が JSON として読めません: %v", err)
	}

	if len(fromJSON) != len(fromAPI) {
		t.Fatalf("話者数が違います: 画面 %d, API %d", len(fromJSON), len(fromAPI))
	}
	for name, styles := range fromAPI {
		if !slices.Equal(fromJSON[name], styles) {
			t.Errorf("%s のスタイルが違います: 画面 %v, API %v", name, fromJSON[name], styles)
		}
	}
}
