// Package repository は、GCS 上の成果物の読み出しを担います。
package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
)

// Repository は、ジョブの成果物を読み出します。
//
// 書き込みは PublishStep が担い、ここは読むだけです。**成果物がジョブ ID ごとの
// プレフィックスにまとまっている**ため、一覧も削除も中身を知らずに行えます。
type Repository struct {
	reader remoteio.InputReader
	writer remoteio.OutputWriter
	bucket string
	layout domain.StorageLayout
}

// titleFetchParallelism は題名を読むときの同時実行数です。
// 1 件ずつ読むと件数分の往復になるため並べますが、GCS を叩きすぎない程度に抑えます。
const titleFetchParallelism = 8

// NewRepository は Repository を構築します。
func NewRepository(reader remoteio.InputReader, writer remoteio.OutputWriter, bucket string) (*Repository, error) {
	if reader == nil {
		return nil, fmt.Errorf("読み出しクライアントが指定されていません")
	}
	if writer == nil {
		return nil, fmt.Errorf("書き込みクライアントが指定されていません")
	}
	if bucket == "" {
		return nil, fmt.Errorf("バケットが指定されていません")
	}
	return &Repository{reader: reader, writer: writer, bucket: bucket, layout: domain.NewStorageLayout()}, nil
}

// Save は、編集された台本を保存済みのものへ書き戻します。
//
// **編集内容をタスクのペイロードに載せません。** Cloud Tasks は 1MB が上限で、
// 長い台本はそこに当たりえます。先に保存してから JobID だけを渡せば、
// Worker は既存の「保存済み台本を読む」経路をそのまま使えます。
func (r *Repository) Save(ctx context.Context, jobID string, script domain.Script) error {
	if err := jobid.Validate(jobID); err != nil {
		return fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	body, err := json.MarshalIndent(script, "", "  ")
	if err != nil {
		return fmt.Errorf("台本のJSONエンコードに失敗しました: %w", err)
	}

	uri := r.uri(r.layout.ScriptPath(jobID))
	if err := r.writer.Write(ctx, uri, bytes.NewReader(body),
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

	stream, err := r.reader.Open(ctx, r.uri(r.layout.ScriptPath(jobID)))
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
	return r.reader.Exists(ctx, r.uri(r.layout.AudioPath(jobID)))
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

// List は、新しい順にジョブを返します。
//
// 並びは作成時刻の降順ですが、**ジョブ ID を辞書順に並べません。** ID は
// 接頭辞付きの形式で、接頭辞の違いが時刻より先に効いてしまうためです
// （go-utils/jobid の SortKey がそこを吸収します）。
func (r *Repository) List(ctx context.Context, limit int) ([]Job, error) {
	prefix := r.uri(r.layout.VoicePrefix())

	seen := make(map[string]*Job)
	err := r.reader.List(ctx, prefix, func(path string) error {
		jobID, object, ok := r.splitJobPath(path)
		if !ok {
			return nil
		}
		job, exists := seen[jobID]
		if !exists {
			created, _ := jobid.CreatedAt(jobID)
			job = &Job{ID: jobID, Title: jobID, CreatedAt: created}
			seen[jobID] = job
		}
		if strings.HasSuffix(object, ".wav") {
			job.HasAudio = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("履歴の一覧取得に失敗しました: %w", err)
	}

	jobs := make([]Job, 0, len(seen))
	for _, job := range seen {
		jobs = append(jobs, *job)
	}

	// **並べて切ってから題名を読みます。** 逆順にすると、50 件を表示するために
	// 全ジョブ分の台本を読むことになり、往復がジョブ数に比例して増え続けます。
	sort.Slice(jobs, func(i, j int) bool {
		return jobid.SortKey(jobs[i].ID) > jobid.SortKey(jobs[j].ID)
	})
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}

	r.fillTitles(ctx, jobs)
	return jobs, nil
}

// fillTitles は各ジョブの台本を読んで題名を埋めます。
//
// **1 件ずつ順に読むと件数分の往復になる**ため並列に読みます。読めなかったものは
// ジョブ ID のままにして一覧には載せます（台本が壊れていても音声は残っていることがあり、
// 一覧から消えると消す手段まで失われます）。
func (r *Repository) fillTitles(ctx context.Context, jobs []Job) {
	// errgroup.WithContext は使いません。**どの読み出しも失敗を握りつぶす**ため、
	// 1 件の失敗で残りを打ち切る意味がありません。
	var g errgroup.Group
	g.SetLimit(titleFetchParallelism)

	for i := range jobs {
		g.Go(func() error {
			script, err := r.Load(ctx, jobs[i].ID)
			if err != nil {
				slog.DebugContext(ctx, "台本を読めないジョブIDのまま一覧に載せます", "job_id", jobs[i].ID, "error", err)
				return nil
			}
			if title := strings.TrimSpace(script.Title); title != "" {
				jobs[i].Title = title
			}
			return nil
		})
	}
	// 個々の失敗は上で握りつぶしているため、ここでエラーは返りません。
	_ = g.Wait()
}

// Delete は、1 つのジョブの成果物をまとめて消します。
//
// **プレフィックス配下を一覧して消します。** 何が置かれたかを呼び出し側が知らなくても
// 消せるのが、ジョブ ID ごとにまとめている理由です。
func (r *Repository) Delete(ctx context.Context, jobID string) error {
	if err := jobid.Validate(jobID); err != nil {
		return fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	var paths []string
	prefix := r.uri(r.layout.VoiceJobPrefix(jobID))
	if err := r.reader.List(ctx, prefix, func(path string) error {
		paths = append(paths, path)
		return nil
	}); err != nil {
		return fmt.Errorf("削除対象の一覧取得に失敗しました (%s): %w", jobID, err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("ジョブが見つかりません (%s)", jobID)
	}

	for _, path := range paths {
		if err := r.writer.Delete(ctx, path); err != nil {
			return fmt.Errorf("削除に失敗しました (%s): %w", path, err)
		}
	}
	slog.InfoContext(ctx, "ジョブの成果物を削除しました", "job_id", jobID, "objects", len(paths))
	return nil
}

// splitJobPath は、オブジェクトのパスからジョブ ID とファイル名を取り出します。
func (r *Repository) splitJobPath(path string) (jobID, object string, ok bool) {
	rest := strings.TrimPrefix(path, r.uri(r.layout.VoicePrefix()))
	if rest == path {
		return "", "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (r *Repository) uri(path string) string {
	return fmt.Sprintf("gs://%s/%s", r.bucket, path)
}
