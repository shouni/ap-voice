package assets

import (
	"fmt"
	"html/template"
	"io/fs"
	"path"
)

// layoutTemplate and navTemplate are the outer frame and shared navigation every page uses.
// partialsGlob holds the parts used by two or more pages, added to every set unconditionally.
const (
	layoutTemplate = "templates/layout.html"
	navTemplate    = "templates/nav.html"
	partialsGlob   = "templates/partials/*.html"
)

// PageTemplate is the template name to execute when rendering from layout.html.
const PageTemplate = "layout.html"

// ParsePages builds and returns an independent template set per page.
// The key is the page's file name ("history.html" and so on).
//
// All pages cannot go into one set because each page defines blocks under the same names ("content" / "title" /
// "scripts"). html/template does not allow redefining a name, so
// "frame + shared nav + that one page" is built per page. The sibling services have the same shape.
func ParsePages() (map[string]*template.Template, error) {
	names, err := fs.Glob(Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to list the templates: %w", err)
	}

	pages := make(map[string]*template.Template)
	for _, name := range names {
		if name == layoutTemplate || name == navTemplate {
			continue
		}
		tmpl, err := template.ParseFS(Templates, layoutTemplate, navTemplate, name)
		if err != nil {
			return nil, fmt.Errorf("failed to load %s: %w", name, err)
		}
		if _, err := tmpl.ParseFS(Templates, partialsGlob); err != nil {
			return nil, fmt.Errorf("failed to load the shared parts for %s: %w", name, err)
		}
		pages[path.Base(name)] = tmpl
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("no page templates found")
	}
	return pages, nil
}
