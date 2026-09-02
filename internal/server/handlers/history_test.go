package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/gcp-kit/jobstatus"

	"github.com/shouni/ap-voice/internal/domain"
)

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

// TestDetailOpensWithoutAScript は、台本がまだ無いジョブでも詳細画面が開くことを
// 検証します。
//
// ここが開かないと、そのジョブは消せません。削除はこの画面からしかできず、
// 台本を書く前に失敗したジョブこそ消したいものです。以前は台本を読めない時点で
// 502 を返しており、履歴に並んだまま手の届かないジョブになっていました。
func TestDetailOpensWithoutAScript(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	h := detailHandler(t, &stateOnlyRepo{status: domain.JobStatus{
		JobID: jobID,
		State: jobstatus.StateFailed,
		Error: "AIモデルが空のスクリプトを返しました",
	}})

	req := httptest.NewRequest(http.MethodGet, "/history/"+jobID, nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Detail(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	// 失敗の理由が人の目に触れるのは、Slack 通知を除けばこの画面だけです。
	for _, want := range []string{"このジョブは失敗しました", "AIモデルが空のスクリプトを返しました", "このジョブを削除"} {
		if !strings.Contains(body, want) {
			t.Errorf("画面に %q がありません", want)
		}
	}
}

// getScript は GET /history/{jobID}/script を呼びます。
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

// TestScriptDownloadsTheStoredScript は、保存済みの台本がそのまま JSON として
// 落ちてくることを検証します。
//
// ファイル名を付ける Content-Disposition が要ります。無いとブラウザは
// JSON を画面に表示するだけで、ダウンロードにならない経路があります。
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
		t.Fatalf("台本が JSON として読めません: %v (body=%q)", err, rec.Body.String())
	}
	if got.Title != want.Title || len(got.Lines) != len(want.Lines) || got.Lines[0] != want.Lines[0] {
		t.Errorf("script = %+v, want %+v", got, want)
	}
}

// TestScriptRejectsBadJobID は、ジョブ ID の検証を通ることを確認します。
// ID はそのままファイル名になるため、素通りさせられません。
func TestScriptRejectsBadJobID(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &storedScriptRepo{})

	rec := getScript(t, h, "../../etc/passwd")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestRegenerateReusesTheRecordedInput は、作り直しが記録に残った入力ソースを
// そのまま使うことを検証します。
//
// 何を読ませたかは台本にも成果物にも残らず、失敗したジョブには台本すらありません。
// 記録から復元できなければ、利用者が URL を控えているかどうかに懸かります。
func TestRegenerateReusesTheRecordedInput(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	repo := &stateOnlyRepo{status: domain.JobStatus{
		JobID:    jobID,
		State:    jobstatus.StateFailed,
		Error:    "AIモデルが空のスクリプトを返しました",
		InputURI: "https://example.com/article",
		Mode:     "tech_solo",
		AIModel:  "gemini-test",
	}}
	queue := &capturingQueue{}
	h := detailHandler(t, repo)
	h.queue = queue

	rec := httptest.NewRecorder()
	h.Regenerate(rec, postWithJobID(jobID))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if queue.calls != 1 {
		t.Fatalf("投入回数 = %d, want 1", queue.calls)
	}
	got := queue.got
	if got.Command != domain.CommandGenerate {
		t.Errorf("Command = %q, want %q", got.Command, domain.CommandGenerate)
	}
	// ジョブ ID は変えません。作り直しなので履歴に 2 件並べる意味がありません。
	if got.JobID != jobID {
		t.Errorf("JobID = %q, want %q", got.JobID, jobID)
	}
	if got.InputURI != "https://example.com/article" {
		t.Errorf("InputURI = %q, 記録から復元できていません", got.InputURI)
	}
	if got.Mode != "tech_solo" || got.AIModel != "gemini-test" {
		t.Errorf("mode/model = %q/%q, 記録から復元できていません", got.Mode, got.AIModel)
	}
}

// TestRegenerateRefusesWithoutAnInput は、入力ソースの記録が無いジョブを
// 作り直さないことを検証します。
//
// 持ち込みの台本（台本 JSON タブ・API の script）がこれです。作り直す先が
// 無いので、押せてしまうと必ず失敗するタスクが積まれます。
func TestRegenerateRefusesWithoutAnInput(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	repo := &stateOnlyRepo{status: domain.JobStatus{JobID: jobID, State: jobstatus.StateSucceeded}}
	queue := &capturingQueue{}
	h := detailHandler(t, repo)
	h.queue = queue

	rec := httptest.NewRecorder()
	h.Regenerate(rec, postWithJobID(jobID))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if queue.calls != 0 {
		t.Errorf("入力ソースが無いのに %d 回投入しています", queue.calls)
	}
}

// postWithJobID は、jobID を埋めた POST を組み立てます。
func postWithJobID(jobID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/history/"+jobID+"/regenerate", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
}
