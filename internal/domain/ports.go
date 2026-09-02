package domain

import (
	"context"
	"errors"
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

// ErrScriptNotFound は、そのジョブの台本がまだ保存されていないことを表します。
//
// ポートの契約なのでここに置きます。実装側に置くと、「見つからない」を判定したい
// 呼び出し元まで実装パッケージを import することになり、ポートを挟んだ意味が
// 無くなります。
var ErrScriptNotFound = errors.New("domain: script not found")

// ScriptStore は、保存済みの台本を読み書きします。
//
// generate が書き、synthesize と詳細画面が読みます。この口があるのは、台本を
// タスクのペイロードで運ばないためです（理由は Request.JobID）。
//
// 台本がまだ無い場合は ErrScriptNotFound を返します。読めなかった（一時障害）
// のとは別物です。前者は待っても直りませんが、後者は直り得ます。
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
