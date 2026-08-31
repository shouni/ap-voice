package handlers

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/shouni/ap-voice/assets"
)

// TestPageScriptsExist は、画面に割り当てたスクリプトが実在することを検証します。
//
// 対応がテンプレートから Go へ移ったので、テンプレートの /static/... を見ている
// assets 側のガードはこれを見られません。ファイル名を変えて表を直し忘れると、
// その画面だけ静かに 404 を引きます（画面は開くので、開いた人にも気付けません）。
func TestPageScriptsExist(t *testing.T) {
	t.Parallel()

	if len(pageScripts) == 0 {
		t.Fatal("pageScripts が空です")
	}

	for page, scripts := range pageScripts {
		for _, src := range scripts {
			name := path.Join("static", strings.TrimPrefix(src, "/static/"))
			if _, err := fs.Stat(assets.StaticFiles, name); err != nil {
				t.Errorf("%s が読む %s がありません: %v", page, src, err)
			}
		}
	}
}

// TestPageScriptsCoverEveryTemplate は、表のキーが実在する画面であることを検証します。
// 画面の名前を変えたときに、対応だけが古い名前で残るのを防ぎます。
func TestPageScriptsCoverEveryTemplate(t *testing.T) {
	t.Parallel()

	pages := parseTemplates(t)
	for page := range pageScripts {
		if _, ok := pages[page]; !ok {
			t.Errorf("pageScripts の %q という画面はありません", page)
		}
	}
}

// TestRenderTemplateLoadsThePageScripts は、画面ごとのスクリプトが実際に
// 描画結果へ入ることを検証します。
//
// 対応表・view の JS・レイアウトの読み込みという 3 つが繋がっていないと、
// 画面は普通に開いたまま振る舞いだけが消えます（テンプレートの define が
// 抜けていた頃と同じ壊れ方です）。
func TestRenderTemplateLoadsThePageScripts(t *testing.T) {
	t.Parallel()

	h := &Handler{templates: parseTemplates(t)}

	t.Run("固有のスクリプトを持つ画面", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.renderTemplate(rec, http.StatusOK, "detail.html", &detailView{
			JobID: "voice-1", Speakers: []string{"ずんだもん"},
		})

		body := rec.Body.String()
		for _, want := range []string{
			`<script src="/static/js/script_editor.js" defer></script>`,
			`<script src="/static/js/job_status.js" defer></script>`,
			// 全画面共通のものはレイアウトが読みます。
			`<script src="/static/js/app.js" defer></script>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%q が読み込まれていません", want)
			}
		}
	})

	t.Run("固有のスクリプトを持たない画面", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.renderTemplate(rec, http.StatusOK, "history.html", &historyView{})

		body := rec.Body.String()
		if !strings.Contains(body, `<script src="/static/js/app.js" defer></script>`) {
			t.Error("共通のスクリプトが読み込まれていません")
		}
		if strings.Contains(body, "script_editor.js") {
			t.Error("他の画面のスクリプトが混ざっています")
		}
	})
}
