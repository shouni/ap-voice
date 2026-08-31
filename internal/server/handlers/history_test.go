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
	"github.com/shouni/go-job-firestore/jobfirestore"

	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
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
	return domain.Script{}, fmt.Errorf("台本がまだありません: %w", repository.ErrScriptNotFound)
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
		State: jobfirestore.StateFailed,
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
