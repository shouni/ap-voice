package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/internal/domain"
)

// minInputContentLength is the minimum length of input text that generation proceeds with.
//
// It belongs to the step that uses it, not to config. It is not a value the deployment target decides
// (there is no env for it), and pipeline used to import config for this one value. Input shorter
// than this does not determine what is meant to be read.
const minInputContentLength = 10

// PromptBuilder defines the responsibility of producing the prompt string.
type PromptBuilder interface {
	Generate(mode, content string) (string, error)
}

// ContentReader is the interface for fetching content from a given URI.
type ContentReader interface {
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
}

// StructuredGenerator is the generation interface that supports structured output through ResponseSchema.
//
// It has the same shape as genai-kit's gemini.Generator. Because it contains no genai types,
// this package need not import the genai SDK, and a mock is a single method.
type StructuredGenerator interface {
	Generate(ctx context.Context, modelName string, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error)
}

// ScriptStep holds the dependencies and options needed to generate a script.
type ScriptStep struct {
	reader        ContentReader
	promptBuilder PromptBuilder
	aiClient      StructuredGenerator
	// defaultModel is the model name used when the Request does not specify one.
	// It is the head of GEMINI_MODELS and has passed startup validation, so it is never empty.
	defaultModel string
	// schema is the response schema built from the speaker roster. There is no reason to rebuild
	// it on every run, so it is built once at construction.
	schema *gemini.Schema
}

// NewScriptStep injects the dependencies and creates a new ScriptStep.
func NewScriptStep(
	reader ContentReader,
	promptBuilder PromptBuilder,
	aiClient StructuredGenerator,
	defaultModel string,
	speakers *speaker.Registry,
) *ScriptStep {
	return &ScriptStep{
		reader:        reader,
		promptBuilder: promptBuilder,
		aiClient:      aiClient,
		defaultModel:  defaultModel,
		schema:        scriptResponseSchema(speakers),
	}
}

// Name is the step name.
func (*ScriptStep) Name() string { return "generate_script" }

// Execute generates the script and puts it on sc.Script.
func (gr *ScriptStep) Execute(ctx context.Context, sc *Context) error {
	script, err := gr.Run(ctx, sc.Request)
	if err != nil {
		return fmt.Errorf("failed to create the script text: %w", err)
	}
	if len(script.Lines) == 0 {
		return fmt.Errorf("the AI model returned an empty script; check the prompt and the input content")
	}
	sc.Script = script
	return nil
}

// modelFor returns the model name the request specified.
// Without one it uses the default model. The caller assembles the task payload, so a missing
// model name is quite possible, and there is no reason to fail the whole generation over it.
func (gr *ScriptStep) modelFor(req domain.Request) string {
	if model := strings.TrimSpace(req.AIModel); model != "" {
		return model
	}
	return gr.defaultModel
}

// Run reads the content from the input source and has the AI model generate a structured narration script.
func (gr *ScriptStep) Run(ctx context.Context, req domain.Request) (domain.Script, error) {
	if req.InputURI == "" {
		return domain.Script{}, errors.New("no input source (InputURI) was given")
	}
	content, err := gr.readContent(ctx, req.InputURI)
	if err != nil {
		return domain.Script{}, err
	}
	model := gr.modelFor(req)
	slog.Info("starting", "mode", req.Mode, "model", model, "input_size", len(content))
	slog.Info("starting AI script generation")

	prompt, err := gr.promptBuilder.Generate(req.Mode, content)
	if err != nil {
		return domain.Script{}, err
	}

	generatedResponse, err := gr.aiClient.Generate(ctx, model, prompt, nil, gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema:   gr.schema,
	})
	if err != nil {
		return domain.Script{}, fmt.Errorf("script generation failed: %w", err)
	}

	// gemini.DecodeJSON owns the emptiness check, repairs such as code fences, and decoding.
	// Even structured output sometimes mixes a preamble or fences into the body, and calling
	// json.Unmarshal on the raw Text would throw away minutes of generation right there.
	script, err := gemini.DecodeJSON[domain.Script](generatedResponse.Text)
	if err != nil {
		return domain.Script{}, fmt.Errorf("failed to decode the AI response JSON: %w", err)
	}
	slog.Info("AI script generation finished", "line_count", len(script.Lines), "title", script.Title)

	return script, nil
}

// readContent fetches content from the given source URL.
func (gr *ScriptStep) readContent(ctx context.Context, sourceURL string) (string, error) {
	stream, err := gr.reader.Open(ctx, sourceURL)
	if err != nil {
		return "", fmt.Errorf("failed to read source: %w", err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			slog.WarnContext(ctx, "failed to close the stream", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("failed to read the content: %w", err)
	}

	trimmedContent := strings.TrimSpace(string(body))
	if len(trimmedContent) < minInputContentLength {
		return "", fmt.Errorf("the input content is too short")
	}
	return trimmedContent, nil
}
