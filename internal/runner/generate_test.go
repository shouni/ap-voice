package runner

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"ap-voice/internal/config"
	"ap-voice/internal/domain"

	"github.com/shouni/go-gemini-client/gemini"
)

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
	generateContentFunc func(ctx context.Context, modelName string, prompt string) (*gemini.Response, error)
}

func (m *mockAIClient) GenerateContent(ctx context.Context, modelName string, prompt string) (*gemini.Response, error) {
	return m.generateContentFunc(ctx, modelName, prompt)
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

func TestGenerateRunnerRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := domain.Request{
		InputURI: "gs://bucket/input.txt",
		Mode:     "duet",
		AIModel:  "gemini-2.5-flash",
	}

	t.Run("正常系: 読み込みから生成まで通ること", func(t *testing.T) {
		t.Parallel()

		reader := &closeTrackingReader{reader: strings.NewReader("  これは十分に長い入力テキストです。  ")}
		readerCalled := false
		promptCalled := false
		aiCalled := false

		runner := NewGenerateRunner(
			&mockContentReader{
				openFunc: func(ctx context.Context, uri string) (io.ReadCloser, error) {
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
				generateContentFunc: func(ctx context.Context, modelName string, prompt string) (*gemini.Response, error) {
					aiCalled = true
					if modelName != req.AIModel {
						t.Fatalf("unexpected model: %s", modelName)
					}
					if prompt != "prompt-body" {
						t.Fatalf("unexpected prompt: %s", prompt)
					}
					return &gemini.Response{Text: "generated script"}, nil
				},
			},
		)

		got, err := runner.Run(ctx, req)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got != "generated script" {
			t.Fatalf("unexpected output: %s", got)
		}
		if !readerCalled || !promptCalled || !aiCalled {
			t.Fatalf("unexpected calls: reader=%v prompt=%v ai=%v", readerCalled, promptCalled, aiCalled)
		}
		if !reader.closed {
			t.Fatal("reader was not closed")
		}
	})

	t.Run("異常系: InputURI が空ならエラーになること", func(t *testing.T) {
		t.Parallel()

		runner := NewGenerateRunner(
			&mockContentReader{},
			&mockPromptBuilder{},
			&mockAIClient{},
		)

		_, err := runner.Run(ctx, domain.Request{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("異常系: 読み込み失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("open failed")
		runner := NewGenerateRunner(
			&mockContentReader{
				openFunc: func(ctx context.Context, uri string) (io.ReadCloser, error) {
					return nil, expectedErr
				},
			},
			&mockPromptBuilder{},
			&mockAIClient{},
		)

		_, err := runner.Run(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("異常系: 入力が短すぎるとエラーになること", func(t *testing.T) {
		t.Parallel()

		short := strings.Repeat("a", config.MinInputContentLength-1)
		runner := NewGenerateRunner(
			&mockContentReader{
				openFunc: func(ctx context.Context, uri string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(short)), nil
				},
			},
			&mockPromptBuilder{},
			&mockAIClient{},
		)

		_, err := runner.Run(ctx, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("異常系: プロンプト生成失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("prompt failed")
		runner := NewGenerateRunner(
			&mockContentReader{
				openFunc: func(ctx context.Context, uri string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("これは十分に長い入力テキストです。")), nil
				},
			},
			&mockPromptBuilder{
				generateFunc: func(mode, content string) (string, error) {
					return "", expectedErr
				},
			},
			&mockAIClient{},
		)

		_, err := runner.Run(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("異常系: AI生成失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("ai failed")
		runner := NewGenerateRunner(
			&mockContentReader{
				openFunc: func(ctx context.Context, uri string) (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader("これは十分に長い入力テキストです。")), nil
				},
			},
			&mockPromptBuilder{
				generateFunc: func(mode, content string) (string, error) {
					return "prompt-body", nil
				},
			},
			&mockAIClient{
				generateContentFunc: func(ctx context.Context, modelName string, prompt string) (*gemini.Response, error) {
					return nil, expectedErr
				},
			},
		)

		_, err := runner.Run(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})
}
