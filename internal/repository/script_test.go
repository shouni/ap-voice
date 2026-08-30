package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

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

// TestListReadsOnlyTheScriptsItShows は、題名の読み出しが**表示件数**で
// 止まることを検証します。
//
// 以前は全件の題名を読んでから上限で切っていたため、50 件を出すのに
// ジョブ数ぶんの往復が発生していました。ジョブが増えるほど遅くなり、
// 増え方に上限がありません。
func TestListReadsOnlyTheScriptsItShows(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t, 30)
	repo := newRepo(t, store)

	jobs, _, err := repo.List(context.Background(), 1, 5)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 5 {
		t.Fatalf("件数 = %d, want 5", len(jobs))
	}
	if got := int(store.opened); got != 5 {
		t.Errorf("台本を %d 件読みました。表示は 5 件なので 5 回で足ります", got)
	}
	// 新しい順であること。
	if jobs[0].ID <= jobs[1].ID {
		t.Errorf("降順に並んでいません: %s, %s", jobs[0].ID, jobs[1].ID)
	}
	// 題名が埋まっていること。
	for _, job := range jobs {
		if !strings.HasPrefix(job.Title, "台本 ") {
			t.Errorf("題名が埋まっていません: %+v", job)
		}
	}
}

// TestListKeepsJobsWithUnreadableScripts は、台本が壊れていても一覧から
// 消えないことを検証します。**消えると削除する手段まで失われます。**
func TestListKeepsJobsWithUnreadableScripts(t *testing.T) {
	t.Parallel()

	store, ids := newStore(t, 2)
	store.put(t, "gs://test/voice/"+ids[0]+"/audio.json", "これはJSONではありません")
	repo := newRepo(t, store)

	jobs, _, err := repo.List(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("件数 = %d, want 2", len(jobs))
	}
	for _, job := range jobs {
		if job.ID == ids[0] && job.Title != ids[0] {
			t.Errorf("読めない台本の題名 = %q, want ジョブID", job.Title)
		}
	}
}

// TestHasAudioDoesNotDependOnTheListLimit は、一覧の上限から外れた古いジョブでも
// 音声の有無を正しく返すことを検証します。
//
// **以前は List の結果を線形探索していました。** 上限 50 件に入らないジョブは
// 必ず false になり、音声があるのに詳細画面から再生欄が消えていました。
func TestHasAudioDoesNotDependOnTheListLimit(t *testing.T) {
	t.Parallel()

	store, ids := newStore(t, 60)
	repo := newRepo(t, store)

	// ids[0] は最も古いので、新しい順 50 件には入りません。
	oldest := ids[0]
	jobs, _, err := repo.List(context.Background(), 1, 50)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, job := range jobs {
		if job.ID == oldest {
			t.Fatalf("前提が崩れています: %s が上限内に入っています", oldest)
		}
	}

	has, err := repo.HasAudio(context.Background(), oldest)
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
