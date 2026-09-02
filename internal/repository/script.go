// Package repository は、GCS 上の成果物の読み出しと、Firestore 上のジョブ状態の
// 読み書きを担います。履歴の一覧は状態だけで組み立て、成果物には触れません。
package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
)

// ErrJobNotFound は、成果物としても記録としても存在しないジョブを指したことを表します。
//
// 「成果物が無い」ではありません。台本を書く前に失敗したジョブは成果物を 1 つも
// 持ちませんが、記録は残っており、消せなければ履歴に残り続けます。
var ErrJobNotFound = errors.New("repository: job not found")

// Repository は、ジョブの成果物を読み出します。
//
// 書き込みは PublishStep が担い、ここは読むだけです。成果物がジョブ ID ごとの
// プレフィックスにまとまっているため、一覧も削除も中身を知らずに行えます。
type Repository struct {
	// store は gs://{bucket} にスコープ済みのストアです。以降のパスはすべて
	// そこからの相対名なので、呼び出しごとにバケットを連れ回す必要がありません。
	store  remoteio.Store
	layout domain.StorageLayout
	// status はジョブの進行状況です。成果物とは別の場所（Firestore）にあるので、
	// プレフィックスの一括削除では片付きません。Delete が明示的に消します。
	//
	// 具象ではなくインターフェースで持つのは、一覧の検証に Firestore を要求しない
	// ためです（実物が要る検査はエミュレータの担当）。
	status statusStore
}

// statusStore は Repository がジョブ状態に対して行う操作です。
// *jobfirestore.Store[domain.JobStatus] がそのまま満たします。
type statusStore interface {
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
	Save(ctx context.Context, jobID string, status domain.JobStatus) error
	Delete(ctx context.Context, jobID string) error
	List(ctx context.Context, page, perPage int, opts ...jobfirestore.ListOption) ([]domain.JobStatus, jobfirestore.PageMeta, error)
}

// Get は、ジョブの状態を読みます。jobfirestore.StatusStore を満たします。
func (r *Repository) Get(ctx context.Context, jobID string) (domain.JobStatus, error) {
	return r.status.Get(ctx, jobID)
}

// Save は、ジョブの状態を書きます。jobfirestore.StatusStore を満たします。
//
// Repository が StatusStore を満たすことで、Recorder をここから組み立てられます。
// 保存先の組み立て（バケットとプレフィックス）を 2 か所に書かずに済みます。
func (r *Repository) Save(ctx context.Context, jobID string, status domain.JobStatus) error {
	return r.status.Save(ctx, jobID, status)
}

// statusCollection はジョブ状態を置く Firestore のコレクションです。
//
// 成果物のバケットと同じ語彙にしてあります。コレクションはサービスごとに 1 本で、
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
// 書き戻す先は Load が読む場所と同じです（どちらも StorageLayout が決めます）。
// ずれると、編集したのに古い台本で合成される、という気付きにくい壊れ方をします。
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
	// 一覧の題名は状態から取るので、ここで追随させないと古い題名が残ります。
	// 台本を読んで題名を出していた頃は保存した瞬間に反映されていました。
	// 失敗しても保存そのものは成功として返します（題名の鮮度より台本が大事です）。
	r.refreshTitle(ctx, jobID, script.Title)

	slog.InfoContext(ctx, "編集された台本を保存しました", "job_id", jobID, "lines", len(script.Lines))
	return nil
}

// refreshTitle は、保存済みの状態にある題名だけを書き換えます。
func (r *Repository) refreshTitle(ctx context.Context, jobID, title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}

	status, err := r.status.Get(ctx, jobID)
	if err != nil {
		// 未記録なら追随させるものがありません。記録前の台本編集で起こります。
		if !errors.Is(err, jobfirestore.ErrNotFound) {
			slog.WarnContext(ctx, "題名の追随のためのジョブ状態を読めませんでした",
				"job_id", jobID, "error", err)
		}
		return
	}
	if status.Title == title {
		return
	}

	status.Title = title
	if err := r.status.Save(ctx, jobID, status); err != nil {
		slog.WarnContext(ctx, "ジョブ状態の題名を更新できませんでした",
			"job_id", jobID, "error", err)
	}
}

// Load は、保存済みの台本を読み出します。domain.ScriptStore を満たします。
//
// まだ書かれていない場合は ErrScriptNotFound を返します。台本が無いのは異常では
// なく、generate が終わる前と generate に失敗したジョブの通常の姿です。呼び出し側が
// 「まだ無い」と「読めなかった」を区別できないと、どちらも同じ失敗として扱うしか
// なくなり、詳細画面が状態を見せられません。
func (r *Repository) Load(ctx context.Context, jobID string) (domain.Script, error) {
	// ジョブ ID はフォームからも来るため、パスへ埋める前に必ず検証します。
	if err := jobid.Validate(jobID); err != nil {
		return domain.Script{}, fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	path := r.layout.ScriptPath(jobID)
	stream, err := r.store.Open(ctx, path)
	if err != nil {
		return domain.Script{}, r.openFailure(ctx, jobID, path, err)
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

// openFailure は、台本を開けなかった理由を「まだ無い」と「読めなかった」に分けます。
//
// 不在の綴りは保存先ごとに違う（GCS と local で別のエラー値）ので、返ってきた
// エラーの中身は見ません。在るかどうかをもう一度だけ問い合わせて決めます。
// 余分な 1 往復は失敗した経路にしか乗らず、成功する読み出しは今までどおりです。
func (r *Repository) openFailure(ctx context.Context, jobID, path string, err error) error {
	if exists, existsErr := r.store.Exists(ctx, path); existsErr == nil && !exists {
		return fmt.Errorf("台本がまだありません (%s): %w", jobID, domain.ErrScriptNotFound)
	}
	return fmt.Errorf("台本の読み込みに失敗しました: %w", err)
}

// HasAudio は、そのジョブの音声が既にあるかを返します。
//
// 一覧を引いて探しません。以前は List の結果から該当ジョブを線形探索していましたが、
// これには 2 つの問題がありました。1 つは、真偽値 1 つを知るために全ジョブの台本を
// 読んでいたこと。もう 1 つは、上限（50 件）から外れた古いジョブが必ず false になる
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
	// Title は台本の題名です。読めなければジョブ ID を入れます。
	// 台本が壊れていても音声だけは残っていることがあり、一覧から消えると
	// 消す手段まで失われるためです。
	Title string
	// CreatedAt はジョブ ID から復元した作成時刻です。
	CreatedAt time.Time
	// HasAudio は音声が既に作られているかです。台本だけの段階と区別します。
	HasAudio bool
	// State は進行状態です。成果物の有無だけでは、実行中なのか失敗したのかを
	// 見分けられません。記録には最初から入っていた値で、一覧が読まずに捨てていました。
	State jobfirestore.State
	// Error は失敗理由です。State が failed のときだけ入ります。
	Error string
}

// List は、指定ページのジョブを新しい順に返します。絞り込みは呼び出し側が決めます
// （jobfirestore.WithState など）。ここで絞り込みの語彙を作り直すと、ライブラリが
// 既に持っているものが 2 つになります。
//
// 成果物を一切読みません。以前はバケット配下を全走査してジョブ ID と音声の
// 有無を集め、ページ分の台本を並行に読んで題名を得ていました。ジョブが増えるほど
// 走査が伸び、件数に上限がありません。いまは 1 ページ分をクエリで取り、必要な
// 3 つの値（題名・音声の有無・作成時刻）をすべて状態から導きます。
func (r *Repository) List(ctx context.Context, page, perPage int, opts ...jobfirestore.ListOption) ([]Job, jobfirestore.PageMeta, error) {
	statuses, meta, err := r.status.List(ctx, page, perPage, opts...)
	if err != nil {
		return nil, jobfirestore.PageMeta{}, fmt.Errorf("履歴の一覧取得に失敗しました: %w", err)
	}

	jobs := make([]Job, 0, len(statuses))
	for _, status := range statuses {
		job := Job{
			ID:    status.JobID,
			Title: status.Title,
			// 音声を作ったのは synthesize だけです。generate は台本で終わるので
			// 在り処が入りません（成果物を数えずに区別できます）。
			HasAudio: status.AudioURI != "",
			State:    status.State,
			Error:    status.Error,
		}
		// 題名が無くても一覧から落としません。生成が題名の確定前に失敗した
		// ジョブこそ消したいもので、一覧から消えると消す手段まで失われます。
		if job.Title == "" {
			job.Title = status.JobID
		}
		job.CreatedAt, _ = jobid.CreatedAt(status.JobID)
		jobs = append(jobs, job)
	}
	return jobs, meta, nil
}

// Delete は、1 つのジョブの成果物をまとめて消します。
//
// プレフィックス配下を一覧して消します。何が置かれたかを呼び出し側が知らなくても
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

	// 成果物が 1 つも無いジョブは、台本を書く前に失敗したジョブです。以前はここで
	// 「見つかりません」と返していましたが、消したいのはまさにそのジョブでした。
	// 記録だけが履歴に残り、詳細画面は台本が無くて開けず、削除もこの分岐で
	// 断られるため、どこからも消せないまま並び続けていました。
	if len(entries) == 0 {
		return r.deleteRecord(ctx, jobID)
	}

	for _, entry := range entries {
		if err := jobStore.Delete(ctx, entry.Name); err != nil {
			return fmt.Errorf("削除に失敗しました (%s): %w", entry.URI, err)
		}
	}
	// 状態は成果物と別の場所にあるので、ここで消さないと孤児が残ります。
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

// deleteRecord は、成果物が残っていないジョブの記録だけを消します。
//
// 記録も無ければ ErrJobNotFound です。「知らないジョブを指した」のと
// 「作りかけで失敗したジョブを片付けた」のとでは、呼び出し側の返し方が
// 変わります（前者は 404、後者は成功）。
//
// 成果物がある経路と違い、記録の削除に失敗したらエラーを返します。あちらは
// 依頼された成果物が既に消えているので警告で足りますが、こちらは記録が
// 消せなければ何も起きていないためです。
func (r *Repository) deleteRecord(ctx context.Context, jobID string) error {
	if _, err := r.status.Get(ctx, jobID); err != nil {
		if errors.Is(err, jobfirestore.ErrNotFound) {
			return fmt.Errorf("ジョブが見つかりません (%s): %w", jobID, ErrJobNotFound)
		}
		return fmt.Errorf("ジョブ状態の確認に失敗しました (%s): %w", jobID, err)
	}
	if err := r.status.Delete(ctx, jobID); err != nil {
		return fmt.Errorf("ジョブ状態の削除に失敗しました (%s): %w", jobID, err)
	}

	slog.InfoContext(ctx, "成果物の無いジョブの記録を削除しました", "job_id", jobID)
	return nil
}
