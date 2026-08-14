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

	"github.com/shouni/ap-voice/internal/domain"
)

// fakeStore は GCS の代わりに、パス→中身のマップを持ちます。
// **読み出しの回数を数えます。** 一覧の往復がジョブ数に比例していないかを見るためです。
type fakeStore struct {
	objects map[string]string
	opened  int32
	exists  int32
	written int32
}

func (f *fakeStore) Open(_ context.Context, path string) (io.ReadCloser, error) {
	atomic.AddInt32(&f.opened, 1)
	body, ok := f.objects[path]
	if !ok {
		return nil, fmt.Errorf("見つかりません: %s", path)
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func (f *fakeStore) List(_ context.Context, prefix string, fn func(string) error, _ ...remoteio.ListOption) error {
	for path := range f.objects {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		if err := fn(path); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeStore) Exists(_ context.Context, path string) (bool, error) {
	atomic.AddInt32(&f.exists, 1)
	_, ok := f.objects[path]
	return ok, nil
}

func (f *fakeStore) Write(_ context.Context, path string, r io.Reader, _ ...remoteio.WriteOption) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	atomic.AddInt32(&f.written, 1)
	f.objects[path] = string(body)
	return nil
}

func (f *fakeStore) Delete(_ context.Context, path string) error {
	delete(f.objects, path)
	return nil
}

// newStore は、n 件のジョブ（台本 + 音声）を持つ倉庫を組み立てます。
// ジョブ ID は go-utils/jobid の形式に従います（voice-{日付}-{時刻}-{hex12}）。
func newStore(t *testing.T, n int) (*fakeStore, []string) {
	t.Helper()

	store := &fakeStore{objects: map[string]string{}}
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
		store.objects["gs://test/voice/"+id+"/audio.json"] = string(script)
		store.objects["gs://test/voice/"+id+"/audio.wav"] = "RIFF"
	}
	return store, ids
}

func newRepo(t *testing.T, store *fakeStore) *Repository {
	t.Helper()

	repo, err := NewRepository(store, store, "test")
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

	jobs, err := repo.List(context.Background(), 5)
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
	store.objects["gs://test/voice/"+ids[0]+"/audio.json"] = "これはJSONではありません"
	repo := newRepo(t, store)

	jobs, err := repo.List(context.Background(), 10)
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
	jobs, err := repo.List(context.Background(), 50)
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
	delete(store.objects, "gs://test/voice/"+ids[1]+"/audio.wav")
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
	for path := range store.objects {
		if strings.Contains(path, ids[0]) {
			t.Errorf("消し残しがあります: %s", path)
		}
	}
	if len(store.objects) != 2 {
		t.Errorf("他のジョブまで消えています: %v", store.objects)
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
	if err := repo.Save(context.Background(), id, edited); err != nil {
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

	if err := repo.Save(context.Background(), "../../evil", domain.Script{Title: "x"}); err == nil {
		t.Fatal("不正なジョブIDが素通りしました")
	}
	if store.written != 0 {
		t.Error("検証前に書き込んでいます")
	}
}
