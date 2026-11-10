package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/memio"

	"github.com/shouni/ap-voice/internal/domain"
)

// fakeStore holds a path → content map in place of GCS.
// It counts reads, to check that the listing's round trips do not grow with the number of jobs.
// fakeStore is a storage fake that wraps memio.
//
// Storage behaviour such as folding listings and how absence is reported is handled by memio
// (it passes the same conformance suite as the real handlers). What remains here is only
// the call counts, "how many opens" and "how many writes", which are needed to check
// "whether reading titles stays within one page".
type fakeStore struct {
	remoteio.Store
	h *memio.Handler

	opened  int32
	exists  int32
	written int32
}

func newFakeStore() *fakeStore {
	f := &fakeStore{h: memio.New(memio.WithScheme(remoteio.SchemeGCS))}
	f.Store = remoteio.NewStore(f.h)
	return f
}

func (f *fakeStore) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	atomic.AddInt32(&f.opened, 1)
	return f.Store.Open(ctx, name)
}

func (f *fakeStore) Exists(ctx context.Context, name string) (bool, error) {
	atomic.AddInt32(&f.exists, 1)
	return f.Store.Exists(ctx, name)
}

func (f *fakeStore) Write(ctx context.Context, name string, r io.Reader, opts ...remoteio.WriteOption) error {
	atomic.AddInt32(&f.written, 1)
	return f.Store.Write(ctx, name, r, opts...)
}

// Sub delegates to the library's Sub. Using the Sub promoted from the embedding as is would
// make the scope's base the embedded Store, letting the counting above be bypassed.
func (f *fakeStore) Sub(prefix string) remoteio.Store { return remoteio.Sub(f, prefix) }

// put places a prerequisite object.
func (f *fakeStore) put(t *testing.T, uri, body string) {
	t.Helper()
	if err := f.h.Seed(uri, []byte(body)); err != nil {
		t.Fatalf("seed(%s) error = %v", uri, err)
	}
}

// drop removes the target.
func (f *fakeStore) drop(t *testing.T, uri string) {
	t.Helper()
	if err := f.Delete(context.Background(), uri); err != nil {
		t.Fatalf("delete(%s) error = %v", uri, err)
	}
}

// uris returns the URIs of the stored objects in lexical order.
func (f *fakeStore) uris() []string { return f.h.URIs() }

// newStore builds a store holding n jobs (script + audio).
// Job IDs follow the go-utils/jobid format (voice-{date}-{time}-{hex12}).
func newStore(t *testing.T, n int) (*fakeStore, []string) {
	t.Helper()

	store := newFakeStore()
	ids := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("voice-20260814-%06d-abcdef123456", i)
		ids = append(ids, id)

		script, err := json.Marshal(map[string]any{
			"title": fmt.Sprintf("台本 %d", i),
			"lines": []map[string]string{{"speaker": "ずんだもん", "style": "ノーマル", "text": "本文"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		store.put(t, "gs://test/voice/"+id+"/audio.json", string(script))
		store.put(t, "gs://test/voice/"+id+"/audio.wav", "RIFF")
	}
	return store, ids
}

func newRepo(t *testing.T, store *fakeStore) *Repository {
	t.Helper()

	// No Firestore client is passed. What is verified here is reading and writing artifacts, which
	// does not touch job state. Delete reports success even if clearing the state fails
	// (the orphan is left in a warning log), so Delete's checks pass with this setup too.
	repo, err := NewRepository(store, "test", NewJobStatusStore(nil))
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	return repo
}

// fakeStatus holds the states to return as is, in place of Firestore.
//
// It is needed to check that the listing reads no artifacts at all. The real Store would need
// the emulator, a dependency on an external process just for this check.
type fakeStatus struct {
	statuses []domain.JobStatus
}

// Get looks it up in the records it holds. Missing is ErrNotFound, as in the real one.
// Delete answers differently for "has a record" and "has none", so this cannot be uniform.
func (f *fakeStatus) Get(_ context.Context, jobID string) (domain.JobStatus, error) {
	for _, status := range f.statuses {
		if status.JobID == jobID {
			return status, nil
		}
	}
	return domain.JobStatus{}, jobstatus.ErrNotFound
}

func (f *fakeStatus) Save(context.Context, string, domain.JobStatus) error { return nil }

// Delete removes the record, so the caller can check it really went.
func (f *fakeStatus) Delete(_ context.Context, jobID string) error {
	f.statuses = slices.DeleteFunc(f.statuses, func(status domain.JobStatus) bool {
		return status.JobID == jobID
	})
	return nil
}

func (f *fakeStatus) List(_ context.Context, page, perPage int, _ ...jobstatus.ListOption) ([]domain.JobStatus, jobstatus.PageMeta, error) {
	// Like the real one, it returns only one page.
	total := len(f.statuses)
	from := min((page-1)*perPage, total)
	to := min(from+perPage, total)
	return f.statuses[from:to], jobstatus.PageMeta{Page: page, PerPage: perPage, Total: total}, nil
}

// withStatuses returns a Repository with the states Firestore returns swapped.
func withStatuses(t *testing.T, store *fakeStore, statuses ...domain.JobStatus) *Repository {
	t.Helper()

	repo := newRepo(t, store)
	repo.status = &fakeStatus{statuses: statuses}
	return repo
}

// TestListDoesNotReadArtifacts verifies that the listing reads no artifacts at all.
//
// It used to scan the whole bucket and open a page's worth of scripts to get titles.
// The scan grew with the number of jobs, without bound. Every value needed is now
// in the state, so the store is never touched. Without counting here, an implementation that
// re-fetches titles or audio presence from the artifacts "just in case" could come back at any time.
func TestListDoesNotReadArtifacts(t *testing.T) {
	t.Parallel()

	// The store is empty. The listing still returns.
	store := newFakeStore()
	repo := withStatuses(t, store,
		domain.JobStatus{JobID: "voice-20260830-090000-aaaaaaaaaaaa", Title: "新しい方"},
		domain.JobStatus{JobID: "voice-20260829-090000-bbbbbbbbbbbb", Title: "古い方"},
	)

	jobs, meta, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("count = %d, want 2", len(jobs))
	}
	if meta.Total != 2 {
		t.Errorf("Total = %d, want 2", meta.Total)
	}
	if store.opened != 0 || store.exists != 0 {
		t.Errorf("touches the store: opened=%d exists=%d", store.opened, store.exists)
	}
	if jobs[0].Title != "新しい方" {
		t.Errorf("title = %q, not taken from the state", jobs[0].Title)
	}
	// The creation time is recovered from the job ID.
	if jobs[0].CreatedAt.IsZero() {
		t.Error("the creation time is empty")
	}
}

// TestListFallsBackToJobID verifies that a job without a title does not vanish from the listing.
//
// If it vanished, the way to delete it would be lost too. Jobs that failed before their title
// was settled are exactly the ones to delete.
func TestListFallsBackToJobID(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260830-090000-aaaaaaaaaaaa"
	repo := withStatuses(t, newFakeStore(),
		domain.JobStatus{JobID: jobID, State: jobstatus.StateFailed})

	jobs, _, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("count = %d, want 1", len(jobs))
	}
	if jobs[0].Title != jobID {
		t.Errorf("title = %q, want the job ID", jobs[0].Title)
	}
}

// TestListMarksAudioFromTheRecordedURI verifies that audio presence is decided from the record,
// not the artifacts. generate ends at the script, so no location is set.
func TestListMarksAudioFromTheRecordedURI(t *testing.T) {
	t.Parallel()

	repo := withStatuses(t, newFakeStore(),
		domain.JobStatus{
			JobID:    "voice-20260830-090000-aaaaaaaaaaaa",
			AudioURI: "gs://test/voice/voice-20260830-090000-aaaaaaaaaaaa/audio.wav",
		},
		domain.JobStatus{JobID: "voice-20260829-090000-bbbbbbbbbbbb"},
	)

	jobs, _, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !jobs[0].HasAudio {
		t.Error("false although the audio location is recorded")
	}
	if jobs[1].HasAudio {
		t.Error("a script-only job is marked true")
	}
}

// TestHasAudioChecksTheObjectItself verifies that audio presence is asked of the store directly.
//
// It differs from the listing's decision. The detail page trusts the object over the record (one
// extra query is cheaper than the playback section vanishing because the record missed it).
func TestHasAudioChecksTheObjectItself(t *testing.T) {
	t.Parallel()

	store, ids := newStore(t, 2)
	repo := newRepo(t, store)

	has, err := repo.HasAudio(context.Background(), ids[0])
	if err != nil {
		t.Fatalf("HasAudio() error = %v", err)
	}
	if !has {
		t.Error("returned false although the audio exists")
	}

	// A script-only job must be false.
	store.drop(t, "gs://test/voice/"+ids[1]+"/audio.wav")
	if has, err := repo.HasAudio(context.Background(), ids[1]); err != nil || has {
		t.Errorf("HasAudio() = %v, %v; want false, nil", has, err)
	}
}

// TestHasAudioRejectsBadJobID verifies that an invalid job ID is rejected before it goes into a path.
func TestHasAudioRejectsBadJobID(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 1)
	repo := newRepo(t, store)

	if _, err := repo.HasAudio(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("an invalid job ID got through")
	}
	if store.exists != 0 {
		t.Error("queried the store before validating")
	}
}

// TestDeleteRemovesTheWholeJobPrefix verifies that deletion removes everything under the job
// and touches no other job.
func TestDeleteRemovesTheWholeJobPrefix(t *testing.T) {
	t.Parallel()

	store, ids := newStore(t, 2)
	repo := newRepo(t, store)

	if err := repo.Delete(context.Background(), ids[0]); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	remaining := store.uris()
	for _, uri := range remaining {
		if strings.Contains(uri, ids[0]) {
			t.Errorf("left behind: %s", uri)
		}
	}
	if len(remaining) != 2 {
		t.Errorf("other jobs were deleted too: %v", remaining)
	}
}

// TestDeleteRemovesTheRecordOfAJobWithNoArtifacts verifies that a job without any artifacts
// can be deleted.
//
// A job that failed before writing a script looks like this. It used to answer "not found" here,
// and the detail page could not open without a script, so the job sat in the history
// undeletable from anywhere. This is exactly the job one wants to delete.
func TestDeleteRemovesTheRecordOfAJobWithNoArtifacts(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260830-090000-aaaaaaaaaaaa"
	status := &fakeStatus{statuses: []domain.JobStatus{
		{JobID: jobID, State: jobstatus.StateFailed, Error: "生成に失敗しました"},
	}}
	repo := newRepo(t, newFakeStore())
	repo.status = status

	if err := repo.Delete(context.Background(), jobID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(status.statuses) != 0 {
		t.Error("the record remains; it will not leave the history")
	}
}

// TestDeleteReportsUnknownJob verifies that a job with neither artifacts nor a record yields
// ErrJobNotFound.
//
// "Pointed at an unknown job" and "cleaned up a job that failed halfway" are different.
// Mixing them would make it impossible to return 404 for the former.
func TestDeleteReportsUnknownJob(t *testing.T) {
	t.Parallel()

	repo := withStatuses(t, newFakeStore())

	err := repo.Delete(context.Background(), "voice-20260830-090000-aaaaaaaaaaaa")
	if !errors.Is(err, ErrJobNotFound) {
		t.Errorf("Delete() error = %v, want ErrJobNotFound", err)
	}
}

// TestLoadReportsMissingScript verifies that a script not written yet yields ErrScriptNotFound.
//
// If the caller cannot tell "not yet" from "could not read", a job mid-generation and a
// GCS outage become the same failure. The detail page must show the state for the former
// and return 502 for the latter.
func TestLoadReportsMissingScript(t *testing.T) {
	t.Parallel()

	repo := newRepo(t, newFakeStore())

	_, err := repo.Load(context.Background(), "voice-20260830-090000-aaaaaaaaaaaa")
	if !errors.Is(err, domain.ErrScriptNotFound) {
		t.Errorf("Load() error = %v, want domain.ErrScriptNotFound", err)
	}
}

// TestListCarriesState verifies that the listing carries the progress state.
//
// Going by artifacts alone, a running job and one that ended at the script look the same.
// The listing could not tell whether to wait or to delete and retry.
func TestListCarriesState(t *testing.T) {
	t.Parallel()

	repo := withStatuses(t, newFakeStore(),
		domain.JobStatus{
			JobID: "voice-20260830-090000-aaaaaaaaaaaa",
			State: jobstatus.StateFailed,
			Error: "AIモデルが空のスクリプトを返しました",
		},
		domain.JobStatus{JobID: "voice-20260829-090000-bbbbbbbbbbbb", State: jobstatus.StateRunning},
	)

	jobs, _, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if jobs[0].State != jobstatus.StateFailed {
		t.Errorf("State = %q, want failed", jobs[0].State)
	}
	if jobs[0].Error == "" {
		t.Error("the failure reason is not carried")
	}
	if jobs[1].State != jobstatus.StateRunning {
		t.Errorf("State = %q, want running", jobs[1].State)
	}
}

// TestSaveWritesBackToTheStoredScript verifies that an edited script is saved to the same place
// it is read from.
//
// If the save and read locations drifted, it would break in a hard-to-notice way: edited, yet
// synthesised from the old script.
func TestSaveWritesBackToTheStoredScript(t *testing.T) {
	t.Parallel()

	store, ids := newStore(t, 1)
	repo := newRepo(t, store)
	id := ids[0]

	edited := domain.Script{
		Title: "直した題名",
		Lines: []domain.ScriptLine{{Speaker: "四国めたん", Style: "ノーマル", Text: "直した本文"}},
	}
	if err := repo.SaveScript(context.Background(), id, edited); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := repo.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Title != edited.Title || len(got.Lines) != 1 || got.Lines[0] != edited.Lines[0] {
		t.Errorf("the script read back differs: %+v", got)
	}
}

// TestSaveRejectsBadJobID verifies that an invalid job ID is rejected before it goes into a path.
func TestSaveRejectsBadJobID(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 1)
	repo := newRepo(t, store)

	if err := repo.SaveScript(context.Background(), "../../evil", domain.Script{Title: "x"}); err == nil {
		t.Fatal("an invalid job ID got through")
	}
	if store.written != 0 {
		t.Error("wrote before validating")
	}
}
