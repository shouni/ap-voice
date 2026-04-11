package pipeline

import (
	"context"
	"fmt"
	"strings"

	"ap-voice/internal/domain"
)

// Pipeline はパイプラインの実行に必要な外部依存関係を保持するサービス構造体です。
type Pipeline struct {
	generator GenerateRunner
	publisher PublishRunner
}

// NewPipeline は、Pipeline を生成します。
func NewPipeline(generator GenerateRunner, publisher PublishRunner) *Pipeline {
	return &Pipeline{
		generator: generator,
		publisher: publisher,
	}
}

// Execute は、すべての依存関係を構築し実行します。
func (p *Pipeline) Execute(ctx context.Context, req domain.Request) error {
	content, err := p.generator.Run(ctx, req)
	if err != nil {
		return fmt.Errorf("スクリプトテキスト作成に失敗しました: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("AIモデルが空のスクリプトを返しました。プロンプトや入力コンテンツに問題がないか確認してください")
	}
	if err = p.publisher.Run(ctx, req.OutputURI, content); err != nil {
		return fmt.Errorf("公開処理の実行に失敗しました: %w", err)
	}
	return nil
}
