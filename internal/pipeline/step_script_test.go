package pipeline

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shouni/ap-voice/internal/domain"

	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/go-voicevox/speaker"
)

// defaultTestModel is the default model, standing in for the head of GEMINI_MODELS.
const defaultTestModel = "default-model"

// testSpeakersJSON is a fictional roster shaped like a /speakers response.
// It is used only to build the schema's enums, so no real characters are needed.
const testSpeakersJSON = `[
	{"name":"話者アルファ","styles":[
		{"name":"標準","id":1,"type":"talk"},
		{"name":"甘め","id":2,"type":"talk"}
	]},
	{"name":"話者ベータ","styles":[{"name":"標準","id":3,"type":"talk"}]}
]`

func testSpeakers(t *testing.T) *speaker.Registry {
	t.Helper()

	reg, err := speaker.NewRegistry([]byte(testSpeakersJSON))
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return reg
}

type mockContentReader struct {
	openFunc func(ctx context.Context, uri string) (io.ReadCloser, error)
}

func (m *mockContentReader) Open(ctx context.Context, uri string) (io.ReadCloser, error) {
	return m.openFunc(ctx, uri)
}

type mockPromptBuilder struct {
	generateFunc func(mode, content string) (string, error)
}

func (m *mockPromptBuilder) Generate(mode, content string) (string, error) {
	return m.generateFunc(mode, content)
}

type mockAIClient struct {
	generateFunc func(ctx context.Context, modelName string, prompt string, opts gemini.GenerateOptions) (*gemini.Response, error)
}

func (m *mockAIClient) Generate(ctx context.Context, modelName string, prompt string, _ []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	return m.generateFunc(ctx, modelName, prompt, opts)
}

type closeTrackingReader struct {
	reader io.Reader
	closed bool
}

func (r *closeTrackingReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *closeTrackingReader) Close() error {
	r.closed = true
	return nil
}

func TestScriptStepRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := domain.Request{
		InputURI: "gs://bucket/input.txt",
		Mode:     "duet",
		AIModel:  "gemini-2.5-flash",
	}

	t.Run("ok: from reading through JSON decoding", func(t *testing.T) {
		t.Parallel()

		reader := &closeTrackingReader{reader: strings.NewReader("  これは十分に長い入力テキストです。  ")}
		readerCalled := false
		promptCalled := false
		aiCalled := false

		runner := NewScriptStep(
			&mockContentReader{
				openFunc: func(_ context.Context, uri string) (io.ReadCloser, error) {
					readerCalled = true
					if uri != req.InputURI {
						t.Fatalf("unexpected uri: %s", uri)
					}
					return reader, nil
				},
			},
			&mockPromptBuilder{
				generateFunc: func(mode, content string) (string, error) {
					promptCalled = true
					if mode != req.Mode {
						t.Fatalf("unexpected mode: %s", mode)
					}
					if content != "これは十分に長い入力テキストです。" {
						t.Fatalf("unexpected content: %s", content)
					}
					return "prompt-body", nil
				},
			},
			&mockAIClient{
				generateFunc: func(_ context.Context, modelName string, prompt string, opts gemini.GenerateOptions) (*gemini.Response, error) {
					aiCalled = true
					if modelName != req.AIModel {
						t.Fatalf("unexpected model: %s", modelName)
					}
					if prompt != "prompt-body" {
						t.Fatalf("unexpected prompt: %q", prompt)
					}
					if opts.ResponseMIMEType != "application/json" {
						t.Fatalf("unexpected ResponseMIMEType: %s", opts.ResponseMIMEType)
					}
					if opts.ResponseSchema == nil {
						t.Fatal("expected ResponseSchema to be set")
					}
					return &gemini.Response{Text: `{"title":"テスト台本","lines":[{"speaker":"ずんだもん","style":"ノーマル","text":"こんにちはなのだ"}]}`}, nil
				},
			},
			defaultTestModel,
			testSpeakers(t),
		)

		got, err := runner.Run(ctx, req)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		want := []domain.ScriptLine{
			{Speaker: "ずんだもん", Style: "ノーマル", Text: "こんにちはなのだ"},
		}
		// The title is shown in the history listing, so it comes from the same generation as the script.
		if got.Title != "テスト台本" {
			t.Errorf("Title = %q, want テスト台本", got.Title)
		}
		if len(got.Lines) != len(want) || got.Lines[0] != want[0] {
			t.Fatalf("unexpected output: %+v", got)
		}
		if !readerCalled || !promptCalled || !aiCalled {
			t.Fatalf("unexpected calls: reader=%v prompt=%v ai=%v", readerCalled, promptCalled, aiCalled)
		}
		if !reader.closed {
			t.Fatal("reader was not closed")
		}
	})

	// Even with structured output, the model sometimes returns code fences or a preamble.
	// When the raw Text went straight into json.Unmarshal, minutes of generation were lost here.
	t.Run("ok: reads a response wrapped in code fences", func(t *testing.T) {
		t.Parallel()

		runner := NewScriptStep(
			&mockContentReader{openFunc: func(context.Context, string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("これは十分に長い入力テキストです。")), nil
			}},
			&mockPromptBuilder{generateFunc: func(string, string) (string, error) { return "p", nil }},
			&mockAIClient{generateFunc: func(context.Context, string, string, gemini.GenerateOptions) (*gemini.Response, error) {
				return &gemini.Response{Text: "```json\n{\"title\":\"t\",\"lines\":[{\"speaker\":\"ずんだもん\",\"style\":\"ノーマル\",\"text\":\"x\"}]}\n```"}, nil
			}},
			defaultTestModel,
			testSpeakers(t),
		)

		got, err := runner.Run(ctx, req)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got.Title != "t" || len(got.Lines) != 1 {
			t.Fatalf("unexpected output: %+v", got)
		}
	})

	t.Run("error: empty InputURI is an error", func(t *testing.T) {
		t.Parallel()

		runner := NewScriptStep(
			&mockContentReader{},
			&mockPromptBuilder{},
			&mockAIClient{},
			defaultTestModel,
			testSpeakers(t),
		)

		_, err := runner.Run(ctx, domain.Request{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("error: returns the read failure", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("open failed")
		runner := NewScriptStep(
			&mockContentReader{
				openFunc: func(_ context.Context, _ string) (io.ReadCloser, error) {
					return nil, expectedErr
				},
			},
			&mockPromptBuilder{},
			&mockAIClient{},
			defaultTestModel,
			testSpeakers(t),
		)

		_, err := runner.Run(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("error: input that is too short is an error", func(t *testing.T) {
		t.Parallel()

		short := strings.Repeat("a", minInputContentLength-1)
		runner := NewScriptStep(
			&mockContentReader{
				openFunc: func(_ context.Context, _ string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(short)), nil
				},
			},
			&mockPromptBuilder{},
			&mockAIClient{},
			defaultTestModel,
			testSpeakers(t),
		)

		_, err := runner.Run(ctx, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("error: returns the prompt build failure", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("prompt failed")
		runner := NewScriptStep(
			&mockContentReader{
				openFunc: func(_ context.Context, _ string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("これは十分に長い入力テキストです。")), nil
				},
			},
			&mockPromptBuilder{
				generateFunc: func(_, _ string) (string, error) {
					return "", expectedErr
				},
			},
			&mockAIClient{},
			defaultTestModel,
			testSpeakers(t),
		)

		_, err := runner.Run(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("error: returns the AI generation failure", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("ai failed")
		runner := NewScriptStep(
			&mockContentReader{
				openFunc: func(_ context.Context, _ string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("これは十分に長い入力テキストです。")), nil
				},
			},
			&mockPromptBuilder{
				generateFunc: func(_, _ string) (string, error) {
					return "prompt-body", nil
				},
			},
			&mockAIClient{
				generateFunc: func(_ context.Context, _ string, _ string, _ gemini.GenerateOptions) (*gemini.Response, error) {
					return nil, expectedErr
				},
			},
			defaultTestModel,
			testSpeakers(t),
		)

		_, err := runner.Run(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("error: an invalid JSON response is an error", func(t *testing.T) {
		t.Parallel()

		runner := NewScriptStep(
			&mockContentReader{
				openFunc: func(_ context.Context, _ string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("これは十分に長い入力テキストです。")), nil
				},
			},
			&mockPromptBuilder{
				generateFunc: func(_, _ string) (string, error) {
					return "prompt-body", nil
				},
			},
			&mockAIClient{
				generateFunc: func(_ context.Context, _ string, _ string, _ gemini.GenerateOptions) (*gemini.Response, error) {
					return &gemini.Response{Text: "not json"}, nil
				},
			},
			defaultTestModel,
			testSpeakers(t),
		)

		_, err := runner.Run(ctx, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// TestScriptStepExecuteRejectsEmptyScript checks that the step fails when the AI returns an empty
// script. Carrying on with it empty would record as "succeeded" a job that stored a script
// with nothing to say.
func TestScriptStepExecuteRejectsEmptyScript(t *testing.T) {
	t.Parallel()

	step := NewScriptStep(
		&mockContentReader{
			openFunc: func(context.Context, string) (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("十分に長い入力テキストです。")), nil
			},
		},
		&mockPromptBuilder{generateFunc: func(string, string) (string, error) { return "prompt", nil }},
		&mockAIClient{
			generateFunc: func(context.Context, string, string, gemini.GenerateOptions) (*gemini.Response, error) {
				return &gemini.Response{Text: `{"title":"t","lines":[]}`}, nil
			},
		},
		"gemini-2.5-flash",
		testSpeakers(t),
	)

	sc := &Context{Request: domain.Request{InputURI: "gs://bucket/input.txt", Mode: "duet"}}
	if err := step.Execute(context.Background(), sc); err == nil {
		t.Fatal("Execute() error = nil, want an error for the empty script")
	}
	if len(sc.Script.Lines) != 0 {
		t.Errorf("empty script was placed on the context: %+v", sc.Script)
	}
}
