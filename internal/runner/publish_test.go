package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shouni/go-remote-io/remoteio"
)

type mockVoice struct {
	uploadWavFunc    func(ctx context.Context, outputURI, content string) error
	uploadScriptFunc func(ctx context.Context, outputURI, content string) error
}

func (m *mockVoice) UploadWav(ctx context.Context, outputURI, content string) error {
	return m.uploadWavFunc(ctx, outputURI, content)
}

func (m *mockVoice) UploadScript(ctx context.Context, outputURI, content string) error {
	return m.uploadScriptFunc(ctx, outputURI, content)
}

type mockURLSigner struct {
	generateSignedURLFunc func(ctx context.Context, path string, method string, expires time.Duration) (string, error)
}

func (m *mockURLSigner) GenerateSignedURL(ctx context.Context, path string, method string, expires time.Duration) (string, error) {
	return m.generateSignedURLFunc(ctx, path, method, expires)
}

var _ remoteio.URLSigner = (*mockURLSigner)(nil)

func TestPublishRunnerRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	outputURI := "gs://bucket/audio.wav"
	content := "generated script"

	t.Run("正常系: 音声とスクリプトを書き込み署名付きURLを返すこと", func(t *testing.T) {
		t.Parallel()

		wavCalled := false
		scriptCalled := false
		signerCalled := false

		runner := NewPublishRunner(
			&mockVoice{
				uploadWavFunc: func(ctx context.Context, gotURI, gotContent string) error {
					wavCalled = true
					if gotURI != outputURI || gotContent != content {
						t.Fatalf("unexpected wav args: %s %s", gotURI, gotContent)
					}
					return nil
				},
				uploadScriptFunc: func(ctx context.Context, gotURI, gotContent string) error {
					scriptCalled = true
					if gotURI != outputURI || gotContent != content {
						t.Fatalf("unexpected script args: %s %s", gotURI, gotContent)
					}
					return nil
				},
			},
			&mockURLSigner{
				generateSignedURLFunc: func(ctx context.Context, path string, method string, expires time.Duration) (string, error) {
					signerCalled = true
					if path != outputURI {
						t.Fatalf("unexpected path: %s", path)
					}
					if method != "GET" {
						t.Fatalf("unexpected method: %s", method)
					}
					if expires != time.Hour {
						t.Fatalf("unexpected expires: %s", expires)
					}
					return "https://example.com/audio.wav", nil
				},
			},
		)

		got, err := runner.Run(ctx, outputURI, content)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got != "https://example.com/audio.wav" {
			t.Fatalf("unexpected url: %s", got)
		}
		if !wavCalled || !scriptCalled || !signerCalled {
			t.Fatalf("unexpected calls: wav=%v script=%v signer=%v", wavCalled, scriptCalled, signerCalled)
		}
	})

	t.Run("正常系: signer が nil ならURLなしで成功すること", func(t *testing.T) {
		t.Parallel()

		runner := NewPublishRunner(
			&mockVoice{
				uploadWavFunc:    func(ctx context.Context, gotURI, gotContent string) error { return nil },
				uploadScriptFunc: func(ctx context.Context, gotURI, gotContent string) error { return nil },
			},
			nil,
		)

		got, err := runner.Run(ctx, outputURI, content)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty url, got %s", got)
		}
	})

	t.Run("正常系: signer エラー時も公開成功としてURLなしで返すこと", func(t *testing.T) {
		t.Parallel()

		runner := NewPublishRunner(
			&mockVoice{
				uploadWavFunc:    func(ctx context.Context, gotURI, gotContent string) error { return nil },
				uploadScriptFunc: func(ctx context.Context, gotURI, gotContent string) error { return nil },
			},
			&mockURLSigner{
				generateSignedURLFunc: func(ctx context.Context, path string, method string, expires time.Duration) (string, error) {
					return "", errors.New("sign failed")
				},
			},
		)

		got, err := runner.Run(ctx, outputURI, content)
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty url, got %s", got)
		}
	})

	t.Run("異常系: outputURI が空ならエラーになること", func(t *testing.T) {
		t.Parallel()

		runner := NewPublishRunner(
			&mockVoice{
				uploadWavFunc:    func(ctx context.Context, gotURI, gotContent string) error { return nil },
				uploadScriptFunc: func(ctx context.Context, gotURI, gotContent string) error { return nil },
			},
			nil,
		)

		_, err := runner.Run(ctx, "", content)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("異常系: UploadWav 失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("wav failed")
		runner := NewPublishRunner(
			&mockVoice{
				uploadWavFunc:    func(ctx context.Context, gotURI, gotContent string) error { return expectedErr },
				uploadScriptFunc: func(ctx context.Context, gotURI, gotContent string) error { return nil },
			},
			nil,
		)

		_, err := runner.Run(ctx, outputURI, content)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("異常系: UploadScript 失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("script failed")
		runner := NewPublishRunner(
			&mockVoice{
				uploadWavFunc:    func(ctx context.Context, gotURI, gotContent string) error { return nil },
				uploadScriptFunc: func(ctx context.Context, gotURI, gotContent string) error { return expectedErr },
			},
			nil,
		)

		_, err := runner.Run(ctx, outputURI, content)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})
}
