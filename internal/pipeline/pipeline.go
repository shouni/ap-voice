package pipeline

import (
	"context"
	"fmt"

	"github.com/shouni/ap-voice/internal/domain"
)

// Pipeline はパイプラインの実行に必要な外部依存関係を保持するサービス構造体です。
type Pipeline struct {
	generator GenerateRunner
	publisher PublishRunner
	notifier  domain.Notifier
}

// NewPipeline は、Pipeline を生成します。
func NewPipeline(generator GenerateRunner, publisher PublishRunner, notifier domain.Notifier) *Pipeline {
	return &Pipeline{
		generator: generator,
		publisher: publisher,
		notifier:  notifier,
	}
}

// Execute は、すべての依存関係を構築し実行します。
func (p *Pipeline) Execute(ctx context.Context, req domain.Request) (err error) {
	defer func() {
		if err != nil {
			p.notifyFailure(ctx, req, err)
		}
	}()

	if err = req.Validate(); err != nil {
		return err
	}

	var lines []domain.ScriptLine
	lines, err = p.resolveScript(ctx, req)
	if err != nil {
		return err
	}

	var publicURL string
	publicURL, err = p.publisher.Run(ctx, req.OutputURI, lines)
	if err != nil {
		err = fmt.Errorf("公開処理の実行に失敗しました: %w", err)
		return err
	}

	p.notifySuccess(ctx, req, publicURL)

	return nil
}

// resolveScript は、Command に応じて合成対象の台本を用意します。
//
// generate は入力ソースから作り、synthesize は渡されたものをそのまま使います。
// どちらの経路でも、以降の公開処理は同じ台本を受け取ります。
func (p *Pipeline) resolveScript(ctx context.Context, req domain.Request) ([]domain.ScriptLine, error) {
	if req.Command == domain.CommandSynthesize {
		// Validate が空でないことを確かめているため、ここでの再検査は要りません。
		return req.Script, nil
	}

	lines, err := p.generator.Run(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("スクリプトテキスト作成に失敗しました: %w", err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("AIモデルが空のスクリプトを返しました。プロンプトや入力コンテンツに問題がないか確認してください")
	}

	return lines, nil
}
