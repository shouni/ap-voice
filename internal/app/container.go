// Package app は、アプリケーションの依存関係を組み立てて保持する DI コンテナを提供します。
package app

import (
	"io"
	"log/slog"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-job-firestore/jobfirestore"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/internal/repository"
)

// Container はアプリケーションの依存関係（DIコンテナ）を保持します。
type Container struct {
	Config *config.Config
	// I/O and Storage
	// Storage は GCS クライアントの寿命を持ちます。go-web-reader のように
	// ファクトリそのものを要求する相手へ渡すために保持しています。
	Storage remoteio.Factory
	// Store は Storage から取り出した読み書きの窓口です。
	Store remoteio.Store
	// Speakers は使用する話者・スタイルの一覧です。assets/speakers.json を
	// 解釈したもので、レスポンススキーマの構築と合成の両方がここを見ます。
	Speakers *speaker.Registry
	// TaskQueue は Web 面が実行を Worker 面へ渡す口です。Worker 面では nil です。
	TaskQueue domain.TaskQueue
	// Repository は成果物の読み出しです。履歴の表示と、保存済み台本からの合成に使います。
	Repository *repository.Repository
	// External Adapters
	HTTPClient httpkit.Requester
	Notifier   domain.Notifier
	// JobStatus はジョブの進行状況を記録します。Web 面が queued を、
	// Worker 面が running / succeeded / failed を書きます。
	JobStatus *jobfirestore.Recorder[domain.JobStatus]
	// Business Logic
	Pipeline domain.Pipeline
	// Closers は、組み立て時に開いた資源です。Container.Close がまとめて閉じます。
	// Close が個々のフィールドを見ないのは、資源が増えたときに builder が append
	// するだけで済ませるためです。
	Closers []io.Closer
}

// Close は、Container が保持するすべての外部接続リソースを安全に解放します。
//
// エラーを返さないのは、呼び出し元が server.Run の defer 1 箇所きりで、返したところで
// slog.Error 以外の行き先が無いためです。
func (c *Container) Close() {
	for _, closer := range c.Closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			slog.Error("failed to close resource", "error", err)
		}
	}
}
