package builder

import (
	"context"
	"fmt"

	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-web-reader/reader"

	"github.com/shouni/ap-voice/internal/adapters"
	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/pipeline"
)

// buildPipeline assembles each step and returns the Pipeline.
func buildPipeline(ctx context.Context, appCtx *app.Container) (*pipeline.Runner, error) {
	scriptStep, err := buildScriptStep(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the script step: %w", err)
	}
	publishStep, err := buildPublishStep(ctx, appCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the publish step: %w", err)
	}

	p := pipeline.NewRunner(scriptStep, publishStep, appCtx.Notifier, appCtx.Repository, appCtx.JobStatus, appCtx.Config.Pipeline.Timeout)

	return p, nil
}

// buildScriptStep returns the step that generates the script.
func buildScriptStep(ctx context.Context, appCtx *app.Container) (*pipeline.ScriptStep, error) {
	aiClient, err := adapters.NewAIAdapter(ctx, appCtx.Config.GCP)
	if err != nil {
		return nil, err
	}

	contentReader := reader.New(
		reader.WithGCSFactory(func(_ context.Context) (remoteio.Factory, error) {
			return appCtx.Storage, nil
		}),
	)

	return pipeline.NewScriptStep(
		contentReader,
		appCtx.Prompt,
		aiClient,
		appCtx.Config.AI.GeminiModel,
		appCtx.Speakers,
	), nil
}

// buildPublishStep returns the step that stores the artifacts.
func buildPublishStep(ctx context.Context, appCtx *app.Container) (*pipeline.PublishStep, error) {
	voiceAdapter, err := adapters.NewVoiceAdapter(ctx, appCtx.HTTPClient, appCtx.Config.Voicevox, appCtx.Speakers, appCtx.Store)
	if err != nil {
		return nil, err
	}

	return pipeline.NewPublishStep(
		voiceAdapter,
		appCtx.Store,
	), nil
}
