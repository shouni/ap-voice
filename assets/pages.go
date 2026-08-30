package assets

import (
	"fmt"
	"html/template"
	"io/fs"
	"path"
)

// layoutTemplate と navTemplate は、全画面が共有する外枠と共通ナビです。
// partialsGlob は 2 画面以上で使う部品で、全セットへ無条件に入れます。
const (
	layoutTemplate = "templates/layout.html"
	navTemplate    = "templates/nav.html"
	partialsGlob   = "templates/partials/*.html"
)

// PageTemplate は、layout.html を起点に描くときのテンプレート名です。
const PageTemplate = "layout.html"

// ParsePages は、画面ごとに独立したテンプレートセットを組み立てて返します。
// キーは画面のファイル名（"history.html" など）です。
//
// 1 つのセットに全画面を入れられないのは、各画面が同じ名前（"content" / "title" /
// "scripts"）でブロックを define するためです。html/template は同名の再定義を許さないので、
// 「外枠 + 共通ナビ + その画面 1 枚」を画面ごとに作ります。姉妹サービスも同じ形です。
func ParsePages() (map[string]*template.Template, error) {
	names, err := fs.Glob(Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("テンプレートの列挙に失敗しました: %w", err)
	}

	pages := make(map[string]*template.Template)
	for _, name := range names {
		if name == layoutTemplate || name == navTemplate {
			continue
		}
		tmpl, err := template.ParseFS(Templates, layoutTemplate, navTemplate, name)
		if err != nil {
			return nil, fmt.Errorf("%s の読み込みに失敗しました: %w", name, err)
		}
		if _, err := tmpl.ParseFS(Templates, partialsGlob); err != nil {
			return nil, fmt.Errorf("%s の共通部品の読み込みに失敗しました: %w", name, err)
		}
		pages[path.Base(name)] = tmpl
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("画面テンプレートが 1 つも見つかりません")
	}
	return pages, nil
}
