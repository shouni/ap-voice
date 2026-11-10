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

// TestPageScriptsExist verifies that the scripts assigned to pages exist.
//
// The mapping moved from the templates to Go, so the assets-side guard that looks at
// /static/... in templates cannot see it. Rename a file and forget to update the table,
// and that page alone quietly 404s (the page still opens, so even its visitors do not notice).
func TestPageScriptsExist(t *testing.T) {
	t.Parallel()

	if len(pageScripts) == 0 {
		t.Fatal("pageScripts is empty")
	}

	for page, scripts := range pageScripts {
		for _, src := range scripts {
			name := path.Join("static", strings.TrimPrefix(src, "/static/"))
			if _, err := fs.Stat(assets.StaticFiles, name); err != nil {
				t.Errorf("%s loads %s, which does not exist: %v", page, src, err)
			}
		}
	}
}

// TestPageScriptsCoverEveryTemplate verifies that the table's keys are pages that exist.
// It keeps the mapping from lingering under an old name when a page is renamed.
func TestPageScriptsCoverEveryTemplate(t *testing.T) {
	t.Parallel()

	pages := parseTemplates(t)
	for page := range pageScripts {
		if _, ok := pages[page]; !ok {
			t.Errorf("pageScripts names page %q, which does not exist", page)
		}
	}
}

// TestRenderTemplateLoadsThePageScripts verifies that each page's scripts actually
// end up in the rendered output.
//
// Unless the table, the view's JS and the layout's loading are all connected,
// the page opens normally and only its behaviour disappears (the same failure as when
// a template's define was missing).
func TestRenderTemplateLoadsThePageScripts(t *testing.T) {
	t.Parallel()

	h := &Handler{templates: parseTemplates(t)}

	t.Run("page with its own scripts", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.renderTemplate(rec, http.StatusOK, "detail.html", &detailView{
			JobID: "voice-1", Speakers: []string{"ずんだもん"},
		})

		body := rec.Body.String()
		for _, want := range []string{
			`<script src="/static/js/script_editor.js" defer></script>`,
			`<script src="/static/js/job_status.js" defer></script>`,
			// The layout loads what every page shares.
			`<script src="/static/js/app.js" defer></script>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%q is not loaded", want)
			}
		}
	})

	t.Run("page without its own scripts", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.renderTemplate(rec, http.StatusOK, "history.html", &historyView{})

		body := rec.Body.String()
		if !strings.Contains(body, `<script src="/static/js/app.js" defer></script>`) {
			t.Error("the shared scripts are not loaded")
		}
		if strings.Contains(body, "script_editor.js") {
			t.Error("another page's scripts are mixed in")
		}
	})
}
