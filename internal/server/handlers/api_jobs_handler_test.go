package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/gcp-kit/jobstatus"
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
// 片方だけ緩いと、そちらから実在しない組み合わせが入ります。合成時に
// 合成時に既定へ黙って落ちるため、保存できてしまうと「指定したのに
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
			// 弾いたものは保存しません。
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
	// 返したものと保存したものが同じであること。
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

// TestAPIEnqueueRecordsQueuedBeforeEnqueueing は、状態を投入より先に書くことを
// 検証します。
//
// Worker は配信されたタスクより先に状態を読みます。Cloud Tasks は数十ミリ秒で
// 届くため、順序が逆だと Worker が書いた running を、あとから届いた queued が
// 上書きしかねません。ap-story が同じ順序で実際に踏んでいます。
func TestAPIEnqueueRecordsQueuedBeforeEnqueueing(t *testing.T) {
	t.Parallel()

	var order []string
	var saved domain.JobStatus
	h := apiHandler(t, &savingRepo{})
	h.status = jobstatus.NewRecorder[domain.JobStatus](recordingStore{order: &order, saved: &saved})
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
	// モードは投入の時点でしか分かりません。ここで載せ損なうと、
	// 出来上がった台本から話者の組み合わせを推し量るしかなくなります。
	if saved.Mode != "tech_solo" {
		t.Errorf("Mode = %q, want tech_solo", saved.Mode)
	}
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

// TestAPIAudioRefusesWhenThereIsNoAudio は、音声が無いジョブにリンクを出さないことを
// 検証します。
//
// 署名は対象の存在を確かめません。作っていない音声の URL も署名できてしまうため、
// 先に確かめないと「開くと 404 になるリンク」を配ることになります。
// Slack 通知で同じことを一度やっています。
func TestAPIAudioRefusesWhenThereIsNoAudio(t *testing.T) {
	t.Parallel()

	signer := &stubSigner{}
	h := apiHandler(t, audioRepo{hasAudio: false})
	h.signer = signer

	rec := callAudio(t, h)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if signer.calls != 0 {
		t.Error("音声が無いのに署名しています")
	}
}

// TestAPIAudioReturnsBothLocations は、期限の無い保存先と再生できるリンクの
// 両方を返すことを検証します。用途が違うため、片方では足りません。
func TestAPIAudioReturnsBothLocations(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, audioRepo{hasAudio: true})
	h.signer = &stubSigner{}

	rec := callAudio(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got apiAudio
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答のデコードに失敗しました: %v", err)
	}
	if !strings.HasPrefix(got.AudioURI, "gs://") {
		t.Errorf("audio_uri = %q, want gs:// で始まること", got.AudioURI)
	}
	if !strings.Contains(got.SignedURL, "X-Goog-Signature") {
		t.Errorf("signed_url に署名がありません: %q", got.SignedURL)
	}
	// 期限を伝えます。いつまで使えるかが分からないと、呼び出し側は
	// 切れたリンクを配ったことに気付けません。
	if got.ExpiresInSeconds <= 0 {
		t.Errorf("expires_in_seconds = %d", got.ExpiresInSeconds)
	}
}

// callAudio は GET /api/jobs/{jobID}/audio を呼びます。
func callAudio(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("GET", "/api/jobs/"+jobID+"/audio", nil)
	// 統合後は Accept が表現を決めます。MCP サーバーの baseClient は常に付けています。
	req.Header.Set("Accept", "application/json")
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Audio(rec, req)
	return rec
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

// TestAPIEnqueueCreatesJobFromSuppliedScript は、持ち込んだ台本から新しいジョブを
// 作れることを検証します。
//
// 既存ジョブが要りません。保存先はジョブ ID から決まるだけで、SaveScript は
// ジョブの存在を確かめないため、ID を発行して保存すればそれが新規作成になります。
// これが無いと、自分で書いた台本を喋らせるのに、捨てる前提の生成を 1 回
// 走らせてから上書きする迂回が要りました。
func TestAPIEnqueueCreatesJobFromSuppliedScript(t *testing.T) {
	t.Parallel()

	var order []string
	repo := &savingRepo{}
	h := apiHandler(t, repo)
	h.status = jobstatus.NewRecorder[domain.JobStatus](recordingStore{order: &order})
	h.queue = recordingQueue{order: &order}

	rec := postJSON(t, h.APIEnqueue, "/api/jobs", `{
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
func TestAPIEnqueueRejectsBadSuppliedScript(t *testing.T) {
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

			rec := postJSON(t, h.APIEnqueue, "/api/jobs", tt.body)
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
func TestAPIEnqueueResolvesRecipeFromMusicJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := apiHandler(t, nil)
	h.queue = queue
	h.musicBucket = "ap-music"

	req := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(
		`{"command":"generate","music_job_id":"music-20260814-031712-5c812debb05f","mode":"music_promo"}`))
	rec := httptest.NewRecorder()
	h.APIEnqueue(rec, req)

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
func TestAPIEnqueueRejectsBadMusicJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := apiHandler(t, nil)
	h.queue = queue
	h.musicBucket = "ap-music"

	req := httptest.NewRequest("POST", "/api/jobs", strings.NewReader(
		`{"command":"generate","music_job_id":"not-a-job-id","mode":"music_promo"}`))
	rec := httptest.NewRecorder()
	h.APIEnqueue(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if queue.calls != 0 {
		t.Errorf("弾いたのに投入しています: %d 回", queue.calls)
	}
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
