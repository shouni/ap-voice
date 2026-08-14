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

// buildPipeline は、各段を組み立てて Pipeline を返します。
func buildPipeline(ctx context.Context, appCtx *app.Container) (*pipeline.Pipeline, error) {
	scriptStep, err := buildScriptStep(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("台本生成の段の初期化に失敗しました: %w", err)
	}
	publishStep, err := buildPublishStep(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("保存の段の初期化に失敗しました: %w", err)
	}

	p := pipeline.NewPipeline(scriptStep, publishStep, appCtx.Notifier, appCtx.Repository, appCtx.Config.Pipeline.Timeout)

	return p, nil
}

// buildScriptStep は、台本を生成する段を返します。
func buildScriptStep(ctx context.Context, appCtx *app.Container) (*pipeline.ScriptStep, error) {
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

// buildPublishStep は、成果物を保存する段を返します。
func buildPublishStep(ctx context.Context, appCtx *app.Container) (*pipeline.PublishStep, error) {
	voiceAdapter, err := adapters.NewVoiceAdapter(ctx, appCtx.HTTPClient, appCtx.Config.Voicevox, appCtx.Speakers, appCtx.RemoteIO.Writer)
	if err != nil {
		return nil, err
	}

	return pipeline.NewPublishStep(
		voiceAdapter,
		appCtx.RemoteIO.Signer,
	), nil
}
