package pipeline

import (
	"context"
	"fmt"
	"strings"

	"prototypus-ai-doc-go/internal/domain"
)

// Pipeline はパイプラインの実行に必要な外部依存関係を保持するサービス構造体です。
type Pipeline struct {
	generator domain.GenerateRunner
	publisher domain.PublishRunner
}

// NewPipeline は、Pipeline を生成します。
func NewPipeline(generator domain.GenerateRunner, publisher domain.PublishRunner) *Pipeline {
	return &Pipeline{
		generator: generator,
		publisher: publisher,
	}
}

// Execute は、すべての依存関係を構築し実行します。
func (p *Pipeline) Execute(
	ctx context.Context,
) error {
	generatedScript, err := p.generate(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(generatedScript) == "" {
		return fmt.Errorf("AIモデルが空のスクリプトを返しました。プロンプトや入力コンテンツに問題がないか確認してください")
	}
	err = p.publish(ctx, generatedScript)
	if err != nil {
		return err
	}

	return nil
}

// generate は、スクリプトテキスト作成を実行します。
// 実行結果の文字列とエラーを返します。
func (p *Pipeline) generate(
	ctx context.Context,
) (string, error) {
	generatedScript, err := p.generator.Run(ctx)
	if err != nil {
		return "", fmt.Errorf("スクリプトテキスト作成に失敗しました: %w", err)
	}

	return generatedScript, nil
}

// publish は、パブリッシュを実行します。
func (p *Pipeline) publish(
	ctx context.Context,
	scriptContent string,
) error {
	err := p.publisher.Run(ctx, scriptContent)
	if err != nil {
		return fmt.Errorf("公開処理の実行に失敗しました: %w", err)
	}

	return nil
}
