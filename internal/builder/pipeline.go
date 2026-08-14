package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-web-reader/pkg/reader"

	"github.com/shouni/ap-voice/internal/adapters"
	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/pipeline"
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

	p := pipeline.NewPipeline(generateRunner, publisherRunner, appCtx.Notifier, appCtx.Repository, appCtx.Config.Pipeline.Timeout)

	return p, nil
}

// buildGenerateRunner は、GenerateRunner のインスタンスを返します。
func buildGenerateRunner(ctx context.Context, appCtx *app.Container) (*pipeline.ScriptStep, error) {
	promptBuilder, err := adapters.NewPromptAdapter(appCtx.Speakers)
	if err != nil {
		return nil, fmt.Errorf("プロンプトビルダーの作成に失敗しました: %w", err)
	}

	aiClient, err := adapters.NewAIAdapter(ctx, appCtx.Config)
	if err != nil {
		return nil, err
	}

	contentReader, err := reader.New(
		reader.WithGCSFactory(func(_ context.Context) (remoteio.IOFactory, error) {
			return appCtx.RemoteIO.Factory, nil
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize content reader: %w", err)
	}

	return pipeline.NewScriptStep(
		contentReader,
		promptBuilder,
		aiClient,
		appCtx.Config.AI.GeminiModel,
		appCtx.Speakers,
	), nil
}

// buildPublishRunner は、PublisherRunner のインスタンスを返します。
func buildPublishRunner(ctx context.Context, appCtx *app.Container) (*pipeline.PublishStep, error) {
	voiceAdapter, err := adapters.NewVoiceAdapter(ctx, appCtx.HTTPClient, appCtx.Config.Voicevox, appCtx.Speakers, appCtx.RemoteIO.Writer)
	if err != nil {
		return nil, err
	}

	return pipeline.NewPublishStep(
		voiceAdapter,
		appCtx.RemoteIO.Signer,
	), nil
}
