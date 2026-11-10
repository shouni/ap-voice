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

// testHandler returns a Handler loaded with the real speaker roster.
// These tests check option validity, so they use the bundled roster rather than a fictional one.
func testHandler(t *testing.T) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return &Handler{speakers: registry}
}

// parseScript is a thin wrapper for calling scriptFromForm.
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

// TestScriptFromFormRejectsUnknownSpeakerAndStyle verifies that speakers and styles not in the roster
// are rejected before saving.
//
// The page offers them as options, but a form can send anything. A non-existent pair silently
// falls back to the speaker's default style at synthesis, so if it could be saved it would show up
// in a hard-to-notice way: "it speaks in a different voice from the one I chose".
func TestScriptFromFormRejectsUnknownSpeakerAndStyle(t *testing.T) {
	t.Parallel()

	h := testHandler(t)

	tests := []struct {
		name    string
		values  url.Values
		wantErr string
	}{
		{
			name: "speaker that does not exist",
			values: url.Values{
				"speaker": {"存在しない話者"}, "style": {"ノーマル"}, "text": {"本文"},
			},
			wantErr: "一覧にありません",
		},
		{
			name: "style the speaker does not have",
			values: url.Values{
				// 春日部つむぎ's only talk style is 「ノーマル」.
				"speaker": {"春日部つむぎ"}, "style": {"ヒソヒソ"}, "text": {"本文"},
			},
			wantErr: "というスタイルはありません",
		},
		{
			name: "field counts do not match",
			values: url.Values{
				"speaker": {"ずんだもん", "四国めたん"}, "style": {"ノーマル"}, "text": {"本文"},
			},
			wantErr: "項目数が揃っていません",
		},
		{
			name:    "empty script",
			values:  url.Values{},
			wantErr: "台本が空です",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseScript(t, h, tt.values)
			if err == nil {
				t.Fatalf("no error (%s)", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestScriptFromFormAcceptsValidLines verifies that existing pairs pass.
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
		t.Fatalf("line count = %d, want 2", len(script.Lines))
	}
	if script.Lines[0].Text != "前後の空白は落ちるのだ" {
		t.Errorf("surrounding whitespace remains in the text: %q", script.Lines[0].Text)
	}
	if script.Lines[0].Style != "あまあま" {
		t.Errorf("Style = %q", script.Lines[0].Style)
	}
}

// TestScriptFromFormDropsEmptyLines verifies that a line whose text was emptied is dropped.
// The page's delete button does not send the row at all, so it does not come this way, but emptying
// the text also works for replacements that bypass the page (the API, the script JSON).
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
		t.Errorf("line count = %d, want 2", lines)
	}

	// If every line is empty, saving is refused. Synthesising an empty script only produces silence.
	allEmpty := url.Values{
		"speaker": {"ずんだもん"}, "style": {"ノーマル"}, "text": {"  "},
	}
	if _, err := parseScript(t, h, allEmpty); err == nil {
		t.Error("no error although every line is empty")
	}
}

// TestScriptFromFormRejectsTooManyLines verifies the line limit.
// A form can assemble any number of lines, so without a limit one request could queue
// unbounded synthesis.
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
		t.Fatalf("%d lines got through (limit %d)", n, maxScriptLines)
	}
}

// TestAcceptedMessageTellsWhichButtonWasPressed verifies that the acknowledgement changes with
// the button pressed.
//
// When both are made together the wait lasts until the audio, so "it will appear in the history" alone
// does not say what to wait for next.
func TestAcceptedMessageTellsWhichButtonWasPressed(t *testing.T) {
	t.Parallel()

	oneShot := acceptedMessage(domain.CommandGenerateAndSynthesize, "voice-1")
	scriptOnly := acceptedMessage(domain.CommandGenerate, "voice-1")

	if !strings.Contains(oneShot, "音声") || !strings.Contains(oneShot, "通知") {
		t.Errorf("the combined message does not mention the audio: %q", oneShot)
	}
	if strings.Contains(scriptOnly, "音声") {
		t.Errorf("the script-only message mentions the audio: %q", scriptOnly)
	}
	for _, msg := range []string{oneShot, scriptOnly} {
		if !strings.Contains(msg, "voice-1") {
			t.Errorf("the job ID is missing: %q", msg)
		}
	}
}
