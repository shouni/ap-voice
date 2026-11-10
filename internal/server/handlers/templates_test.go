package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// Templates are evaluated only at run time, so the code still compiles when the name of a value
// they reference changes. Not knowing it is broken until the page is opened is their weak spot;
// a duplicated nav and a missing CSRF hidden field were actually found after deploying.
//
// Here they are read the same way the builder does, and the same types the handlers pass are fed in.
// Fed a map, a missing key becomes "<no value>" and slips through, so
// the real View structs are always used. That is what makes a renamed field detectable.

// parseTemplates reads the templates the same way builder/handlers.go does.
// Each page becomes an independent set (keyed by file name, such as "history.html").
func parseTemplates(t *testing.T) map[string]*template.Template {
	t.Helper()

	pages, err := assets.ParsePages()
	if err != nil {
		t.Fatalf("failed to load the templates: %v", err)
	}
	return pages
}

// testBaseView holds the values every page shares. The nav reads .Path and .DefaultModel.
func testBaseView(path string) baseView {
	return baseView{CSRFToken: "test-csrf-token", Path: path, DefaultModel: "gemini-test"}
}

// testModes are the modes shown on the form. One with a display name and one without are mixed
// in, to check that a prompt without front matter does not drop out of the options.
func testModes() []assets.Mode {
	return []assets.Mode{
		{Key: "promo",
			Label:     "楽曲紹介（春日部つむぎ × ずんだもん）",
			Direction: "楽曲レシピから作る宣伝ナレーション",
			UseWhen:   "recipe.json のとき",
			// The input type decides the tab. Only promo takes a recipe.
			Input: assets.InputRecipe},
		{Key: "solo"},
	}
}

// TestTemplatesRender verifies that the 3 pages render with the real View structs.
func TestTemplatesRender(t *testing.T) {
	t.Parallel()

	script := domain.Script{
		Title: "テスト台本",
		Lines: []domain.ScriptLine{
			{Speaker: "ずんだもん", Style: "ノーマル", Text: "こんにちはなのだ"},
		},
	}

	tests := []struct {
		name     string
		template string
		view     any
		// want are fragments that must appear. Merely rendering would let a page
		// with missing values through.
		want []string
		// notWant are fragments that must not appear. A button pointing at a form that does not exist
		// renders successfully, so it is caught only by saying it must be absent.
		notWant []string
	}{
		{
			name:     "submission form",
			template: "home.html",
			view: formView{
				baseView:    testBaseView("/"),
				TextModes:   assets.FilterModes(testModes(), assets.InputText),
				RecipeModes: assets.FilterModes(testModes(), assets.InputRecipe),
				Models:      []string{"gemini-test"},
				Message:     "受け付けました",
				// On re-render after submission the input stays. Clearing it would mean pasting
				// the URL again to make another one from the same source.
				Form: domain.Request{
					Command:  domain.CommandGenerate,
					InputURI: "gs://bucket/recipe.json",
					Mode:     "promo",
					AIModel:  "gemini-test",
				},
			},
			want: []string{
				`value="test-csrf-token"`,
				`value="gs://bucket/recipe.json"`,
				// The options' display names and descriptions come from the front matter.
				"楽曲紹介",
				`data-usewhen="recipe.json のとき"`,
				// A mode without a display name shows by its key (a forgotten description does not make it vanish).
				">solo<",
				`value="gemini-test" selected`,
				"受け付けました",
			},
		},
		{
			name:     "history listing",
			template: "history.html",
			view: historyView{
				baseView: testBaseView("/jobs"),
				Page:     jobstatus.PageMeta{Page: 2, TotalPages: 3, Total: 120, From: 51, To: 100, HasPrev: true, HasNext: true, PrevPage: 1, NextPage: 3},
				Filter:   "failed",
				Jobs: []repository.Job{
					{ID: "voice-1", Title: "一覧のタイトル", CreatedAt: time.Now(), HasAudio: true,
						State: jobstatus.StateSucceeded},
					// When the script could not be read, the job ID goes in the title.
					{ID: "voice-2", Title: "voice-2", CreatedAt: time.Now(), HasAudio: false,
						State: jobstatus.StateFailed},
					// A running job must be distinguishable from "script only".
					{ID: "voice-3", Title: "実行中のジョブ", CreatedAt: time.Now(),
						State: jobstatus.StateRunning},
				},
			},
			want: []string{
				"一覧のタイトル", "voice-2",
				// The filter carries over to the pager. If it dropped, page 2 would go back to everything,
				// and a filtered listing would silently widen.
				`href="/jobs?page=1&amp;state=failed"`,
				`href="/jobs?state=failed"`,
				// The state comes before artifact presence. The listing has to show whether to wait
				// or to delete and retry.
				">音声あり<", ">失敗<", ">実行中<",
				// The pager appears only with 2 or more pages.
				`href="/jobs?page=3&amp;state=failed"`, "全 120 件",
			},
		},
		{
			name:     "detail (with audio)",
			template: "detail.html",
			view: detailView{
				baseView:   testBaseView("/jobs/voice-1"),
				JobID:      "voice-1",
				Script:     script,
				HasScript:  true,
				HasAudio:   true,
				State:      jobstatus.StateSucceeded,
				MaxLines:   maxScriptLines,
				Speakers:   []string{"ずんだもん", "四国めたん"},
				StylesJSON: `{"ずんだもん":["ノーマル"]}`,
			},
			want: []string{
				"保存して音声を作り直す",
				"このジョブを削除",
				"履歴一覧へ戻る",
				`src="/jobs/voice-1/audio"`,
				// The script can be fixed, not just read.
				`action="/jobs/voice-1/synthesize"`,
				`name="title"`,
				`<textarea class="form-control form-control-sm js-text" name="text"`,
				`<option value="四国めたん"`,
				// The stored style comes back selected.
				`data-selected="ノーマル"`,
				"こんにちはなのだ",
				// Reading checks are pointless unless usable while editing.
				// The target must match the path the router registers.
				`data-endpoint="/reading/preview"`,
				"js-reading",
				// The same goes for watching a running job; it must match the router's registration.
				`data-endpoint="/jobs/voice-1"`,
				`data-state="succeeded"`,
				// Line operations. The order is the script itself, so cutting and pasting text cannot fix it.
				"js-move-up", "js-move-down", "js-add-row", "js-remove-row",
				// The line cap is passed from the server (so it is not copied into the page).
				`data-max-lines="200"`,
			},
		},
		{
			name:     "detail (no audio)",
			template: "detail.html",
			view: detailView{
				baseView:  testBaseView("/jobs/voice-2"),
				JobID:     "voice-2",
				Script:    script,
				HasScript: true,
				HasAudio:  false,
				Speakers:  []string{"ずんだもん"},
			},
			want: []string{"保存して音声を作成"},
			// For a job with no recorded input source (a supplied script), regenerating
			// always fails, so the button is not shown at all.
			notWant: []string{"同じ入力で台本を作り直す"},
		},
		{
			// A job that failed before writing a script. If this page did not open, it would sit in the
			// history undeletable (deletion is possible only from here).
			name:     "detail (no script, failed)",
			template: "detail.html",
			view: detailView{
				baseView: testBaseView("/jobs/voice-3"),
				JobID:    "voice-3",
				State:    jobstatus.StateFailed,
				JobError: "AIモデルが空のスクリプトを返しました",
				InputURI: "https://example.com/article",
				Speakers: []string{"ずんだもん"},
			},
			want: []string{
				"このジョブは失敗しました",
				// Apart from the Slack notification, this is the only place a person sees why it failed.
				"AIモデルが空のスクリプトを返しました",
				"台本はまだありません",
				"このジョブを削除",
				// What was read is in the record. There is no reason to make anyone paste it again.
				"同じ入力で台本を作り直す",
				`action="/jobs/voice-3/regenerate"`,
				"https://example.com/article",
			},
			// There is no script to fix, so neither saving, synthesis nor reading checks are possible. Leaving the
			// buttons would line up things that do nothing when pressed (the form or rows they point at are missing).
			notWant: []string{"保存して音声", "台本JSONをダウンロード", "読みを確認"},
		},
	}

	tmpl := parseTemplates(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			if err := renderInto(&buf, tmpl, tt.template, tt.view); err != nil {
				t.Fatalf("failed to render %s: %v", tt.template, err)
			}

			got := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("%s does not contain %q", tt.template, want)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("%s still contains %q", tt.template, notWant)
				}
			}
		})
	}
}

// optionPattern extracts the opening tag of the <option> with the given value.
// The attributes are split across lines, so it matches across lines.
func optionPattern(value string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<option value="` + regexp.QuoteMeta(value) + `".*?>`)
}

// TestFormKeepsSelectedMode verifies that the submitted mode comes back selected.
//
// Now that the options come from the front matter, the value (key) and the display name are
// different things. selected is decided by matching the key, so this pins that changing the
// display name does not drop the selection.
func TestFormKeepsSelectedMode(t *testing.T) {
	t.Parallel()

	tmpl := parseTemplates(t)

	var buf strings.Builder
	err := renderInto(&buf, tmpl, "home.html", formView{
		baseView:    testBaseView("/"),
		TextModes:   assets.FilterModes(testModes(), assets.InputText),
		RecipeModes: assets.FilterModes(testModes(), assets.InputRecipe),
		Models:      []string{"gemini-test"},
		Form:        domain.Request{Command: domain.CommandGenerate, Mode: "promo"},
	})
	if err != nil {
		t.Fatalf("failed to render home.html: %v", err)
	}

	got := buf.String()
	if tag := optionPattern("promo").FindString(got); !strings.Contains(tag, "selected") {
		t.Errorf("promo is not selected: %q", tag)
	}
	if tag := optionPattern("solo").FindString(got); strings.Contains(tag, "selected") {
		t.Errorf("solo is selected too: %q", tag)
	}
}

// TestTemplatesIncludeCSRFTokenInForms verifies that every form that POSTs has a CSRF
// token hidden field.
//
// Missing even one makes only that operation get rejected with 「不正なCSRFトークン」, and the
// page looks normal, so it is hard to notice. It actually happened on the submission form.
func TestTemplatesIncludeCSRFTokenInForms(t *testing.T) {
	t.Parallel()

	tmpl := parseTemplates(t)

	views := map[string]any{
		"home.html": formView{
			baseView:    testBaseView("/"),
			TextModes:   assets.FilterModes(testModes(), assets.InputText),
			RecipeModes: assets.FilterModes(testModes(), assets.InputRecipe),
			Models:      []string{"gemini-test"},
			Form:        domain.Request{Command: domain.CommandGenerate},
		},
		"detail.html": detailView{
			baseView:  testBaseView("/jobs/voice-1"),
			JobID:     "voice-1",
			Script:    domain.Script{Title: "T"},
			HasScript: true,
			HasAudio:  true,
			Speakers:  []string{"ずんだもん"},
		},
	}

	for name, view := range views {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			if err := renderInto(&buf, tmpl, name, view); err != nil {
				t.Fatalf("failed to render %s: %v", name, err)
			}

			got := buf.String()
			// One hidden field is needed per method="post". There may be one extra for the delete button
			// that sends DELETE through fetch (it reads a hidden field placed outside the forms).
			if forms, tokens := strings.Count(got, `method="post"`), strings.Count(got, `name="csrf_token"`); tokens < forms {
				t.Errorf("%s: %d POST forms but %d csrf_token fields", name, forms, tokens)
			}
		})
	}
}

// TestNavHighlightsCurrentPage verifies that the nav's current location switches with .Path.
// The nav is shared by 3 pages; it used to be copied into each file,
// which led to the home page alone missing the history link.
func TestNavHighlightsCurrentPage(t *testing.T) {
	t.Parallel()

	tmpl := parseTemplates(t)

	for _, path := range []string{"/", "/jobs"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			if err := tmpl["home.html"].ExecuteTemplate(&buf, "nav", testBaseView(path)); err != nil {
				t.Fatalf("failed to render the nav: %v", err)
			}

			got := buf.String()
			// Both links appear from every page.
			if !strings.Contains(got, `href="/"`) || !strings.Contains(got, `href="/jobs"`) {
				t.Errorf("the nav is missing links: %s", got)
			}
			if want := `href="` + path + `"`; !strings.Contains(got, `aria-current="page" `+want) {
				t.Errorf("current location %s is not highlighted", path)
			}
		})
	}
}

// TestDetailAlwaysEmitsParsableStyles verifies that the page does not fail JSON.parse even when the
// speaker → style table is empty.
//
// It used to be embedded with an inline script, `window.apVoiceStyles = {{ .StylesJSON }};`,
// and an empty value gave `= ;`, breaking not just that line but all loading after it.
// To limit the CSP's script-src to 'self', the inline script was dropped for a data attribute
// (template.JS passes </script> through, so the attribute context is also safer).
// The failure mode changed, but the property to keep is the same: "emit a valid value even when empty".
func TestDetailAlwaysEmitsParsableStyles(t *testing.T) {
	t.Parallel()

	tmpl := parseTemplates(t)

	tests := []struct {
		name string
		view detailView
	}{
		{
			name: "table present",
			view: detailView{
				baseView:   testBaseView("/jobs/voice-1"),
				JobID:      "voice-1",
				Speakers:   []string{"ずんだもん"},
				StylesJSON: `{"ずんだもん":["ノーマル"]}`,
			},
		},
		{
			// The table could not be built for some reason. The page should still open.
			name: "table empty",
			view: detailView{
				baseView: testBaseView("/jobs/voice-1"),
				JobID:    "voice-1",
				Speakers: []string{"ずんだもん"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			if err := renderInto(&buf, tmpl, "detail.html", tt.view); err != nil {
				t.Fatalf("render failed: %v", err)
			}
			match := regexp.MustCompile(`data-styles="([^"]*)"`).FindStringSubmatch(buf.String())
			if match == nil {
				t.Fatal("no data-styles (the page cannot read the table)")
			}
			// Attribute values come out HTML-escaped, so they are read back before parsing.
			var styles map[string][]string
			if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &styles); err != nil {
				t.Errorf("data-styles is not readable as JSON: %v (value: %s)", err, match[1])
			}
		})
	}
}

// renderInto renders one page into buf. Unlike executePage it does not t.Fatalf but returns the
// error, leaving how to handle failure to the caller.
func renderInto(buf *strings.Builder, pages map[string]*template.Template, name string, view any) error {
	tmpl, ok := pages[name]
	if !ok {
		return fmt.Errorf("page template not found: %s", name)
	}
	return tmpl.ExecuteTemplate(buf, assets.PageTemplate, view)
}
