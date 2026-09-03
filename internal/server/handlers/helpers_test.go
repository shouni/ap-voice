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

// browserAccept は、ブラウザが実際に送る Accept です。
// application/json を含まないので、表現は HTML 側に倒れます。
const browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

// requestWithJobID は、jobID を埋めたリクエストを組み立てます。
func requestWithJobID(method, target, accept, jobID string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Accept", accept)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}

// listingRepo は、要求されたページ幅と絞り込みを覚えておくフェイクです。
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

// savingRepo は、保存された台本を覚えておくフェイクです。
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

// apiHandler は、実物の話者一覧を積んだ API 用の Handler を返します。
func apiHandler(t *testing.T, repo ScriptRepository) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return &Handler{speakers: registry, repo: repo, layout: domain.NewStorageLayout(), bucket: "test"}
}

// recordingStore は、保存の順序を記録する jobstatus.StatusStore です。
type recordingStore struct {
	order *[]string
	// saved は最後に書かれた状態です。中身まで見たいテストだけが渡します。
	saved *domain.JobStatus
}

func (s recordingStore) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, errors.New("記録がありません")
}

func (s recordingStore) Save(_ context.Context, _ string, status domain.JobStatus) error {
	*s.order = append(*s.order, "status")
	if s.saved != nil {
		*s.saved = status
	}
	return nil
}

// recordingQueue は、投入の順序を記録する TaskQueue です。
type recordingQueue struct {
	order *[]string
}

func (q recordingQueue) Enqueue(context.Context, domain.Request) error {
	*q.order = append(*q.order, "enqueue")
	return nil
}

// audioRepo は音声の有無を差し替えられるフェイクです。
type audioRepo struct {
	ScriptRepository
	hasAudio bool
}

func (r audioRepo) HasAudio(context.Context, string) (bool, error) { return r.hasAudio, nil }

// stubSigner は署名付き URL を組み立てたことにするフェイクです。
type stubSigner struct{ calls int }

func (s *stubSigner) SignURL(_ context.Context, path, _ string, _ time.Duration) (string, error) {
	s.calls++
	return "https://storage.googleapis.com/" + path + "?X-Goog-Signature=xxx", nil
}

// fakeReading は、変換したことにするフェイクです。実際の辞書は adapters 側で見ます。
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

// postJSON は JSON ボディの POST を呼びます。
func postJSON(t *testing.T, h http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// statusRepo は、ジョブ状態の読み出しだけを差し替えるフェイクです。
type statusRepo struct {
	ScriptRepository
	status domain.JobStatus
	err    error
}

func (r *statusRepo) Get(context.Context, string) (domain.JobStatus, error) {
	return r.status, r.err
}

// savingStateRepo は、台本がまだ無いジョブに台本を保存できるフェイクです。
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

// passthroughRenderer と passthroughReading は、選択肢の検証に関係しない依存を
// 埋めるだけのものです（プロンプトと読みはここでは見ません）。
type passthroughRenderer struct{}

func (passthroughRenderer) Generate(_, content string) (string, error) { return content, nil }

type passthroughReading struct{}

func (passthroughReading) ConvertToReading(text string) (string, error) { return text, nil }

// builtHandler は、実物の NewHandler を通した Handler を返します。
//
// 他のテストのように構造体を直に組むと、構築時にしか行わない組み立て
// （話者ごとのスタイル表）が空のまま通ってしまいます。
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

// storedScriptRepo は、保存済みの台本を返すフェイクです。
type storedScriptRepo struct {
	ScriptRepository
	script domain.Script
}

func (r *storedScriptRepo) Load(_ context.Context, _ string) (domain.Script, error) {
	return r.script, nil
}

// stateOnlyRepo は、台本がまだ無いジョブのフェイクです。
// 生成が終わる前と、生成に失敗したジョブがこの姿になります。
type stateOnlyRepo struct {
	ScriptRepository
	status domain.JobStatus
}

func (r *stateOnlyRepo) Load(context.Context, string) (domain.Script, error) {
	return domain.Script{}, fmt.Errorf("台本がまだありません: %w", domain.ErrScriptNotFound)
}

func (r *stateOnlyRepo) HasAudio(context.Context, string) (bool, error) { return false, nil }

func (r *stateOnlyRepo) Get(context.Context, string) (domain.JobStatus, error) {
	return r.status, nil
}

// detailHandler は、画面を描けるだけの依存を積んだ Handler を返します。
func detailHandler(t *testing.T, repo ScriptRepository) *Handler {
	t.Helper()

	h := apiHandler(t, repo)
	h.templates = parseTemplates(t)
	return h
}

// postWithJobID は、jobID を埋めた POST を組み立てます。
func postWithJobID(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/regenerate", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}
