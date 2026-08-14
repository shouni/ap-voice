package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
)

// testHandler は、実際の話者一覧を積んだ Handler を返します。
// 選択肢の妥当性を見るテストなので、ここは架空の一覧ではなく同梱の実物を使います。
func testHandler(t *testing.T) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return &Handler{speakers: registry}
}

// parseScript は scriptFromForm を呼ぶための薄い包みです。
func parseScript(t *testing.T, h *Handler, values url.Values) (lines int, err error) {
	t.Helper()

	req := httptest.NewRequest("POST", "/history/x/script", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm() error = %v", err)
	}

	script, err := h.scriptFromForm(req)
	return len(script.Lines), err
}

// TestScriptFromFormRejectsUnknownSpeakerAndStyle は、一覧に無い話者・スタイルを
// 保存前に弾くことを検証します。
//
// **画面が選択肢で出していても、フォームは何でも送れます。** 実在しない組み合わせは
// 合成時に getStyleID がその話者の既定へ黙って落とすため、保存できてしまうと
// 「指定したのに違う声で喋る」という気付きにくい形で現れます。
func TestScriptFromFormRejectsUnknownSpeakerAndStyle(t *testing.T) {
	t.Parallel()

	h := testHandler(t)

	tests := []struct {
		name    string
		values  url.Values
		wantErr string
	}{
		{
			name: "実在しない話者",
			values: url.Values{
				"speaker": {"存在しない話者"}, "style": {"ノーマル"}, "text": {"本文"},
			},
			wantErr: "一覧にありません",
		},
		{
			name: "話者が持たないスタイル",
			values: url.Values{
				// 春日部つむぎの talk スタイルは「ノーマル」だけです。
				"speaker": {"春日部つむぎ"}, "style": {"ヒソヒソ"}, "text": {"本文"},
			},
			wantErr: "というスタイルはありません",
		},
		{
			name: "項目数が揃っていない",
			values: url.Values{
				"speaker": {"ずんだもん", "四国めたん"}, "style": {"ノーマル"}, "text": {"本文"},
			},
			wantErr: "項目数が揃っていません",
		},
		{
			name:    "空の台本",
			values:  url.Values{},
			wantErr: "台本が空です",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseScript(t, h, tt.values)
			if err == nil {
				t.Fatalf("エラーになりません（%s）", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want に %q を含む", err, tt.wantErr)
			}
		})
	}
}

// TestScriptFromFormAcceptsValidLines は、実在する組み合わせが通ることを検証します。
func TestScriptFromFormAcceptsValidLines(t *testing.T) {
	t.Parallel()

	h := testHandler(t)
	values := url.Values{
		"title":   {"直した題名"},
		"speaker": {"ずんだもん", "四国めたん"},
		"style":   {"あまあま", "ノーマル"},
		"text":    {"  前後の空白は落ちるのだ  ", "二行目です"},
	}

	req := httptest.NewRequest("POST", "/history/x/script", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := req.ParseForm(); err != nil {
		t.Fatal(err)
	}

	script, err := h.scriptFromForm(req)
	if err != nil {
		t.Fatalf("scriptFromForm() error = %v", err)
	}
	if script.Title != "直した題名" {
		t.Errorf("Title = %q", script.Title)
	}
	if len(script.Lines) != 2 {
		t.Fatalf("行数 = %d, want 2", len(script.Lines))
	}
	if script.Lines[0].Text != "前後の空白は落ちるのだ" {
		t.Errorf("本文の前後の空白が残っています: %q", script.Lines[0].Text)
	}
	if script.Lines[0].Style != "あまあま" {
		t.Errorf("Style = %q", script.Lines[0].Style)
	}
}

// TestScriptFromFormDropsEmptyLines は、本文を空にした行が落ちることを検証します。
// **行を消す唯一の手段**なので、残ってしまうと編集画面から行を減らせません。
func TestScriptFromFormDropsEmptyLines(t *testing.T) {
	t.Parallel()

	h := testHandler(t)
	values := url.Values{
		"speaker": {"ずんだもん", "ずんだもん", "ずんだもん"},
		"style":   {"ノーマル", "ノーマル", "ノーマル"},
		"text":    {"残る", "   ", "これも残る"},
	}

	lines, err := parseScript(t, h, values)
	if err != nil {
		t.Fatalf("scriptFromForm() error = %v", err)
	}
	if lines != 2 {
		t.Errorf("行数 = %d, want 2", lines)
	}

	// すべて空なら保存させません。空の台本で合成しても無音が出るだけです。
	allEmpty := url.Values{
		"speaker": {"ずんだもん"}, "style": {"ノーマル"}, "text": {"  "},
	}
	if _, err := parseScript(t, h, allEmpty); err == nil {
		t.Error("全行が空でもエラーになりません")
	}
}

// TestScriptFromFormRejectsTooManyLines は、行数の上限を検証します。
// フォームは任意の行数を組み立てられるため、上限が無いと 1 リクエストで
// 際限なく合成を積めます。
func TestScriptFromFormRejectsTooManyLines(t *testing.T) {
	t.Parallel()

	h := testHandler(t)
	n := maxScriptLines + 1
	values := url.Values{}
	for range n {
		values.Add("speaker", "ずんだもん")
		values.Add("style", "ノーマル")
		values.Add("text", "本文")
	}

	if _, err := parseScript(t, h, values); err == nil {
		t.Fatalf("%d 行が素通りしました（上限 %d）", n, maxScriptLines)
	}
}

// TestAcceptedMessageTellsWhichButtonWasPressed は、受付の案内文が押したボタンで
// 変わることを検証します。
//
// まとめて作った場合は音声まで待つため、「履歴に並びます」だけだと
// 次に何を待てばよいのか分かりません。
func TestAcceptedMessageTellsWhichButtonWasPressed(t *testing.T) {
	t.Parallel()

	oneShot := acceptedMessage(domain.CommandGenerateAndSynthesize, "voice-1")
	scriptOnly := acceptedMessage(domain.CommandGenerate, "voice-1")

	if !strings.Contains(oneShot, "音声") || !strings.Contains(oneShot, "通知") {
		t.Errorf("まとめて作成の案内が音声に触れていません: %q", oneShot)
	}
	if strings.Contains(scriptOnly, "音声") {
		t.Errorf("台本のみの案内が音声に触れています: %q", scriptOnly)
	}
	for _, msg := range []string{oneShot, scriptOnly} {
		if !strings.Contains(msg, "voice-1") {
			t.Errorf("ジョブIDが入っていません: %q", msg)
		}
	}
}
