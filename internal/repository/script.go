// Package repository は、GCS 上の成果物の読み出しを担います。
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

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
	bucket string
	layout domain.StorageLayout
}

// NewRepository は Repository を構築します。
func NewRepository(reader remoteio.InputReader, bucket string) (*Repository, error) {
	if reader == nil {
		return nil, fmt.Errorf("InputReader が指定されていません")
	}
	if bucket == "" {
		return nil, fmt.Errorf("バケットが指定されていません")
	}
	return &Repository{reader: reader, bucket: bucket, layout: domain.NewStorageLayout()}, nil
}

// Load は、保存済みの台本を読み出します。domain.ScriptStore を満たします。
func (r *Repository) Load(ctx context.Context, jobID string) ([]domain.ScriptLine, error) {
	// ジョブ ID はフォームからも来るため、パスへ埋める前に必ず検証します。
	if err := jobid.Validate(jobID); err != nil {
		return nil, fmt.Errorf("不正なジョブID (%s): %w", jobID, err)
	}

	stream, err := r.reader.Open(ctx, r.uri(r.layout.ScriptPath(jobID)))
	if err != nil {
		return nil, fmt.Errorf("台本の読み込みに失敗しました: %w", err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			slog.WarnContext(ctx, "台本ストリームのクローズに失敗しました", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("台本の読み込みに失敗しました: %w", err)
	}

	var lines []domain.ScriptLine
	if err := json.Unmarshal(body, &lines); err != nil {
		return nil, fmt.Errorf("台本のJSONデコードに失敗しました: %w", err)
	}
	return lines, nil
}

// Job は履歴一覧の 1 件です。
type Job struct {
	ID string
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
			job = &Job{ID: jobID}
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
	sort.Slice(jobs, func(i, j int) bool {
		return jobid.SortKey(jobs[i].ID) > jobid.SortKey(jobs[j].ID)
	})

	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
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
