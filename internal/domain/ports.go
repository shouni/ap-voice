package domain

import (
	"context"
)

// Pipeline は、処理を行うインターフェースです。
type Pipeline interface {
	// Execute は、すべての依存関係を構築し実行します。
	Execute(ctx context.Context, req Request) error
}

// VoicevoxExecutor は、音声合成を行うインターフェースです。
type VoicevoxExecutor interface {
	Run(ctx context.Context, outputURI, content string) error
}
