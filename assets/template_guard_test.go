package assets

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// このファイルは、埋め込みテンプレートが CSP の前提を満たし続けることを見ます。
//
// secureheaders が組み立てる CSP は script-src を 'self' だけにしています。
// インラインの <script> や on* 属性が 1 つでも入るとその画面のスクリプトが実行されず、
// しかもテンプレートを足した本人には気付けません（他の画面は正常に動くため）。
//
// このアプリは実際に踏んでいます。2026-08-24 まで detail.html が
// `<script>window.apVoiceStyles = ...</script>` を持っており、CSP を入れる前に
// data 属性（#voice-styles）へ移す必要がありました。同じ日に調べた 5 アプリのうち
// 違反が残っていたのは、このガードを持たない ap-voice と adk-review だけでした。

// templateNames は、埋め込まれている全テンプレートのパスを返します。
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
		t.Fatalf("テンプレートを列挙できない: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("テンプレートが 1 つも見つからない（embed の指定を疑う）")
	}
	return names
}

// TestTemplatesHaveNoInlineScripts は、振る舞いをテンプレートの外に置き続けることを
// 保証します。インラインのままだと画面の構造と振る舞いが同じファイルに混ざり、
// テンプレートの値を JS の文字列リテラルへ埋め込む書き方（onclick="del('{{.JobID}}')"）
// を誘発します。CSP の script-src を 'self' に保つ前提条件でもあります。
func TestTemplatesHaveNoInlineScripts(t *testing.T) {
	t.Parallel()

	scriptTag := regexp.MustCompile(`<script\b[^>]*>`)
	// 属性位置の on*= だけを拾います（本文中の英単語を巻き込まないよう空白を要求）。
	eventAttribute := regexp.MustCompile(`\son[a-z]+\s*=\s*["']`)

	for _, name := range templateNames(t) {
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("読めない: %v", err)
			}
			for _, tag := range scriptTag.FindAllString(string(body), -1) {
				if !strings.Contains(tag, "src=") {
					t.Errorf("インラインスクリプトが残っています: %s", tag)
				}
			}
			for _, attribute := range eventAttribute.FindAllString(string(body), -1) {
				t.Errorf("イベントハンドラ属性が残っています: %s", strings.TrimSpace(attribute))
			}
		})
	}
}

// TestTemplatesReferenceNoExternalOrigins は、Bootstrap を自前配信へ移した状態を保ちます。
// 外部オリジンへの参照が復活すると CSP が実行時にそれを落とすので、CI で気付けるようにします。
func TestTemplatesReferenceNoExternalOrigins(t *testing.T) {
	t.Parallel()

	external := regexp.MustCompile(`(?:href|src)="(https?://[^"]+)"`)

	for _, name := range templateNames(t) {
		t.Run(name, func(t *testing.T) {
			body, err := Templates.ReadFile(name)
			if err != nil {
				t.Fatalf("読めない: %v", err)
			}
			for _, ref := range external.FindAllStringSubmatch(string(body), -1) {
				t.Errorf("外部オリジンへの参照が復活しています: %s", ref[1])
			}
		})
	}
}

// TestTemplateLocalAssetsExist は、テンプレートが指す /static/... が実在することを見ます。
// vendor のバージョン更新はテンプレートとディレクトリ名の両方を直す必要があり、
// 片方だけ直すと 404 になります（ブラウザで開くまで気付けません）。
func TestTemplateLocalAssetsExist(t *testing.T) {
	t.Parallel()

	ref := regexp.MustCompile(`(?:href|src)="/static/([^"?]+)`)

	for _, name := range templateNames(t) {
		body, err := Templates.ReadFile(name)
		if err != nil {
			t.Fatalf("読めない: %v", err)
		}
		for _, match := range ref.FindAllStringSubmatch(string(body), -1) {
			asset := path.Join("static", match[1])
			if _, err := fs.Stat(StaticFiles, asset); err != nil {
				t.Errorf("%s が指す %s が存在しません: %v", name, asset, err)
			}
		}
	}
}

// TestPagesUseTheSharedLayout は、画面が外枠を自前で持たないことを保証します。
//
// 2026-08-24 まで 5 つの画面がそれぞれ <!doctype html> から </html> まで丸ごと持っており、
// head の違いは <title> だけでした。Bootstrap を CDN から自前配信へ移したとき同じ 3 行を
// 5 ファイル直すことになり、1 つ直し漏らせばその画面だけスタイルが崩れる状態でした。
// 外枠は layout.html だけが持ちます。
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
				t.Fatalf("読めない: %v", err)
			}
			text := string(body)

			if strings.Contains(strings.ToLower(text), "<!doctype") {
				t.Error("画面が外枠を自前で持っています（layout.html に寄せてください）")
			}
			// layout.html は title と content を呼びます。1 つでも欠けるとその画面だけ
			// 描画時に落ちます（他の画面は無事なので気付きにくい）。
			//
			// スクリプトはここにありません。どの画面が何を読むかは handlers の
			// pageScripts が持ち、レイアウトが .JS から読み込みます。
			for _, block := range []string{"title", "content"} {
				if !strings.Contains(text, `{{ define "`+block+`" }}`) {
					t.Errorf("%q を define していません", block)
				}
			}
		})
	}
}

// TestParsePagesCoversEveryScreen は、画面テンプレートが 1 枚残らず組み立てられることを見ます。
// 共通部品を片方の画面にだけ置くと、もう片方のセットから引けず描画時に落ちます
// （input_badge を modes.html に置いたまま mode_detail.html から呼んで実際に踏みました）。
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
			t.Errorf("%s のテンプレートセットがありません", base)
			continue
		}
		if tmpl.Lookup(PageTemplate) == nil {
			t.Errorf("%s のセットに %s がありません", base, PageTemplate)
		}
	}
}
