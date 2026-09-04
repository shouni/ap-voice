package pipeline

import (
	"fmt"

	"github.com/shouni/ap-voice/internal/domain"
)

// Planner は、依頼に応じて実行する工程列（実行計画）を決めます。
// コマンドから工程への対応はここ 1 箇所だけが持ちます（public-docs のワーカー規約 2.5）。
type Planner interface {
	Plan(req domain.Request) ([]Step, error)
}

// DefaultPlanner は、本番用の Planner です。
//
// generate は台本を作って保存するところまで、synthesize は保存済みの台本を読んで
// 音声を作るところまで、generate_and_synthesize は両方です。台本の出どころ（生成か
// 読み出しか）だけが違い、保存・合成は同じ工程です。
type DefaultPlanner struct {
	Generate Step
	Load     Step
	Publish  Step
}

// Plan は、コマンドに対応する工程列を返します。
func (p DefaultPlanner) Plan(req domain.Request) ([]Step, error) {
	switch req.Command {
	case domain.CommandGenerate, domain.CommandGenerateAndSynthesize:
		return []Step{p.Generate, p.Publish}, nil
	case domain.CommandSynthesize:
		return []Step{p.Load, p.Publish}, nil
	default:
		return nil, fmt.Errorf("%w: %q", domain.ErrUnknownCommand, req.Command)
	}
}
