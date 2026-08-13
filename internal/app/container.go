// Package app は、アプリケーションの依存関係を組み立てて保持する DI コンテナを提供します。
package app

import (
	"errors"

	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"

	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"
)

// Container はアプリケーションの依存関係（DIコンテナ）を保持します。
type Container struct {
	Config *config.Config
	// I/O and Storage
	RemoteIO *RemoteIO
	// Speakers は使用する話者・スタイルの一覧です。assets/speakers/speakers.json を
	// 解釈したもので、レスポンススキーマの構築と合成の両方がここを見ます。
	Speakers *speaker.Registry
	// External Adapters
	HTTPClient httpkit.Requester
	Notifier   domain.Notifier
	// Business Logic
	Pipeline domain.Pipeline
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
	if c.RemoteIO != nil {
		if err := c.RemoteIO.Close(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	return errs
}
