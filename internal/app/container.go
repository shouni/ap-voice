// Package app は、アプリケーションの依存関係を組み立てて保持する DI コンテナを提供します。
package app

import (
	"errors"
	"io"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/internal/repository"
)

// Container はアプリケーションの依存関係（DIコンテナ）を保持します。
type Container struct {
	Config *config.Config
	// I/O and Storage
	RemoteIO *RemoteIO
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
	JobStatus *jobstatus.Recorder[jobstatus.Status]
	// Business Logic
	Pipeline domain.Pipeline
	// Closers は、組み立て時に開いた資源です。Container.Close がまとめて閉じます。
	Closers []io.Closer
}

// RemoteIO は外部ストレージ操作に関するコンポーネントをまとめます。
//
// 実体は go-remote-io が持つ remoteio.Bundle です。同じ構造体と組み立て関数を
// 各アプリが個別に持っていたものをライブラリへ引き取ったため、ここはアプリ内での
// 呼び名を保つための別名だけになっています（rio.Writer などの参照はそのまま使えます）。
type RemoteIO = remoteio.Bundle

// Close は、Container が保持するすべての外部接続リソースを安全に解放します。
func (c *Container) Close() error {
	if c == nil {
		return nil
	}
	var errs error
	for _, closer := range c.Closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	if c.RemoteIO != nil {
		if err := c.RemoteIO.Close(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
