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

// TestAPIEnqueueRecordsQueuedBeforeEnqueueing は、状態を投入より先に書くことを
// 検証します。
//
// Worker は配信されたタスクより先に状態を読みます。Cloud Tasks は数十ミリ秒で
// 届くため、順序が逆だと Worker が書いた running を、あとから届いた queued が
// 上書きしかねません。ap-story が同じ順序で実際に踏んでいます。
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
		t.Errorf("順序 = %v, want [status enqueue]", order)
	}

	// 応答は姉妹サービスと同じ封筒（status + job_id）であること。
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
	// 本文を読まなくてもポーリング先が分かるように、Location にジョブの URL を載せます。
	if loc := rec.Header().Get("Location"); loc != "/jobs/"+body["job_id"] {
		t.Errorf("Location = %q, want /jobs/%s", loc, body["job_id"])
	}
	// モードは投入の時点でしか分かりません。ここで載せ損なうと、
	// 出来上がった台本から話者の組み合わせを推し量るしかなくなります。
	if saved.Mode != "tech_solo" {
		t.Errorf("Mode = %q, want tech_solo", saved.Mode)
	}
}

// TestAPIEnqueueCreatesJobFromSuppliedScript は、持ち込んだ台本から新しいジョブを
// 作れることを検証します。
//
// 既存ジョブが要りません。保存先はジョブ ID から決まるだけで、SaveScript は
// ジョブの存在を確かめないため、ID を発行して保存すればそれが新規作成になります。
// これが無いと、自分で書いた台本を喋らせるのに、捨てる前提の生成を 1 回
// 走らせてから上書きする迂回が要りました。
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

	// 台本は投入より先に保存されます。タスクには載せないため、
	// worker は保存済みの台本を読む既存の経路をそのまま使えます。
	if len(repo.saved.Lines) != 2 || repo.saved.Title != "漫才" {
		t.Errorf("保存された台本が違います: %+v", repo.saved)
	}
	if len(order) != 2 || order[0] != "status" || order[1] != "enqueue" {
		t.Errorf("順序 = %v, want [status enqueue]", order)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["job_id"] == "" {
		t.Error("新しいジョブ ID が返っていません")
	}
	if body["command"] != string(domain.CommandSynthesize) {
		t.Errorf("command = %q", body["command"])
	}
}

// TestAPIEnqueueRejectsBadSuppliedScript は、持ち込んだ台本にも同じ検証が
// かかることを検証します。片方だけ緩いと、そちらから実在しない組み合わせが入ります。
func TestJobCreateJSONRejectsBadSuppliedScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "script が無い",
			body:    `{"command":"synthesize"}`,
			wantErr: "script が要ります",
		},
		{
			name:    "実在しない話者",
			body:    `{"command":"synthesize","script":{"lines":[{"speaker":"誰か","style":"ノーマル","text":"本文"}]}}`,
			wantErr: "一覧にありません",
		},
		{
			// 春日部つむぎの talk スタイルは「ノーマル」だけです。
			name:    "話者が持たないスタイル",
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
				t.Errorf("body = %s, want に %q を含む", rec.Body.String(), tt.wantErr)
			}
			if repo.calls != 0 {
				t.Error("弾いたのに保存しています")
			}
		})
	}
}

// TestAPIEnqueueResolvesRecipeFromMusicJobID は、楽曲のジョブ ID から
// レシピの場所が組み立てられることを検証します。
//
// 規則は ap-voice が持ちます。呼び出し側に gs:// を組み立てさせると、
// 置き場を変えるときに全員へ知らせて回ることになります。画面の
// 「楽曲レシピ」タブと同じ関数を通るので、両者がずれることもありません。
func TestJobCreateJSONResolvesRecipeFromMusicJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := apiHandler(t, nil)
	h.queue = queue
	h.musicBucket = "ap-music"

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(
		`{"command":"generate","music_job_id":"music-20260814-031712-5c812debb05f","mode":"music_promo"}`))
	rec := httptest.NewRecorder()
	req.Header.Set("Content-Type", "application/json")
	h.JobCreate(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	const want = "gs://ap-music/music/music-20260814-031712-5c812debb05f/recipe.json"
	if queue.got.InputURI != want {
		t.Errorf("InputURI = %q, want %q", queue.got.InputURI, want)
	}
}

// TestAPIEnqueueRejectsBadMusicJobID は、形式の違うジョブ ID を投入前に
// 弾くことを検証します。存在しない場所を指すジョブを作らないためです。
func TestJobCreateJSONRejectsBadMusicJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := apiHandler(t, nil)
	h.queue = queue
	h.musicBucket = "ap-music"

	req := httptest.NewRequest("POST", "/jobs", strings.NewReader(
		`{"command":"generate","music_job_id":"not-a-job-id","mode":"music_promo"}`))
	rec := httptest.NewRecorder()
	req.Header.Set("Content-Type", "application/json")
	h.JobCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if queue.calls != 0 {
		t.Errorf("弾いたのに投入しています: %d 回", queue.calls)
	}
}
