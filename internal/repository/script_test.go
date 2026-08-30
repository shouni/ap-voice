package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-remote-io/remoteio/memio"

	"github.com/shouni/ap-voice/internal/domain"
)

// fakeStore は GCS の代わりに、パス→中身のマップを持ちます。
// **読み出しの回数を数えます。** 一覧の往復がジョブ数に比例していないかを見るためです。
// fakeStore は memio を包んだストレージのフェイクです。
//
// 一覧の畳み込みや不在の返し方といったストレージの振る舞いは memio が受け持ちます
// （本物のハンドラと同じ適合性スイートを通っています）。ここに残しているのは
// 「何回開いたか」「何回書いたか」という呼び出しの回数だけで、これは
// 「題名の読み込みが 1 ページぶんに収まっているか」を見るために要ります。
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

// Sub をライブラリの Sub へ委譲します。埋め込みから昇格した Sub をそのまま使うと、
// スコープの土台が埋め込まれた Store になり、上の回数記録が素通しされます。
func (f *fakeStore) Sub(prefix string) remoteio.Store { return remoteio.Sub(f, prefix) }

// put は前提となるオブジェクトを置きます。
func (f *fakeStore) put(t *testing.T, uri, body string) {
	t.Helper()
	if err := f.h.Seed(uri, []byte(body)); err != nil {
		t.Fatalf("seed(%s) error = %v", uri, err)
	}
}

// drop は対象を取り除きます。
func (f *fakeStore) drop(t *testing.T, uri string) {
	t.Helper()
	if err := f.Delete(context.Background(), uri); err != nil {
		t.Fatalf("delete(%s) error = %v", uri, err)
	}
}

// uris は保存されているオブジェクトの URI を辞書順で返します。
func (f *fakeStore) uris() []string { return f.h.URIs() }

// newStore は、n 件のジョブ（台本 + 音声）を持つ倉庫を組み立てます。
// ジョブ ID は go-utils/jobid の形式に従います（voice-{日付}-{時刻}-{hex12}）。
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

	// Firestore クライアントは渡しません。ここで検証するのは成果物の読み書きで、
	// ジョブ状態には触れないためです。削除は状態の消し込みに失敗しても成功として
	// 返すので（孤児は警告ログに残ります）、この構成でも Delete の検証は通ります。
	repo, err := NewRepository(store, "test", nil)
	if err != nil {
		t.Fatalf("NewRepository() error = %v", err)
	}
	return repo
}

// fakeStatus は Firestore の代わりに、返すべき状態をそのまま持ちます。
//
// 一覧が**成果物を一切読まない**ことを見るために要ります。実物の Store を使うと
// エミュレータが要り、この検査のためだけに外部プロセスへ依存することになります。
type fakeStatus struct {
	statuses []domain.JobStatus
}

func (f *fakeStatus) Get(context.Context, string) (domain.JobStatus, error) {
	return domain.JobStatus{}, jobfirestore.ErrNotFound
}

func (f *fakeStatus) Save(context.Context, string, domain.JobStatus) error { return nil }

func (f *fakeStatus) Delete(context.Context, string) error { return nil }

func (f *fakeStatus) List(_ context.Context, page, perPage int, _ ...jobfirestore.ListOption) ([]domain.JobStatus, jobfirestore.PageMeta, error) {
	// 実物と同じく、返すのはページ分だけです。
	total := len(f.statuses)
	from := (page - 1) * perPage
	if from > total {
		from = total
	}
	to := min(from+perPage, total)
	return f.statuses[from:to], jobfirestore.PageMeta{Page: page, PerPage: perPage, Total: total}, nil
}

// withStatuses は、Firestore が返す状態を差し替えた Repository を返します。
func withStatuses(t *testing.T, store *fakeStore, statuses ...domain.JobStatus) *Repository {
	t.Helper()

	repo := newRepo(t, store)
	repo.status = &fakeStatus{statuses: statuses}
	return repo
}

// TestListDoesNotReadArtifacts は、一覧が成果物を 1 つも読まないことを検証します。
//
// **以前はバケット配下を全走査し、ページ分の台本を開いて題名を得ていました。**
// ジョブが増えるほど走査が伸び、増え方に上限がありませんでした。いま必要な値は
// すべて状態に入っているので、倉庫へは一度も触りません。ここで回数を見ておかないと、
// 題名や音声の有無を「念のため」成果物から取り直す実装がいつでも戻ってきます。
func TestListDoesNotReadArtifacts(t *testing.T) {
	t.Parallel()

	// 倉庫は**空**です。それでも一覧は返ります。
	store := newFakeStore()
	repo := withStatuses(t, store,
		domain.JobStatus{Status: jobfirestore.Status{JobID: "voice-20260830-090000-aaaaaaaaaaaa", Title: "新しい方"}},
		domain.JobStatus{Status: jobfirestore.Status{JobID: "voice-20260829-090000-bbbbbbbbbbbb", Title: "古い方"}},
	)

	jobs, meta, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("件数 = %d, want 2", len(jobs))
	}
	if meta.Total != 2 {
		t.Errorf("Total = %d, want 2", meta.Total)
	}
	if store.opened != 0 || store.exists != 0 {
		t.Errorf("倉庫へ触れています: opened=%d exists=%d", store.opened, store.exists)
	}
	if jobs[0].Title != "新しい方" {
		t.Errorf("題名 = %q, 状態から取れていません", jobs[0].Title)
	}
	// 作成時刻はジョブ ID から復元します。
	if jobs[0].CreatedAt.IsZero() {
		t.Error("作成時刻が空です")
	}
}

// TestListFallsBackToJobID は、題名の無いジョブが一覧から消えないことを検証します。
//
// **消えると削除する手段まで失われます。** 題名が確定する前に失敗したジョブこそ
// 消したいものです。
func TestListFallsBackToJobID(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260830-090000-aaaaaaaaaaaa"
	repo := withStatuses(t, newFakeStore(),
		domain.JobStatus{Status: jobfirestore.Status{JobID: jobID, State: jobfirestore.StateFailed}})

	jobs, _, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("件数 = %d, want 1", len(jobs))
	}
	if jobs[0].Title != jobID {
		t.Errorf("題名 = %q, want ジョブID", jobs[0].Title)
	}
}

// TestListMarksAudioFromTheRecordedURI は、音声の有無を成果物ではなく記録から
// 判定することを検証します。generate は台本で終わるので在り処が入りません。
func TestListMarksAudioFromTheRecordedURI(t *testing.T) {
	t.Parallel()

	repo := withStatuses(t, newFakeStore(),
		domain.JobStatus{
			Status:   jobfirestore.Status{JobID: "voice-20260830-090000-aaaaaaaaaaaa"},
			AudioURI: "gs://test/voice/voice-20260830-090000-aaaaaaaaaaaa/audio.wav",
		},
		domain.JobStatus{Status: jobfirestore.Status{JobID: "voice-20260829-090000-bbbbbbbbbbbb"}},
	)

	jobs, _, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !jobs[0].HasAudio {
		t.Error("音声の在り処が記録されているのに false です")
	}
	if jobs[1].HasAudio {
		t.Error("台本だけのジョブを true にしています")
	}
}

// TestHasAudioChecksTheObjectItself は、音声の有無を倉庫へ直接問い合わせることを
// 検証します。
//
// **一覧の判定とは別物です。** 詳細画面は記録より実物を信じます（記録の取りこぼしで
// 再生欄が消えるより、余分に 1 回問い合わせるほうが安いためです）。
func TestHasAudioChecksTheObjectItself(t *testing.T) {
	t.Parallel()

	store, ids := newStore(t, 2)
	repo := newRepo(t, store)

	has, err := repo.HasAudio(context.Background(), ids[0])
	if err != nil {
		t.Fatalf("HasAudio() error = %v", err)
	}
	if !has {
		t.Error("音声があるのに false を返しました")
	}

	// 台本だけのジョブは false であること。
	store.drop(t, "gs://test/voice/"+ids[1]+"/audio.wav")
	if has, err := repo.HasAudio(context.Background(), ids[1]); err != nil || has {
		t.Errorf("HasAudio() = %v, %v; want false, nil", has, err)
	}
}

// TestHasAudioRejectsBadJobID は、不正なジョブ ID をパスへ埋める前に弾くことを検証します。
func TestHasAudioRejectsBadJobID(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 1)
	repo := newRepo(t, store)

	if _, err := repo.HasAudio(context.Background(), "../../etc/passwd"); err == nil {
		t.Fatal("不正なジョブIDが素通りしました")
	}
	if store.exists != 0 {
		t.Error("検証前に倉庫へ問い合わせています")
	}
}

// TestDeleteRemovesTheWholeJobPrefix は、削除がジョブ配下をまとめて消し、
// 他のジョブに触れないことを検証します。
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
			t.Errorf("消し残しがあります: %s", uri)
		}
	}
	if len(remaining) != 2 {
		t.Errorf("他のジョブまで消えています: %v", remaining)
	}
}

// TestSaveWritesBackToTheStoredScript は、編集した台本が**読み出し先と同じ場所**へ
// 保存されることを検証します。
//
// 保存先と読み出し先がずれると、編集したのに古い台本で合成される、という
// 気付きにくい壊れ方をします。
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
		t.Errorf("読み戻した台本が違います: %+v", got)
	}
}

// TestSaveRejectsBadJobID は、不正なジョブ ID をパスへ埋める前に弾くことを検証します。
func TestSaveRejectsBadJobID(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 1)
	repo := newRepo(t, store)

	if err := repo.SaveScript(context.Background(), "../../evil", domain.Script{Title: "x"}); err == nil {
		t.Fatal("不正なジョブIDが素通りしました")
	}
	if store.written != 0 {
		t.Error("検証前に書き込んでいます")
	}
}
