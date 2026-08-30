// Package repository は、GCS 上の成果物の読み出しを担います。
package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-job-kit/paging"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
)

// Repository は、ジョブの成果物を読み出します。
//
// 書き込みは PublishStep が担い、ここは読むだけです。**成果物がジョブ ID ごとの
// プレフィックスにまとまっている**ため、一覧も削除も中身を知らずに行えます。
type Repository struct {
	// store は gs://{bucket} にスコープ済みのストアです。以降のパスはすべて
	// そこからの相対名なので、呼び出しごとにバケットを連れ回す必要がありません。
	store  remoteio.Store
	layout domain.StorageLayout
	// status はジョブの進行状況です。**成果物とは別の場所（Firestore）にあるので、
	// プレフィックスの一括削除では片付きません。** Delete が明示的に消します。
	status *jobfirestore.Store[domain.JobStatus]
}

// Get は、ジョブの状態を読みます。jobfirestore.StatusStore を満たします。
func (r *Repository) Get(ctx context.Context, jobID string) (domain.JobStatus, error) {
	return r.status.Get(ctx, jobID)
}

// Save は、ジョブの状態を書きます。jobfirestore.StatusStore を満たします。
//
// **Repository が StatusStore を満たすことで、Recorder をここから組み立てられます。**
// 保存先の組み立て（バケットとプレフィックス）を 2 か所に書かずに済みます。
func (r *Repository) Save(ctx context.Context, jobID string, status domain.JobStatus) error {
	return r.status.Save(ctx, jobID, status)
}

// titleFetchParallelism は題名を読むときの同時実行数です。
// 1 件ずつ読むと件数分の往復になるため並べますが、GCS を叩きすぎない程度に抑えます。
const titleFetchParallelism = 8

// statusCollection はジョブ状態を置く Firestore のコレクションです。
//
// **成果物のバケットと同じ語彙にしてあります。** コレクションはサービスごとに 1 本で、
// 共有 1 本に判別フィールドを持たせる形は採りません（絞り忘れが落ちずに他サービスの
// ジョブを履歴へ混ぜるためです）。設定にせず定数なのは、これがサービスの身元であって
// デプロイごとに変わる値ではないからです。
const statusCollection = "ap-voice"

// NewRepository は Repository を構築します。
//
// firestore は nil でも構築できます。その場合ジョブ状態の読み書きだけが失敗し、
// 成果物の読み出しは動きます（テストが状態を要求しないため）。
func NewRepository(store remoteio.Store, bucket string, firestore *firestore.Client) (*Repository, error) {
	if store == nil {
		return nil, fmt.Errorf("ストレージが指定されていません")
	}
	if bucket == "" {
		return nil, fmt.Errorf("バケットが指定されていません")
	}
	layout := domain.NewStorageLayout()
	scoped := store.Sub(remoteio.BuildURI(remoteio.SchemeGCS, bucket, ""))
	return &Repository{
		store: scoped, layout: layout,
		status: jobfirestore.NewStore[domain.JobStatus](firestore, statusCollection),
	}, nil
}

// SaveScript は、編集された台本を保存済みのものへ書き戻します。
//
// **編集内容をタスクのペイロードに載せません。** Cloud Tasks は 1MB が上限で、
// 長い台本はそこに当たりえます。先に保存してから JobID だけを渡せば、
// Worker は既存の「保存済み台本を読む」経路をそのまま使えます。
func (r *Repository) SaveScript(ctx context.Context, jobID string, script domain.Script) error {
	if err := jobid.Validate(jobID); err != nil {
		return fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	body, err := json.MarshalIndent(script, "", "  ")
	if err != nil {
		return fmt.Errorf("台本のJSONエンコードに失敗しました: %w", err)
	}

	uri := r.layout.ScriptPath(jobID)
	if err := r.store.Write(ctx, uri, bytes.NewReader(body),
		remoteio.WithContentType("application/json; charset=utf-8")); err != nil {
		return fmt.Errorf("台本の保存に失敗しました (%s): %w", uri, err)
	}
	slog.InfoContext(ctx, "編集された台本を保存しました", "job_id", jobID, "lines", len(script.Lines))
	return nil
}

// Load は、保存済みの台本を読み出します。domain.ScriptStore を満たします。
func (r *Repository) Load(ctx context.Context, jobID string) (domain.Script, error) {
	// ジョブ ID はフォームからも来るため、パスへ埋める前に必ず検証します。
	if err := jobid.Validate(jobID); err != nil {
		return domain.Script{}, fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	stream, err := r.store.Open(ctx, r.layout.ScriptPath(jobID))
	if err != nil {
		return domain.Script{}, fmt.Errorf("台本の読み込みに失敗しました: %w", err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			slog.WarnContext(ctx, "台本ストリームのクローズに失敗しました", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return domain.Script{}, fmt.Errorf("台本の読み込みに失敗しました: %w", err)
	}

	var script domain.Script
	if err := json.Unmarshal(body, &script); err != nil {
		return domain.Script{}, fmt.Errorf("台本のJSONデコードに失敗しました: %w", err)
	}
	return script, nil
}

// HasAudio は、そのジョブの音声が既にあるかを返します。
//
// **一覧を引いて探しません。** 以前は List の結果から該当ジョブを線形探索していましたが、
// これには 2 つの問題がありました。1 つは、真偽値 1 つを知るために全ジョブの台本を
// 読んでいたこと。もう 1 つは、**上限（50 件）から外れた古いジョブが必ず false になる**
// ことで、音声があるのに詳細画面から再生欄が消えていました。
func (r *Repository) HasAudio(ctx context.Context, jobID string) (bool, error) {
	if err := jobid.Validate(jobID); err != nil {
		return false, fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}
	return r.store.Exists(ctx, r.layout.AudioPath(jobID))
}

// Job は履歴一覧の 1 件です。
type Job struct {
	ID string
	// Title は台本の題名です。**読めなければジョブ ID を入れます。**
	// 台本が壊れていても音声だけは残っていることがあり、一覧から消えると
	// 消す手段まで失われるためです。
	Title string
	// CreatedAt はジョブ ID から復元した作成時刻です。
	CreatedAt time.Time
	// HasAudio は音声が既に作られているかです。台本だけの段階と区別します。
	HasAudio bool
}

// List は、指定ページのジョブを新しい順に返します。
//
// **並べ替えと切り出しは go-job-kit の paging に任せます。** 御三家と同じ実装なので、
// ページ番号の解釈やメタデータの形が揃います。題名の読み込みは 1 ページぶんだけを
// 並行に行い、失敗した ID はジョブ ID のまま一覧へ残します（台本が壊れていても
// 音声は残っていることがあり、一覧から消えると消す手段まで失われます）。
func (r *Repository) List(ctx context.Context, page, perPage int) ([]Job, paging.PageMeta, error) {
	hasAudio := make(map[string]bool)
	var jobIDs []string
	// Entry.Name は列挙したプレフィックスからの相対名なので、"{jobID}/{ファイル名}" が
	// そのまま得られます。以前は完全な URI から接頭辞を削って同じものを復元していました。
	for entry, err := range r.store.List(ctx, r.layout.VoicePrefix()) {
		if err != nil {
			return nil, paging.PageMeta{}, fmt.Errorf("履歴の一覧取得に失敗しました: %w", err)
		}
		jobID, object, ok := splitJobPath(entry.Name)
		if !ok {
			continue
		}
		if _, seen := hasAudio[jobID]; !seen {
			hasAudio[jobID] = false
			jobIDs = append(jobIDs, jobID)
		}
		if strings.HasSuffix(object, ".wav") {
			hasAudio[jobID] = true
		}
	}

	load := func(ctx context.Context, jobID string) (Job, error) {
		job := Job{ID: jobID, Title: jobID, HasAudio: hasAudio[jobID]}
		job.CreatedAt, _ = jobid.CreatedAt(jobID)
		script, err := r.Load(ctx, jobID)
		if err != nil {
			// **一覧からは落としません。** 読めない台本のジョブこそ消したいものです。
			slog.DebugContext(ctx, "台本を読めないジョブIDのまま一覧に載せます", "job_id", jobID, "error", err)
			return job, nil
		}
		if title := strings.TrimSpace(script.Title); title != "" {
			job.Title = title
		}
		return job, nil
	}

	// **ジョブ ID を辞書順に並べません。** 接頭辞付きの形式で、接頭辞の違いが
	// 時刻より先に効いてしまうためです（jobid.SortKey がそこを吸収します）。
	return paging.LoadPage(ctx, jobIDs, page, perPage, jobid.SortKey, load,
		paging.WithConcurrency(titleFetchParallelism))
}

// Delete は、1 つのジョブの成果物をまとめて消します。
//
// **プレフィックス配下を一覧して消します。** 何が置かれたかを呼び出し側が知らなくても
// 消せるのが、ジョブ ID ごとにまとめている理由です。
func (r *Repository) Delete(ctx context.Context, jobID string) error {
	if err := jobid.Validate(jobID); err != nil {
		return fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	// ジョブ配下へさらにスコープを絞ると、一覧で得た名前をそのまま削除へ渡せます。
	jobStore := r.store.Sub(r.layout.VoiceJobPrefix(jobID))

	var entries []remoteio.Entry
	for entry, err := range jobStore.List(ctx, "") {
		if err != nil {
			return fmt.Errorf("削除対象の一覧取得に失敗しました (%s): %w", jobID, err)
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return fmt.Errorf("ジョブが見つかりません (%s)", jobID)
	}

	for _, entry := range entries {
		if err := jobStore.Delete(ctx, entry.Name); err != nil {
			return fmt.Errorf("削除に失敗しました (%s): %w", entry.URI, err)
		}
	}
	// **状態は成果物と別の場所にあるので、ここで消さないと孤児が残ります。**
	// 以前は状態ファイルがジョブディレクトリ配下にあり、上の一括削除で一緒に
	// 片付いていました。Firestore へ移したことで、その連動が失われています。
	//
	// 失敗しても削除そのものは成功として返します。成果物は既に消えており、
	// 呼び出し側の依頼は果たされているためです。残るのは観測用の記録だけなので、
	// ここでエラーを返すと「消えたのに失敗した」と見える分だけ害が大きくなります。
	if err := r.status.Delete(ctx, jobID); err != nil {
		slog.WarnContext(ctx, "ジョブ状態の削除に失敗しました。孤児が残ります",
			"job_id", jobID, "error", err)
	}

	slog.InfoContext(ctx, "ジョブの成果物を削除しました", "job_id", jobID, "objects", len(entries))
	return nil
}

// splitJobPath は、プレフィックスからの相対名をジョブ ID とファイル名に分けます。
func splitJobPath(name string) (jobID, object string, ok bool) {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
