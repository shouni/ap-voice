package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/jobstatus"
)

// TestAPIEnqueueRecordsQueuedBeforeEnqueueing verifies that the state is written before the enqueue.
//
// The worker reads the state before the delivered task. Cloud Tasks delivers within tens of
// milliseconds, so in the opposite order a queued arriving late could overwrite the running the
// worker wrote. ap-story actually hit this with the same ordering.
func TestJobCreateJSONRecordsQueuedBeforeEnqueueing(t *testing.T) {
	t.Parallel()

	var order []string
	var saved domain.JobStatus
	h := apiHandler(t, &savingRepo{})
	h.status = jobstatus.NewRecorder[domain.JobStatus](recordingStore{order: &order, saved: &saved})
	h.queue = recordingQueue{order: &order}

	req := httptest.NewRequest("POST", "/jobs",
		strings.NewReader(`{"command":"generate","input_uri":"gs://in/x.txt","mode":"tech_solo"}`))
	rec := httptest.NewRecorder()
	req.Header.Set("Content-Type", "application/json")
	h.JobCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(order) != 2 || order[0] != "status" || order[1] != "enqueue" {
		t.Errorf("order = %v, want [status enqueue]", order)
	}

	// The response is the same envelope as the sibling services (status + job_id).
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != string(jobstatus.StateQueued) {
		t.Errorf("status = %q, want %q", body["status"], jobstatus.StateQueued)
	}
	if body["job_id"] == "" {
		t.Error("job_id is empty")
	}
	// The job URL goes in Location so the polling target is known without reading the body.
	if loc := rec.Header().Get("Location"); loc != "/jobs/"+body["job_id"] {
		t.Errorf("Location = %q, want /jobs/%s", loc, body["job_id"])
	}
	// The mode is known only at enqueue time. Miss it here and
	// the only way left is to infer the speaker combination from the finished script.
	if saved.Mode != "tech_solo" {
		t.Errorf("Mode = %q, want tech_solo", saved.Mode)
	}
}

// TestAPIEnqueueCreatesJobFromSuppliedScript verifies that a new job can be created from
// a supplied script.
//
// No existing job is needed. The location follows from the job ID alone and SaveScript does not
// check that the job exists, so minting an ID and saving is the creation.
// Without this, having your own script spoken required the detour of running a throwaway
// generation once and then overwriting it.
func TestJobCreateJSONCreatesJobFromSuppliedScript(t *testing.T) {
	t.Parallel()

	var order []string
	repo := &savingRepo{}
	h := apiHandler(t, repo)
	h.status = jobstatus.NewRecorder[domain.JobStatus](recordingStore{order: &order})
	h.queue = recordingQueue{order: &order}

	rec := postJSON(t, h.JobCreate, "/jobs", `{
		"command":"synthesize",
		"script":{"title":"漫才","lines":[
			{"speaker":"ずんだもん","style":"ノーマル","text":"どうもなのだ"},
			{"speaker":"四国めたん","style":"ノーマル","text":"どうも"}
		]}
	}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}

	// The script is saved before the enqueue. It does not ride on the task, so
	// the worker can use its existing path of reading the stored script as is.
	if len(repo.saved.Lines) != 2 || repo.saved.Title != "漫才" {
		t.Errorf("the saved script differs: %+v", repo.saved)
	}
	if len(order) != 2 || order[0] != "status" || order[1] != "enqueue" {
		t.Errorf("order = %v, want [status enqueue]", order)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["job_id"] == "" {
		t.Error("no new job ID returned")
	}
	if body["command"] != string(domain.CommandSynthesize) {
		t.Errorf("command = %q", body["command"])
	}
}

// TestAPIEnqueueRejectsBadSuppliedScript verifies that a supplied script goes through the same
// validation. If one side were looser, non-existent pairs would get in through it.
func TestJobCreateJSONRejectsBadSuppliedScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "no script",
			body:    `{"command":"synthesize"}`,
			wantErr: "script が要ります",
		},
		{
			name:    "speaker that does not exist",
			body:    `{"command":"synthesize","script":{"lines":[{"speaker":"誰か","style":"ノーマル","text":"本文"}]}}`,
			wantErr: "一覧にありません",
		},
		{
			// 春日部つむぎ's only talk style is 「ノーマル」.
			name:    "style the speaker does not have",
			body:    `{"command":"synthesize","script":{"lines":[{"speaker":"春日部つむぎ","style":"セクシー","text":"本文"}]}}`,
			wantErr: "というスタイルはありません",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &savingRepo{}
			h := apiHandler(t, repo)
			h.queue = recordingQueue{order: &[]string{}}

			rec := postJSON(t, h.JobCreate, "/jobs", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("body = %s, want it to contain %q", rec.Body.String(), tt.wantErr)
			}
			if repo.calls != 0 {
				t.Error("saved although it was rejected")
			}
		})
	}
}

// TestAPIEnqueueResolvesRecipeFromMusicJobID verifies that the recipe's location is built from
// a music job ID.
//
// ap-voice owns the rule. Making callers assemble the gs:// would mean telling everyone
// whenever the location changes. It goes through the same function as the page's
// 「楽曲レシピ」 tab, so the two cannot drift.
func TestJobCreateJSONResolvesRecipeFromMusicJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := apiHandler(t, nil)
	h.queue = queue
	h.musicBucket = "music-bucket"

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(
		`{"command":"generate","music_job_id":"music-20260814-031712-5c812debb05f","mode":"music_promo"}`))
	rec := httptest.NewRecorder()
	req.Header.Set("Content-Type", "application/json")
	h.JobCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	const want = "gs://music-bucket/music/music-20260814-031712-5c812debb05f/recipe.json"
	if queue.got.InputURI != want {
		t.Errorf("InputURI = %q, want %q", queue.got.InputURI, want)
	}
}

// TestAPIEnqueueRejectsBadMusicJobID verifies that a malformed job ID is rejected before the
// enqueue, so that no job points at a location that does not exist.
func TestJobCreateJSONRejectsBadMusicJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := apiHandler(t, nil)
	h.queue = queue
	h.musicBucket = "music-bucket"

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(
		`{"command":"generate","music_job_id":"not-a-job-id","mode":"music_promo"}`))
	rec := httptest.NewRecorder()
	req.Header.Set("Content-Type", "application/json")
	h.JobCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if queue.calls != 0 {
		t.Errorf("enqueued although it was rejected: %d times", queue.calls)
	}
}
