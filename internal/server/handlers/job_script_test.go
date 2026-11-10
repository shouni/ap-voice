package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-voice/internal/domain"
)

// The same route answers in the representation the other side asks for.
//
// The MCP server's baseClient sends Accept: application/json on every request, so
// even with a single route the machine side keeps receiving JSON.
func TestScriptServesBothAudiences(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	h := &Handler{repo: &storedScriptRepo{}}

	t.Run("returns plain JSON to machines", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/"+jobID+"/script", "application/json", jobID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("Content-Disposition = %q, want empty (machines do not receive it as a file)", got)
		}
	})

	// If opening it from the page only displayed the body, it could be checked but not kept locally.
	t.Run("returns a file to people", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/"+jobID+"/script", browserAccept, jobID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Disposition"); got == "" {
			t.Error("no Content-Disposition (it would not download)")
		}
	})

	// The same URL changes its content with Accept, so caches must be told.
	t.Run("sets Vary: Accept", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/"+jobID+"/script", "application/json", jobID))

		if got := rec.Header().Get("Vary"); got != "Accept" {
			t.Errorf("Vary = %q, want %q", got, "Accept")
		}
	})
}

// putScript calls PUT /api/jobs/{jobID}/script.
func putScript(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("PUT", "/jobs/"+jobID+"/script", strings.NewReader(body))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.ScriptUpdate(rec, req)
	return rec
}

// TestAPIUpdateScriptSharesValidationWithTheForm verifies that the API goes through the same
// validation as the page.
//
// If one side were looser, non-existent pairs would get in through it. At synthesis
// they silently fall back to the default, so once saved they show up only as
// "it speaks in a different voice from the one I chose".
func TestScriptUpdateSharesValidationWithTheForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "speaker that does not exist",
			body:       `{"title":"t","lines":[{"speaker":"存在しない","style":"ノーマル","text":"本文"}]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "一覧にありません",
		},
		{
			// 春日部つむぎ's only talk style is 「ノーマル」.
			name:       "style the speaker does not have",
			body:       `{"title":"t","lines":[{"speaker":"春日部つむぎ","style":"ヒソヒソ","text":"本文"}]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "というスタイルはありません",
		},
		{
			name:       "empty script",
			body:       `{"title":"t","lines":[]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "台本が空です",
		},
		{
			name:       "broken JSON",
			body:       `{"title":`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "JSONの解釈に失敗しました",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &savingRepo{}
			rec := putScript(t, apiHandler(t, repo), tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("body = %s, want it to contain %q", rec.Body.String(), tt.wantErr)
			}
			// What was rejected is not saved.
			if repo.calls != 0 {
				t.Errorf("saved %d times although validation failed", repo.calls)
			}
		})
	}
}

// TestAPIUpdateScriptSavesAndReturnsTheCleanedScript verifies that an accepted script is saved
// in its cleaned-up form and returned as is.
func TestScriptUpdateSavesAndReturnsTheCleanedScript(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	rec := putScript(t, apiHandler(t, repo),
		`{"title":"  題名  ","lines":[
			{"speaker":"ずんだもん","style":"あまあま","text":"  前後の空白は落ちるのだ  "},
			{"speaker":"四国めたん","style":"ノーマル","text":"   "},
			{"speaker":"四国めたん","style":"セクシー","text":"三行目"}
		]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got domain.Script
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}

	// Lines with empty text are dropped. It is also the way to delete a line.
	if len(got.Lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(got.Lines))
	}
	if got.Title != "題名" {
		t.Errorf("Title = %q, want 題名", got.Title)
	}
	if got.Lines[0].Text != "前後の空白は落ちるのだ" {
		t.Errorf("surrounding whitespace remains in the text: %q", got.Lines[0].Text)
	}
	// What was returned and what was saved are the same.
	if repo.calls != 1 {
		t.Fatalf("save count = %d, want 1", repo.calls)
	}
	if len(repo.saved.Lines) != len(got.Lines) || repo.saved.Title != got.Title {
		t.Errorf("saved %+v and response %+v disagree", repo.saved, got)
	}
}

// TestAPIUpdateScriptRejectsBadJobID verifies that an invalid job ID is rejected before saving.
func TestScriptUpdateRejectsBadJobID(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	h := apiHandler(t, repo)

	req := httptest.NewRequest("PUT", "/jobs/x/script", strings.NewReader(`{"lines":[]}`))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", "../../evil")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.ScriptUpdate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if repo.calls != 0 {
		t.Error("saved with an invalid job ID")
	}
}

// TestSynthesizeDispatchesOnBody verifies that POST /jobs/{jobID}/synthesize routes by the body's
// shape.
//
// JSON (no body) synthesises the stored script as is, and the form saves the script being edited
// before synthesising. They share one URL, so if this branch broke, machine calls would fall into
// form parsing, or the page's save would be silently skipped.
func TestSynthesizeDispatchesOnBody(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	withJobID := func(req *http.Request) *http.Request {
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("jobID", jobID)
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	}

	t.Run("JSON synthesises the stored script", func(t *testing.T) {
		t.Parallel()

		var order []string
		repo := &savingStateRepo{}
		h := apiHandler(t, repo)
		h.queue = recordingQueue{order: &order}

		req := withJobID(httptest.NewRequest("POST", "/jobs/"+jobID+"/synthesize", nil))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.Synthesize(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		if repo.calls != 0 {
			t.Errorf("the script was saved although it is JSON (%d times)", repo.calls)
		}
		if loc := rec.Header().Get("Location"); loc != "/jobs/"+jobID {
			t.Errorf("Location = %q, want /jobs/%s", loc, jobID)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["command"] != string(domain.CommandSynthesize) {
			t.Errorf("command = %q, want synthesize", body["command"])
		}
	})

	t.Run("the form saves the script before synthesising", func(t *testing.T) {
		t.Parallel()

		var order []string
		repo := &savingStateRepo{}
		h := detailHandler(t, repo)
		h.queue = recordingQueue{order: &order}

		form := url.Values{}
		form.Set("title", "t")
		form.Add("speaker", "ずんだもん")
		form.Add("style", "ノーマル")
		form.Add("text", "こんにちは")
		req := withJobID(httptest.NewRequest("POST", "/jobs/"+jobID+"/synthesize", strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.Synthesize(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		if repo.calls != 1 {
			t.Errorf("script save count = %d, want 1", repo.calls)
		}
		if len(order) != 1 || order[0] != "enqueue" {
			t.Errorf("enqueue = %v, want [enqueue]", order)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html (the page is re-rendered)", ct)
		}
	})
}

// getScript calls GET /history/{jobID}/script.
func getScript(t *testing.T, h *Handler, jobID string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/history/"+jobID+"/script", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Script(rec, req)
	return rec
}

// TestScriptDownloadsTheStoredScript verifies that the stored script comes down as JSON
// unchanged.
//
// A Content-Disposition naming the file is needed. Without it, on some paths the browser just
// displays the JSON instead of downloading it.
func TestScriptDownloadsTheStoredScript(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	want := domain.Script{
		Title: "テスト台本",
		Lines: []domain.ScriptLine{
			{Speaker: "ずんだもん", Style: "ノーマル", Text: "こんにちはなのだ"},
		},
	}
	h := apiHandler(t, &storedScriptRepo{script: want})

	rec := getScript(t, h, jobID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="`+jobID+`.json"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}

	var got domain.Script
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the script is not readable as JSON: %v (body=%q)", err, rec.Body.String())
	}
	if got.Title != want.Title || len(got.Lines) != len(want.Lines) || got.Lines[0] != want.Lines[0] {
		t.Errorf("script = %+v, want %+v", got, want)
	}
}

// TestScriptRejectsBadJobID confirms that the job ID goes through validation.
// The ID becomes the file name as is, so it cannot be let through.
func TestScriptRejectsBadJobID(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &storedScriptRepo{})

	rec := getScript(t, h, "../../etc/passwd")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
