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

	"github.com/shouni/go-job-firestore/jobfirestore"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// テンプレートは実行時にしか評価されないため、参照している値の名前が変わっても
// コンパイルは通ります。画面を開くまで壊れたと分からないのがテンプレートの弱点で、
// ナビの重複や CSRF の hidden 欠落は実際にデプロイ後に見つかりました。
//
// ここでは builder と同じ組み立て方で読み、ハンドラーが渡すのと同じ型を流します。
// map で流すと存在しないキーが "<no value>" になって素通りするため、
// 必ず本物の View 構造体を使います。それがフィールド名の改名を検知する条件です。

// parseTemplates は builder/handlers.go と同じ手順でテンプレートを読みます。
// 画面ごとに独立したセットになります（キーは "history.html" などのファイル名）。
func parseTemplates(t *testing.T) map[string]*template.Template {
	t.Helper()

	pages, err := assets.ParsePages()
	if err != nil {
		t.Fatalf("テンプレートの読み込みに失敗しました: %v", err)
	}
	return pages
}

// testBaseView は全画面共通の値です。ナビが .Path と .DefaultModel を見ます。
func testBaseView(path string) baseView {
	return baseView{CSRFToken: "test-csrf-token", Path: path, DefaultModel: "gemini-test"}
}

// testModes はフォームに出すモードです。表示名を持つものと持たないものを 1 つずつ
// 混ぜて、front matter が無いプロンプトでも選択肢が消えないことを見ます。
func testModes() []assets.Mode {
	return []assets.Mode{
		{Key: "promo",
			Label:     "楽曲紹介（春日部つむぎ × ずんだもん）",
			Direction: "楽曲レシピから作る宣伝ナレーション",
			UseWhen:   "recipe.json のとき",
			// 入力の型がタブを決めます。promo だけがレシピ入力です。
			Input: assets.InputRecipe},
		{Key: "solo"},
	}
}

// TestTemplatesRender は、3 画面が実際の View 構造体で描画できることを検証します。
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
		// want は必ず出ていてほしい断片です。描画できるだけでは、値が
		// 抜け落ちた画面を通してしまいます。
		want []string
		// notWant は出てはいけない断片です。存在しないフォームを指すボタンは
		// 描画としては成功するので、出ていないことを言わないと拾えません。
		notWant []string
	}{
		{
			name:     "投入フォーム",
			template: "home.html",
			view: formView{
				baseView:    testBaseView("/"),
				TextModes:   assets.FilterModes(testModes(), assets.InputText),
				RecipeModes: assets.FilterModes(testModes(), assets.InputRecipe),
				Models:      []string{"gemini-test"},
				Message:     "受け付けました",
				// 投入後の再描画では入力内容が残ります。空に戻すと
				// 同じソースから作り直すのに URL を貼り直すことになります。
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
				// 選択肢の表示名と説明は front matter 由来です。
				"楽曲紹介",
				`data-usewhen="recipe.json のとき"`,
				// 表示名が無いモードはキーで出ます（説明の書き忘れで消えない）。
				">solo<",
				`value="gemini-test" selected`,
				"受け付けました",
			},
		},
		{
			name:     "履歴一覧",
			template: "history.html",
			view: historyView{
				baseView: testBaseView("/history"),
				Page:     jobfirestore.PageMeta{Page: 2, TotalPages: 3, Total: 120, From: 51, To: 100, HasPrev: true, HasNext: true, PrevPage: 1, NextPage: 3},
				Jobs: []repository.Job{
					{ID: "voice-1", Title: "一覧のタイトル", CreatedAt: time.Now(), HasAudio: true,
						State: jobfirestore.StateSucceeded},
					// 台本が読めなかった場合はジョブ ID が題名に入ります。
					{ID: "voice-2", Title: "voice-2", CreatedAt: time.Now(), HasAudio: false,
						State: jobfirestore.StateFailed},
					// 実行中は「台本のみ」と見分けが付かなければなりません。
					{ID: "voice-3", Title: "実行中のジョブ", CreatedAt: time.Now(),
						State: jobfirestore.StateRunning},
				},
			},
			want: []string{
				"一覧のタイトル", "voice-2",
				// 状態は成果物の有無より先に出ます。待てば出るのか、
				// 消してやり直すのかが一覧で分かる必要があります。
				">音声あり<", ">失敗<", ">実行中<",
				// ページ送りは 2 ページ以上のときだけ出ます。
				`href="/history?page=1"`, `href="/history?page=3"`, "全 120 件",
			},
		},
		{
			name:     "詳細（音声あり）",
			template: "detail.html",
			view: detailView{
				baseView:   testBaseView("/history/voice-1"),
				JobID:      "voice-1",
				Script:     script,
				HasScript:  true,
				HasAudio:   true,
				State:      jobfirestore.StateSucceeded,
				Speakers:   []string{"ずんだもん", "四国めたん"},
				StylesJSON: `{"ずんだもん":["ノーマル"]}`,
			},
			want: []string{
				"保存して音声を作り直す",
				"このジョブを削除",
				"履歴一覧へ戻る",
				`src="/history/voice-1/audio"`,
				// 台本は読むだけでなく直せます。
				`action="/history/voice-1/script"`,
				`name="title"`,
				`<textarea class="form-control form-control-sm js-text" name="text"`,
				`<option value="四国めたん"`,
				// 保存済みのスタイルは選ばれた状態で戻ること。
				`data-selected="ノーマル"`,
				"こんにちはなのだ",
			},
		},
		{
			name:     "詳細（音声なし）",
			template: "detail.html",
			view: detailView{
				baseView:  testBaseView("/history/voice-2"),
				JobID:     "voice-2",
				Script:    script,
				HasScript: true,
				HasAudio:  false,
				Speakers:  []string{"ずんだもん"},
			},
			want: []string{"保存して音声を作成"},
		},
		{
			// 台本を書く前に失敗したジョブです。この画面が開かないと、履歴に
			// 並んだまま消せません（削除はここからしかできません）。
			name:     "詳細（台本なし・失敗）",
			template: "detail.html",
			view: detailView{
				baseView: testBaseView("/history/voice-3"),
				JobID:    "voice-3",
				State:    jobfirestore.StateFailed,
				JobError: "AIモデルが空のスクリプトを返しました",
				Speakers: []string{"ずんだもん"},
			},
			want: []string{
				"このジョブは失敗しました",
				// 失敗の理由が人の目に触れるのは、Slack 通知を除けばここだけです。
				"AIモデルが空のスクリプトを返しました",
				"台本はまだありません",
				"このジョブを削除",
			},
			// 直す台本が無いので、保存も合成もできません。ボタンだけ残すと、
			// 押しても何も起きない（指す先のフォームが無い）ものが並びます。
			notWant: []string{"保存して音声", "台本JSONをダウンロード"},
		},
	}

	tmpl := parseTemplates(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			if err := renderInto(&buf, tmpl, tt.template, tt.view); err != nil {
				t.Fatalf("%s の描画に失敗しました: %v", tt.template, err)
			}

			got := buf.String()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("%s に %q が含まれていません", tt.template, want)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("%s に %q が残っています", tt.template, notWant)
				}
			}
		})
	}
}

// optionPattern は、指定した value を持つ <option> の開始タグを取り出します。
// 属性は複数行に分かれて出るため、行をまたいで拾います。
func optionPattern(value string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<option value="` + regexp.QuoteMeta(value) + `".*?>`)
}

// TestFormKeepsSelectedMode は、投入済みのモードが選択された状態で戻ることを検証します。
//
// 選択肢の中身が front matter 由来になったことで、value（キー）と表示名が別物に
// なりました。selected が付くのはキーの一致で決まるので、表示名を変えても
// 選択状態が外れないことをここで押さえます。
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
		t.Fatalf("home.html の描画に失敗しました: %v", err)
	}

	got := buf.String()
	if tag := optionPattern("promo").FindString(got); !strings.Contains(tag, "selected") {
		t.Errorf("promo が選択されていません: %q", tag)
	}
	if tag := optionPattern("solo").FindString(got); strings.Contains(tag, "selected") {
		t.Errorf("solo まで選択されています: %q", tag)
	}
}

// TestTemplatesIncludeCSRFTokenInForms は、POST するフォームすべてに
// CSRF トークンの hidden があることを検証します。
//
// 1 つでも欠けるとその操作だけが「不正なCSRFトークン」で弾かれ、しかも
// 画面は正常に見えるため気付きにくい。実際に投入フォームで踏みました。
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
			baseView:  testBaseView("/history/voice-1"),
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
				t.Fatalf("%s の描画に失敗しました: %v", name, err)
			}

			got := buf.String()
			// method="post" の数だけ hidden が要ります。
			if forms, tokens := strings.Count(got, `method="post"`), strings.Count(got, `name="csrf_token"`); forms != tokens {
				t.Errorf("%s: POST フォーム %d 件に対し csrf_token は %d 件です", name, forms, tokens)
			}
		})
	}
}

// TestNavHighlightsCurrentPage は、ナビの現在地が .Path で切り替わることを検証します。
// ナビは 3 画面で共有しており、以前は各ファイルに写していたため
// ホームだけ履歴リンクが欠けるという食い違いが起きました。
func TestNavHighlightsCurrentPage(t *testing.T) {
	t.Parallel()

	tmpl := parseTemplates(t)

	for _, path := range []string{"/", "/history"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			var buf strings.Builder
			if err := tmpl["home.html"].ExecuteTemplate(&buf, "nav", testBaseView(path)); err != nil {
				t.Fatalf("ナビの描画に失敗しました: %v", err)
			}

			got := buf.String()
			// どの画面からでも両方のリンクが出ていること。
			if !strings.Contains(got, `href="/"`) || !strings.Contains(got, `href="/history"`) {
				t.Errorf("ナビにリンクが揃っていません: %s", got)
			}
			if want := `href="` + path + `"`; !strings.Contains(got, `aria-current="page" `+want) {
				t.Errorf("現在地 %s が強調されていません", path)
			}
		})
	}
}

// TestDetailAlwaysEmitsParsableStyles は、話者→スタイルの対応表が空でも
// 画面側が JSON.parse に失敗しないことを検証します。
//
// 以前はインラインスクリプトで `window.apVoiceStyles = {{ .StylesJSON }};` と埋めており、
// 値が空だと `= ;` になってその行だけでなく以降の読み込みごと壊れていました。
// CSP の script-src を 'self' だけにするためインラインをやめ、data 属性へ移しています
// （template.JS は </script> を素通しするので、属性コンテキストの方が安全でもあります）。
// 壊れ方は変わりましたが、「空でも妥当な値を吐く」という守るべき性質は同じです。
func TestDetailAlwaysEmitsParsableStyles(t *testing.T) {
	t.Parallel()

	tmpl := parseTemplates(t)

	tests := []struct {
		name string
		view detailView
	}{
		{
			name: "対応表がある",
			view: detailView{
				baseView:   testBaseView("/history/voice-1"),
				JobID:      "voice-1",
				Speakers:   []string{"ずんだもん"},
				StylesJSON: `{"ずんだもん":["ノーマル"]}`,
			},
		},
		{
			// 何らかの理由で対応表を組めなかった場合です。画面は開けるべきです。
			name: "対応表が空",
			view: detailView{
				baseView: testBaseView("/history/voice-1"),
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
				t.Fatalf("描画に失敗しました: %v", err)
			}
			match := regexp.MustCompile(`data-styles="([^"]*)"`).FindStringSubmatch(buf.String())
			if match == nil {
				t.Fatal("data-styles が出ていません（画面側が対応表を読めません）")
			}
			// 属性値は HTML エスケープされて出るので、読み戻してから解析します。
			var styles map[string][]string
			if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &styles); err != nil {
				t.Errorf("data-styles が JSON として読めません: %v (値: %s)", err, match[1])
			}
		})
	}
}

// renderInto は 1 画面を buf へ描きます。失敗の扱いは呼び出し側に任せるため、
// executePage と違って t.Fatalf せずエラーを返します。
func renderInto(buf *strings.Builder, pages map[string]*template.Template, name string, view any) error {
	tmpl, ok := pages[name]
	if !ok {
		return fmt.Errorf("画面テンプレートが見つかりません: %s", name)
	}
	return tmpl.ExecuteTemplate(buf, assets.PageTemplate, view)
}
