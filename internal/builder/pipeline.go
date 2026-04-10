package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-web-exact/v2/extract"

	"prototypus-ai-doc-go/internal/adapters"
	"prototypus-ai-doc-go/internal/app"
	"prototypus-ai-doc-go/internal/pipeline"
	"prototypus-ai-doc-go/internal/runner"
)

// buildPipeline は、提供されたランナーを使用して新しいパイプラインを初期化して返します。
func buildPipeline(ctx context.Context, appCtx *app.Container) (*pipeline.Pipeline, error) {
	generateRunner, err := buildGenerateRunner(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("生成ランナーの初期化に失敗しました: %w", err)
	}
	publisherRunner, err := buildPublishRunner(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("パブリッシャーランナーの初期化に失敗しました: %w", err)
	}

	p := pipeline.NewPipeline(generateRunner, publisherRunner)

	return p, nil
}

// buildGenerateRunner は、GenerateRunner のインスタンスを返します。
func buildGenerateRunner(ctx context.Context, appCtx *app.Container) (*runner.GenerateRunner, error) {
	extractor, err := extract.NewExtractor(appCtx.HTTPClient)
	if err != nil {
		return nil, fmt.Errorf("エクストラクタの初期化に失敗しました: %w", err)
	}

	promptBuilder, err := adapters.NewPromptAdapter()
	if err != nil {
		return nil, fmt.Errorf("プロンプトビルダーの作成に失敗しました: %w", err)
	}

	aiClient, err := adapters.NewAIAdapter(ctx, appCtx.Config)
	if err != nil {
		return nil, err
	}

	return runner.NewGenerateRunner(
		appCtx.Config,
		extractor,
		promptBuilder,
		aiClient,
		appCtx.RemoteIO.Reader,
	), nil
}

// buildPublishRunner は、PublisherRunner のインスタンスを返します。
func buildPublishRunner(ctx context.Context, appCtx *app.Container) (*runner.PublishRunner, error) {
	voicevoxExecutor, err := adapters.NewVoiceAdapter(ctx, appCtx.HTTPClient, appCtx.RemoteIO.Writer, appCtx.Config.VoicevoxOutput)
	if err != nil {
		return nil, err
	}

	return runner.NewPublishRunner(
		appCtx.Config,
		voicevoxExecutor,
		appCtx.RemoteIO.Writer,
	), nil
}
