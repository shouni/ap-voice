// Package pipeline は、台本生成と音声合成のワーカー本体です。
package pipeline

import (
	"context"

	"github.com/shouni/ap-voice/internal/domain"
)

// Step は、ワーカーの工程 1 つです。どのコマンドがどの工程列になるかは
// planner.go（DefaultPlanner.Plan）だけが知っています。
//
// 具象（ScriptStep / LoadScriptStep / PublishStep）と同居しているため外から実装する
// 必要はありません。公開しているのは、テストが工程を差し替えて Runner の統制だけを
// 試すためです。
type Step interface {
	Name() string
	Execute(ctx context.Context, sc *Context) error
}

// Context は、工程間で引き継ぐ実行コンテキストです。Request は固定で、
// 残りは工程が順に埋めていきます。
type Context struct {
	Request domain.Request
	// Script は、生成または読み出した台本です（ScriptStep / LoadScriptStep が埋めます）。
	Script domain.Script
	// PublicURL は、成果物の公開 URL です（PublishStep が埋めます。無いこともあります）。
	PublicURL string
}
