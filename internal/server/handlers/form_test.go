package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
)

// capturingQueue is a TaskQueue that remembers the enqueued request.
type capturingQueue struct {
	got   domain.Request
	calls int
}

func (q *capturingQueue) Enqueue(_ context.Context, req domain.Request) error {
	q.got = req
	q.calls++
	return nil
}

// formHandler returns a Handler that can render and process the submission form.
// It reads the real templates — which tab shows what can only be checked in the rendered output.
func formHandler(t *testing.T, queue domain.TaskQueue, repo ScriptRepository) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	tmpl := parseTemplates(t)
	modes, err := assets.LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	return &Handler{
		queue:       queue,
		templates:   tmpl,
		modes:       modes,
		models:      []string{"gemini-test"},
		bucket:      "test",
		musicBucket: "music-bucket",
		layout:      domain.NewStorageLayout(),
		repo:        repo,
		speakers:    registry,
	}
}

// postForm POSTs to the submission form.
func postForm(t *testing.T, h *Handler, values url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.JobCreate(rec, req)
	return rec
}

// TestHomeSeparatesModesByInputKind verifies that the generation modes each tab offers are
// split by input type.
//
// Offering music promotion on the plain text tab would make it selectable but always failing.
// A mode that expects a recipe fails at decoding once generation starts, so
// nothing happens on selection and the cause shows only in the worker's logs.
func TestHomeSeparatesModesByInputKind(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, nil)

	rec := httptest.NewRecorder()
	h.Home(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	inputTab := sectionOf(t, body, `id="mode_input"`, "</select>")
	recipeTab := sectionOf(t, body, `id="mode_recipe"`, "</select>")

	if strings.Contains(inputTab, `value="music_promo"`) {
		t.Error("the input source tab offers music_promo")
	}
	if !strings.Contains(inputTab, `value="comedy_manzai"`) {
		t.Error("the input source tab does not offer the plain text modes")
	}
	if !strings.Contains(recipeTab, `value="music_promo"`) {
		t.Error("the music recipe tab does not offer music_promo")
	}
	if strings.Contains(recipeTab, `value="comedy_manzai"`) {
		t.Error("the music recipe tab offers plain text modes")
	}

	// The script JSON tab has no mode selection at all.
	if !strings.Contains(body, `name="script_json"`) {
		t.Error("there is no script JSON tab")
	}
}

// sectionOf cuts out the part from from to to. It is used to tell
// the options of each tab apart.
func sectionOf(t *testing.T, body, from, to string) string {
	t.Helper()

	i := strings.Index(body, from)
	if i < 0 {
		t.Fatalf("%q not found", from)
	}
	rest := body[i:]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("after %q there is no %q", from, to)
	}
	return rest[:j]
}

// openingTagOf returns the opening tag of the element containing marker.
// class is written before id, so it searches backwards from marker.
func openingTagOf(t *testing.T, body, marker string) string {
	t.Helper()

	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("%q not found", marker)
	}
	start := strings.LastIndex(body[:i], "<")
	if start < 0 {
		t.Fatalf("opening tag of %q not found", marker)
	}
	end := strings.Index(body[start:], ">")
	if end < 0 {
		t.Fatalf("opening tag of %q is not closed", marker)
	}
	return body[start : start+end]
}

// TestEnqueueFromRecipeBuildsURIFromJobID verifies that the recipe's location is built from
// a music job ID.
//
// It is the same rule as the video generation service's. If only one side's rule changed, passing the same ID
// would work on one side only, and you could not tell which one is right.
func TestEnqueueFromRecipeBuildsURIFromJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := formHandler(t, queue, nil)

	rec := postForm(t, h, url.Values{
		"source":       {"recipe"},
		"command":      {"generate"},
		"music_job_id": {"music-20260814-031712-5c812debb05f"},
		"mode":         {"music_promo"},
		"ai_model":     {"gemini-test"},
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	const want = "gs://music-bucket/music/music-20260814-031712-5c812debb05f/recipe.json"
	if queue.got.InputURI != want {
		t.Errorf("InputURI = %q, want %q", queue.got.InputURI, want)
	}
	if queue.got.Mode != "music_promo" {
		t.Errorf("Mode = %q", queue.got.Mode)
	}
}

// TestEnqueueFromRecipeRejectsBadJobID verifies that a malformed job ID is rejected before the
// enqueue.
//
// The path is built from the ID, so a malformed one points at a location that does not exist.
// Once enqueued, the failure shows only after the worker goes to read it.
func TestEnqueueFromRecipeRejectsBadJobID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		jobID   string
		wantErr string
	}{
		{name: "empty", jobID: "", wantErr: "楽曲のジョブIDを入力してください"},
		{name: "malformed", jobID: "not-a-job-id", wantErr: "形式が正しくありません"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queue := &capturingQueue{}
			h := formHandler(t, queue, nil)

			rec := postForm(t, h, url.Values{
				"source": {"recipe"}, "command": {"generate"},
				"music_job_id": {tt.jobID}, "mode": {"music_promo"},
			})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("the body lacks %q", tt.wantErr)
			}
			if queue.calls != 0 {
				t.Errorf("enqueued although it was rejected: %d times", queue.calls)
			}
		})
	}
}

// TestEnqueueFromScriptCreatesJobWithoutGemini verifies that a pasted script is saved as a
// new job and goes to synthesis without generation.
//
// Saving comes before the enqueue. The script does not ride on the task, so if it is not already
// where the arriving worker reads it, the run fails on the spot.
func TestEnqueueFromScriptCreatesJobWithoutGemini(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	queue := &capturingQueue{}
	h := formHandler(t, queue, repo)

	rec := postForm(t, h, url.Values{
		"source": {"script"},
		"script_json": {`{"title":"漫才","lines":[
			{"speaker":"ずんだもん","style":"ノーマル","text":"どうもなのだ"},
			{"speaker":"四国めたん","style":"ノーマル","text":"どうも。"}]}`},
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if repo.calls != 1 {
		t.Fatalf("saved %d times", repo.calls)
	}
	if len(repo.saved.Lines) != 2 || repo.saved.Title != "漫才" {
		t.Errorf("the saved script differs: %+v", repo.saved)
	}
	if queue.got.Command != domain.CommandSynthesize {
		t.Errorf("Command = %q, want synthesize", queue.got.Command)
	}
	// It carries no generation input. This path skips Gemini, so if an input source
	// or mode were mixed in, a generation would run.
	if queue.got.InputURI != "" || queue.got.Mode != "" {
		t.Errorf("generation input is mixed in: InputURI=%q Mode=%q", queue.got.InputURI, queue.got.Mode)
	}
	if queue.got.JobID == "" {
		t.Error("no job ID was issued")
	}
}

// TestEnqueueFromScriptRejectsBadScript verifies that broken JSON and non-existent
// speakers and styles are rejected before the enqueue.
func TestEnqueueFromScriptRejectsBadScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{name: "empty", json: "", wantErr: "貼り付けてください"},
		{name: "broken JSON", json: `{"title":`, wantErr: "JSONの解釈に失敗しました"},
		{
			name:    "speaker that does not exist",
			json:    `{"title":"t","lines":[{"speaker":"居ない人","style":"ノーマル","text":"本文"}]}`,
			wantErr: "一覧にありません",
		},
		{
			// 春日部つむぎ's only talk style is 「ノーマル」.
			name:    "style the speaker does not have",
			json:    `{"title":"t","lines":[{"speaker":"春日部つむぎ","style":"ヒソヒソ","text":"本文"}]}`,
			wantErr: "というスタイルはありません",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &savingRepo{}
			queue := &capturingQueue{}
			h := formHandler(t, queue, repo)

			rec := postForm(t, h, url.Values{"source": {"script"}, "script_json": {tt.json}})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("the body lacks %q", tt.wantErr)
			}
			if repo.calls != 0 || queue.calls != 0 {
				t.Errorf("saved/enqueued although it was rejected: save=%d enqueue=%d", repo.calls, queue.calls)
			}
		})
	}
}

// TestEnqueueFromScriptKeepsTheJSONOnError verifies that the pasted JSON remains after a failure.
// Fixing and resubmitting is the usual course; losing it means pasting again.
func TestEnqueueFromScriptKeepsTheJSONOnError(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, &savingRepo{})

	const bad = `{"title":"t","lines":[{"speaker":"居ない人","style":"ノーマル","text":"覚えていてほしい本文"}]}`
	rec := postForm(t, h, url.Values{"source": {"script"}, "script_json": {bad}})

	if !strings.Contains(rec.Body.String(), "覚えていてほしい本文") {
		t.Error("the pasted JSON is gone")
	}
}

// TestEnqueueReopensTheTabThatFailed verifies that a failed page is re-rendered with
// that tab open.
//
// If the first tab always opened, only the error message would be visible while the input
// stayed inside a closed tab. You could not tell what to fix.
func TestEnqueueReopensTheTabThatFailed(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, &savingRepo{})

	rec := postForm(t, h, url.Values{
		"source": {"recipe"}, "command": {"generate"},
		"music_job_id": {"こわれたID"}, "mode": {"music_promo"},
	})

	pane := openingTagOf(t, rec.Body.String(), `id="tab-recipe"`)
	if !strings.Contains(pane, "show active") {
		t.Errorf("the music recipe tab is not open: %q", pane)
	}
}

// TestEnqueueSaysWhichButtonWasPressed verifies that, when both are made together, the wording
// makes clear the wait lasts until the audio.
//
// For a while either button said 「台本の作成を受け付けました」, and
// the page did not reveal that the run went on to the audio.
func TestEnqueueSaysWhichButtonWasPressed(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, nil)

	rec := postForm(t, h, url.Values{
		"source": {"input"}, "command": {"generate_and_synthesize"},
		"input_uri": {"gs://in/x.md"}, "mode": {"tech_solo"},
	})

	if !strings.Contains(rec.Body.String(), "台本と音声の作成を受け付けました") {
		t.Error("the wording does not say both were made together")
	}
}

// TestSubmitValidatesBeforeEnqueuing verifies that the enqueue procedure is gathered in one place.
//
// Validate → record → enqueue had been copied to 5 places, and one of them (the detail page's save
// path) lacked validation. The page still worked normally and only tasks that always fail
// piled up, so there was no chance to notice.
func TestSubmitValidatesBeforeEnqueuing(t *testing.T) {
	t.Parallel()

	t.Run("does not enqueue when validation fails", func(t *testing.T) {
		t.Parallel()

		queue := &capturingQueue{}
		h := &Handler{queue: queue}

		// A request with an empty command. Validate rejects it.
		status, err := h.submit(context.Background(), domain.Request{OutputURI: "gs://b/o.wav"})

		if err == nil {
			t.Fatal("a request that should fail validation passed")
		}
		if status != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
		}
		if queue.calls != 0 {
			t.Errorf("enqueued %d times although validation failed", queue.calls)
		}
	})

	t.Run("enqueues and returns 202 when it passes", func(t *testing.T) {
		t.Parallel()

		queue := &capturingQueue{}
		h := &Handler{queue: queue}
		req := domain.Request{
			Command:   domain.CommandSynthesize,
			JobID:     "voice-20260814-020913-b1b8b2f9e8d7",
			OutputURI: "gs://b/o.wav",
		}

		status, err := h.submit(context.Background(), req)

		if err != nil {
			t.Fatalf("submit() error = %v", err)
		}
		if status != http.StatusAccepted {
			t.Errorf("status = %d, want %d", status, http.StatusAccepted)
		}
		if queue.calls != 1 || queue.got.JobID != req.JobID {
			t.Errorf("not enqueued: calls=%d got=%+v", queue.calls, queue.got)
		}
	})
}
