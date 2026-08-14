package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
)

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

// putScript は PUT /api/jobs/{jobID}/script を呼びます。
func putScript(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("PUT", "/api/jobs/"+jobID+"/script", strings.NewReader(body))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.APIUpdateScript(rec, req)
	return rec
}

// TestAPIUpdateScriptSharesValidationWithTheForm は、API が画面と同じ検証を
// 通ることを検証します。
//
// **片方だけ緩いと、そちらから実在しない組み合わせが入ります。** 合成時に
// getStyleID が既定へ黙って落とすため、保存できてしまうと「指定したのに
// 違う声で喋る」形でしか現れません。
func TestAPIUpdateScriptSharesValidationWithTheForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "実在しない話者",
			body:       `{"title":"t","lines":[{"speaker":"存在しない","style":"ノーマル","text":"本文"}]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "一覧にありません",
		},
		{
			// 春日部つむぎの talk スタイルは「ノーマル」だけです。
			name:       "話者が持たないスタイル",
			body:       `{"title":"t","lines":[{"speaker":"春日部つむぎ","style":"ヒソヒソ","text":"本文"}]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "というスタイルはありません",
		},
		{
			name:       "空の台本",
			body:       `{"title":"t","lines":[]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "台本が空です",
		},
		{
			name:       "壊れたJSON",
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
				t.Errorf("body = %s, want に %q を含む", rec.Body.String(), tt.wantErr)
			}
			// **弾いたものは保存しません。**
			if repo.calls != 0 {
				t.Errorf("検証に失敗したのに %d 回保存しています", repo.calls)
			}
		})
	}
}

// TestAPIUpdateScriptSavesAndReturnsTheCleanedScript は、通った台本が
// 整えられた形で保存され、そのまま返ることを検証します。
func TestAPIUpdateScriptSavesAndReturnsTheCleanedScript(t *testing.T) {
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
		t.Fatalf("応答のデコードに失敗しました: %v", err)
	}

	// 本文が空の行は落ちます。行を消す手段でもあります。
	if len(got.Lines) != 2 {
		t.Fatalf("行数 = %d, want 2", len(got.Lines))
	}
	if got.Title != "題名" {
		t.Errorf("Title = %q, want 題名", got.Title)
	}
	if got.Lines[0].Text != "前後の空白は落ちるのだ" {
		t.Errorf("本文の前後の空白が残っています: %q", got.Lines[0].Text)
	}
	// **返したものと保存したものが同じであること。**
	if repo.calls != 1 {
		t.Fatalf("保存回数 = %d, want 1", repo.calls)
	}
	if len(repo.saved.Lines) != len(got.Lines) || repo.saved.Title != got.Title {
		t.Errorf("保存 %+v と応答 %+v が食い違います", repo.saved, got)
	}
}

// TestAPIUpdateScriptRejectsBadJobID は、不正なジョブ ID を保存前に弾くことを検証します。
func TestAPIUpdateScriptRejectsBadJobID(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	h := apiHandler(t, repo)

	req := httptest.NewRequest("PUT", "/api/jobs/x/script", strings.NewReader(`{"lines":[]}`))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", "../../evil")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.APIUpdateScript(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if repo.calls != 0 {
		t.Error("不正なジョブIDで保存しています")
	}
}

// recordingStore は、保存の順序を記録する jobstatus.StatusStore です。
type recordingStore struct {
	order *[]string
}

func (s recordingStore) Get(context.Context, string) (jobstatus.Status, error) {
	return jobstatus.Status{}, errors.New("記録がありません")
}

func (s recordingStore) Save(context.Context, string, jobstatus.Status) error {
	*s.order = append(*s.order, "status")
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

// TestAPIEnqueueRecordsQueuedBeforeEnqueueing は、状態を投入より先に書くことを
// 検証します。
//
// **Worker は配信されたタスクより先に状態を読みます。** Cloud Tasks は数十ミリ秒で
// 届くため、順序が逆だと Worker が書いた running を、あとから届いた queued が
// 上書きしかねません。ap-story が同じ順序で実際に踏んでいます。
func TestAPIEnqueueRecordsQueuedBeforeEnqueueing(t *testing.T) {
	t.Parallel()

	var order []string
	h := apiHandler(t, &savingRepo{})
	h.status = jobstatus.NewRecorder[jobstatus.Status](recordingStore{order: &order})
	h.queue = recordingQueue{order: &order}

	req := httptest.NewRequest("POST", "/api/jobs",
		strings.NewReader(`{"command":"generate","input_uri":"gs://in/x.txt","mode":"tech_solo"}`))
	rec := httptest.NewRecorder()
	h.APIEnqueue(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if len(order) != 2 || order[0] != "status" || order[1] != "enqueue" {
		t.Errorf("順序 = %v, want [status enqueue]", order)
	}

	// 応答は御三家と同じ封筒（status + job_id）であること。
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != string(jobstatus.StateQueued) {
		t.Errorf("status = %q, want %q", body["status"], jobstatus.StateQueued)
	}
	if body["job_id"] == "" {
		t.Error("job_id が空です")
	}
}
