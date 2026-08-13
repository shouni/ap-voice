package domain

import (
	"context"
)

// Pipeline は、処理を行うインターフェースです。
type Pipeline interface {
	// Execute は、すべての依存関係を構築し実行します。
	Execute(ctx context.Context, req Request) error
}

// Voice は、音声合成を行うインターフェースです。
type Voice interface {
	UploadWav(ctx context.Context, outputURI string, lines []ScriptLine) error
	UploadScript(ctx context.Context, outputURI string, lines []ScriptLine) error
}

// TaskQueue は、実行を Worker 面へ引き渡すインターフェースです。
//
// Web 面は Worker を直接呼ばず、必ずキュー越しに渡します。合成は分単位かかるため
// リクエストの中で待てず、また公開側と実行側で権限を分けるためでもあります。
type TaskQueue interface {
	Enqueue(ctx context.Context, req Request) error
}
