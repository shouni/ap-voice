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

	var content string
	content, err = p.generator.Run(ctx, req)
	if err != nil {
		err = fmt.Errorf("スクリプトテキスト作成に失敗しました: %w", err)
		return err
	}
	if strings.TrimSpace(content) == "" {
		err = fmt.Errorf("AIモデルが空のスクリプトを返しました。プロンプトや入力コンテンツに問題がないか確認してください")
		return err
	}
	var publicURL string
	publicURL, err = p.publisher.Run(ctx, req.OutputURI, content)
	if err != nil {
		err = fmt.Errorf("公開処理の実行に失敗しました: %w", err)
		return err
	}

	p.notifySuccess(ctx, req, publicURL)

	return nil
}
