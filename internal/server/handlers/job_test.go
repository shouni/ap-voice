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
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/gcp-kit/jobstatus"
)

// 不正なジョブ ID のエラーも、要求された表現で返ること。
// 機械に HTML のエラーページを返しても解釈できません。
func TestJobIDErrorFollowsTheRequestedFormat(t *testing.T) {
	t.Parallel()

	h := &Handler{repo: &storedScriptRepo{}}

	rec := httptest.NewRecorder()
	h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/bad/script", "application/json", "../etc/passwd"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}
}

// getJobStatus は GET /history/{jobID}/status を呼びます。
func getJobStatus(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("GET", "/jobs/"+jobID, nil)
	req.Header.Set("Accept", "application/json")
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Job(rec, req)
	return rec
}

// 未記録（ErrNotFound）は 404。MCP サーバーが unknown として扱う正常系。
func TestJobStatusNotRecordedIs404(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{err: fmt.Errorf("%w: 未記録", jobstatus.ErrNotFound)})
	if rec := getJobStatus(t, h); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// 読めなかっただけ（ErrUnavailable）のときに 404 と答えないこと。
// 混ぜると、ストレージ障害の間すべてのジョブが「記録が無い」ように見え、
// ポーリング側が unknown として静かに受け入れてしまう。
func TestJobStatusUnreadableIsNot404(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{err: fmt.Errorf("%w: storage down", jobstatus.ErrUnavailable)})
	rec := getJobStatus(t, h)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("status = 404: 読めないだけのジョブを未記録と答えている: %s", rec.Body.String())
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

// 読めたら 200 で jobstatus.Status のフラットな形のまま返すこと。
func TestJobStatusReturnsRecord(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &statusRepo{status: domain.JobStatus{
		State:    jobstatus.StateRunning,
		AudioURI: "gs://test/voice/x/audio.wav",
	}})
	rec := getJobStatus(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答が JSON ではない: %v", err)
	}
	if got["state"] != string(jobstatus.StateRunning) {
		t.Errorf("state = %v, want running", got["state"])
	}
	if got["audio_uri"] != "gs://test/voice/x/audio.wav" {
		t.Errorf("audio_uri = %v（埋め込みのフラット展開が崩れている）", got["audio_uri"])
	}
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

	req := httptest.NewRequest(http.MethodGet, "/jobs/"+jobID, nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Job(rec, req)

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
