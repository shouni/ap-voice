package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-voicevox/speaker"
)

// browserAccept is the Accept header a browser actually sends.
// It does not include application/json, so the representation falls to HTML.
const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// requestWithJobID builds a request with jobID filled in.
func requestWithJobID(method, target, accept, jobID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Accept", accept)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}

// listingRepo is a fake that remembers the requested page size and filter.
type listingRepo struct {
	ScriptRepository
	perPage int
	opts    int
}

func (r *listingRepo) List(_ context.Context, _, perPage int, opts ...jobstatus.ListOption) ([]repository.Job, jobstatus.PageMeta, error) {
	r.perPage = perPage
	r.opts = len(opts)
	return nil, jobstatus.PageMeta{}, nil
}

// savingRepo is a fake that remembers the stored script.
type savingRepo struct {
	ScriptRepository
	saved domain.Script
	calls int
}

func (r *savingRepo) SaveScript(_ context.Context, _ string, script domain.Script) error {
	r.saved = script
	r.calls++
	return nil
}

// apiHandler returns an API Handler loaded with the real speaker roster.
func apiHandler(t *testing.T, repo ScriptRepository) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return &Handler{speakers: registry, repo: repo, layout: domain.NewStorageLayout(), bucket: "test"}
}

// recordingStore is a jobstatus.StatusStore that records the order of saves.
type recordingStore struct {
	order *[]string
	// saved is the last state written. Only tests that want to inspect it pass one.
	saved *domain.JobStatus
}

func (s recordingStore) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, errors.New("no record")
}

func (s recordingStore) Save(_ context.Context, _ string, status domain.JobStatus) error {
	*s.order = append(*s.order, "status")
	if s.saved != nil {
		*s.saved = status
	}
	return nil
}

// recordingQueue is a TaskQueue that records the order of enqueues.
type recordingQueue struct {
	order *[]string
}

func (q recordingQueue) Enqueue(context.Context, domain.Request) error {
	*q.order = append(*q.order, "enqueue")
	return nil
}

// audioRepo is a fake whose audio presence can be swapped.
type audioRepo struct {
	ScriptRepository
	hasAudio bool
}

func (r audioRepo) HasAudio(context.Context, string) (bool, error) { return r.hasAudio, nil }

// stubSigner is a fake that pretends to have built a signed URL.
type stubSigner struct{ calls int }

func (s *stubSigner) SignURL(_ context.Context, path, _ string, _ time.Duration) (string, error) {
	s.calls++
	return "https://storage.googleapis.com/" + path + "?X-Goog-Signature=xxx", nil
}

// fakeReading is a fake that pretends to convert. The real dictionary is tested in adapters.
type fakeReading struct{ err error }

func (f fakeReading) ConvertToReading(text string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if text == "水面" {
		return "スイメン", nil
	}
	return text, nil
}

// postJSON calls a POST with a JSON body.
func postJSON(t *testing.T, h http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// statusRepo is a fake that swaps only the job state read.
type statusRepo struct {
	ScriptRepository
	status domain.JobStatus
	err    error
}

func (r *statusRepo) Get(context.Context, string) (domain.JobStatus, error) {
	return r.status, r.err
}

// savingStateRepo is a fake that can store a script for a job that has none yet.
type savingStateRepo struct {
	stateOnlyRepo
	saved domain.Script
	calls int
}

func (r *savingStateRepo) SaveScript(_ context.Context, _ string, script domain.Script) error {
	r.saved = script
	r.calls++
	return nil
}

// passthroughRenderer and passthroughReading only fill dependencies unrelated to
// option validation (prompts and readings are not looked at here).
type passthroughRenderer struct{}

func (passthroughRenderer) Generate(_, content string) (string, error) { return content, nil }

type passthroughReading struct{}

func (passthroughReading) ConvertToReading(text string) (string, error) { return text, nil }

// builtHandler returns a Handler built through the real NewHandler.
//
// Assembling the struct directly, as other tests do, would let the construction-only
// assembly (the per-speaker style table) pass through empty.
func builtHandler(t *testing.T) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	modes, err := assets.LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	h, err := NewHandler(HandlerOptions{
		Queue:     &capturingQueue{},
		Templates: parseTemplates(t),
		Modes:     modes,
		Models:    []string{"gemini-test"},
		Bucket:    "test",
		Repo:      &savingRepo{},
		Speakers:  registry,
		Renderer:  passthroughRenderer{},
		Reading:   passthroughReading{},
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return h
}

// storedScriptRepo is a fake that returns a stored script.
type storedScriptRepo struct {
	ScriptRepository
	script domain.Script
}

func (r *storedScriptRepo) Load(_ context.Context, _ string) (domain.Script, error) {
	return r.script, nil
}

// stateOnlyRepo is a fake for a job that has no script yet.
// Jobs look like this before generation finishes and after it fails.
type stateOnlyRepo struct {
	ScriptRepository
	status domain.JobStatus
}

func (r *stateOnlyRepo) Load(context.Context, string) (domain.Script, error) {
	return domain.Script{}, fmt.Errorf("no script yet: %w", domain.ErrScriptNotFound)
}

func (r *stateOnlyRepo) HasAudio(context.Context, string) (bool, error) { return false, nil }

func (r *stateOnlyRepo) Get(context.Context, string) (domain.JobStatus, error) {
	return r.status, nil
}

// detailHandler returns a Handler with just enough dependencies to render pages.
func detailHandler(t *testing.T, repo ScriptRepository) *Handler {
	t.Helper()

	h := apiHandler(t, repo)
	h.templates = parseTemplates(t)
	return h
}

// postWithJobID builds a POST with jobID filled in.
func postWithJobID(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/regenerate", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}
