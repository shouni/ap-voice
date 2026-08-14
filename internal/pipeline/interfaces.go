// Package pipeline は、台本生成と音声合成の各段を実行します。
package pipeline

import (
	"context"

	"github.com/shouni/ap-voice/internal/domain"
)

// 段の抽象は**パッケージ内に閉じています。** 具象（ScriptStep / PublishStep）と
// 同居しているため外から実装する必要がなく、公開すると「差し替えられる」という
// 誤った期待を与えます。オーケストレーションを段から切り離して試すための継ぎ目です。
type (
	// scriptGenerator は、入力ソースから台本を生成します。
	scriptGenerator interface {
		Run(ctx context.Context, req domain.Request) ([]domain.ScriptLine, error)
	}
	// publisher は、成果物を保存します。
	publisher interface {
		// PublishScript は台本だけを保存します（generate）。
		PublishScript(ctx context.Context, outputURI string, lines []domain.ScriptLine) (string, error)
		// Run は音声を合成して保存します（synthesize）。
		Run(ctx context.Context, outputURI string, lines []domain.ScriptLine) (string, error)
	}
)
