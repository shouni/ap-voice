package domain

import (
	"context"
)

// Pipeline は、処理を行うインターフェースです。
type Pipeline interface {
	// Execute は、すべての依存関係を構築し実行します。
	Execute(ctx context.Context, req Request) error
}

// Voice は、音声合成と成果物の保存を行うインターフェースです。
type Voice interface {
	UploadWav(ctx context.Context, outputURI string, lines []ScriptLine) error
	UploadScript(ctx context.Context, outputURI string, script Script) error
}

// ScriptStore は、保存済みの台本を読み書きします。
//
// generate が書き、synthesize と詳細画面が読みます。台本を Cloud Tasks の
// ペイロードで運ばないのは、長い台本が 1MB の上限に当たりうるためです。
type ScriptStore interface {
	Load(ctx context.Context, jobID string) (Script, error)
}

// TaskQueue は、実行を Worker 面へ引き渡すインターフェースです。
//
// Web 面は Worker を直接呼ばず、必ずキュー越しに渡します。合成は分単位かかるため
// リクエストの中で待てず、また公開側と実行側で権限を分けるためでもあります。
type TaskQueue interface {
	Enqueue(ctx context.Context, req Request) error
}
