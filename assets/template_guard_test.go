package assets

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// This file checks that the embedded templates keep meeting the CSP's assumptions.
//
// The CSP secureheaders builds limits script-src to 'self'.
// A single inline <script> or on* attribute stops that page's scripts from running,
// and whoever added the template cannot notice (the other pages keep working).
//
// This app has actually hit it. Until 2026-08-24 detail.html had
// `<script>window.apVoiceStyles = ...</script>`, which had to move to a data attribute
// (#voice-styles) before the CSP could go in. Of the 5 apps checked that day, the only ones
// still violating it were ap-voice and adk-review, the two without this guard.

// templateNames returns the paths of every embedded template.
func templateNames(t *testing.T) []string {
	t.Helper()

	var names []string
	err := fs.WalkDir(Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && path.Ext(p) == ".html" {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot list the templates: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no templates found (suspect the embed directive)")
	}
	return names
}

// TestTemplatesHaveNoInlineScripts keeps behaviour outside the templates. Inline, a page's
// structure and behaviour mix in one file, and it invites embedding template values in JS
// string literals (onclick="del('{{.JobID}}')").
// It is also the precondition for keeping the CSP's script-src at 'self'.
func TestTemplatesHaveNoInlineScripts(t *testing.T) {
	t.Parallel()

	scriptTag := regexp.MustCompile(`<script\b[^>]*>`)
	// Pick up only on*= in attribute position (whitespace is required so English words in text are not caught).
	eventAttribute := regexp.MustCompile(`\son[a-z]+\s*=\s*["']`)

	for _, name := range templateNames(t) {
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			for _, tag := range scriptTag.FindAllString(string(body), -1) {
				if !strings.Contains(tag, "src=") {
					t.Errorf("an inline script remains: %s", tag)
				}
			}
			for _, attribute := range eventAttribute.FindAllString(string(body), -1) {
				t.Errorf("an event handler attribute remains: %s", strings.TrimSpace(attribute))
			}
		})
	}
}

// TestTemplatesReferenceNoExternalOrigins keeps Bootstrap self-hosted.
// If a reference to an external origin came back, the CSP would drop it at run time, so CI catches it.
func TestTemplatesReferenceNoExternalOrigins(t *testing.T) {
	t.Parallel()

	external := regexp.MustCompile(`(?:href|src)="(https?://[^"]+)"`)

	for _, name := range templateNames(t) {
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			for _, ref := range external.FindAllStringSubmatch(string(body), -1) {
				t.Errorf("a reference to an external origin is back: %s", ref[1])
			}
		})
	}
}

// TestTemplateLocalAssetsExist checks that the /static/... the templates point at exist.
// A vendor version bump has to fix both the templates and the directory name, and
// fixing only one gives a 404 (unnoticed until opened in a browser).
func TestTemplateLocalAssetsExist(t *testing.T) {
	t.Parallel()

	ref := regexp.MustCompile(`(?:href|src)="/static/([^"?]+)`)

	for _, name := range templateNames(t) {
		body, err := Templates.ReadFile(name)
		if err != nil {
			t.Fatalf("cannot read: %v", err)
		}
		for _, match := range ref.FindAllStringSubmatch(string(body), -1) {
			asset := path.Join("static", match[1])
			if _, err := fs.Stat(StaticFiles, asset); err != nil {
				t.Errorf("%s points at %s, which does not exist: %v", name, asset, err)
			}
		}
	}
}

// TestPagesUseTheSharedLayout ensures pages do not carry their own outer frame.
//
// Until 2026-08-24 each of 5 pages carried everything from <!doctype html> to </html>, and their
// heads differed only in <title>. Moving Bootstrap from a CDN to self-hosting meant fixing the same
// 3 lines in 5 files, and missing one would break that page's styling alone.
// Only layout.html holds the outer frame.
func TestPagesUseTheSharedLayout(t *testing.T) {
	t.Parallel()

	const layout = "templates/layout.html"

	for _, name := range templateNames(t) {
		if name == layout || strings.HasPrefix(name, "templates/partials/") || name == "templates/nav.html" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("cannot read: %v", err)
			}
			text := string(body)

			if strings.Contains(strings.ToLower(text), "<!doctype") {
				t.Error("the page carries its own outer frame (move it to layout.html)")
			}
			// layout.html calls title and content. Missing even one makes that page alone
			// fail at render time (easy to miss, since the other pages are fine).
			//
			// Scripts are not here. Which page loads what is held by handlers'
			// pageScripts, and the layout loads them from .JS.
			for _, block := range []string{"title", "content"} {
				if !strings.Contains(text, `{{ define "`+block+`" }}`) {
					t.Errorf("does not define %q", block)
				}
			}
		})
	}
}

// TestParsePagesCoversEveryScreen checks that every page template is assembled.
// A shared part placed with only one page cannot be found from the other set and fails at render time
// (actually hit by leaving input_badge in modes.html while calling it from mode_detail.html).
func TestParsePagesCoversEveryScreen(t *testing.T) {
	t.Parallel()

	pages, err := ParsePages()
	if err != nil {
		t.Fatalf("ParsePages() error = %v", err)
	}

	for _, name := range templateNames(t) {
		if name == "templates/layout.html" || name == "templates/nav.html" ||
			strings.HasPrefix(name, "templates/partials/") {
			continue
		}
		base := path.Base(name)
		tmpl, ok := pages[base]
		if !ok {
			t.Errorf("no template set for %s", base)
			continue
		}
		if tmpl.Lookup(PageTemplate) == nil {
			t.Errorf("the set for %s lacks %s", base, PageTemplate)
		}
	}
}
