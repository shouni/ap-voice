package pipeline

import (
	"context"
	"errors"
	"testing"

	"ap-voice/internal/domain"
)

var _ GenerateRunner = (*MockGenerateRunner)(nil)
var _ PublishRunner = (*MockPublishRunner)(nil)
var _ domain.Notifier = (*MockNotifier)(nil)

type MockGenerateRunner struct {
	RunFunc func(ctx context.Context, req domain.Request) (string, error)
}

func (m *MockGenerateRunner) Run(ctx context.Context, req domain.Request) (string, error) {
	if m.RunFunc == nil {
		return "", nil
	}
	return m.RunFunc(ctx, req)
}

type MockPublishRunner struct {
	RunFunc func(ctx context.Context, outputURI, content string) (string, error)
}

func (m *MockPublishRunner) Run(ctx context.Context, outputURI, content string) (string, error) {
	if m.RunFunc == nil {
		return "", nil
	}
	return m.RunFunc(ctx, outputURI, content)
}

type MockNotifier struct {
	NotifyFunc        func(ctx context.Context, req domain.Request, publicURL string) error
	NotifyFailureFunc func(ctx context.Context, req domain.Request, err error) error
	NotifySkippedFunc func(ctx context.Context, req domain.Request, reason error) error
}

func (m *MockNotifier) Notify(ctx context.Context, req domain.Request, publicURL string) error {
	if m.NotifyFunc == nil {
		return nil
	}
	return m.NotifyFunc(ctx, req, publicURL)
}

func (m *MockNotifier) NotifyFailure(ctx context.Context, req domain.Request, err error) error {
	if m.NotifyFailureFunc == nil {
		return nil
	}
	return m.NotifyFailureFunc(ctx, req, err)
}

func (m *MockNotifier) NotifySkipped(ctx context.Context, req domain.Request, reason error) error {
	if m.NotifySkippedFunc == nil {
		return nil
	}
	return m.NotifySkippedFunc(ctx, req, reason)
}

func TestPipelineExecute(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := domain.Request{
		InputURI:  "gs://bucket/input.txt",
		OutputURI: "gs://bucket/output.wav",
		Mode:      "solo",
		AIModel:   "gemini-2.5-flash",
	}

	t.Run("正常系: 生成と公開と成功通知が呼ばれること", func(t *testing.T) {
		t.Parallel()

		generateCalled := false
		publishCalled := false
		notifyCalled := false

		p := NewPipeline(
			&MockGenerateRunner{
				RunFunc: func(ctx context.Context, got domain.Request) (string, error) {
					generateCalled = true
					if got != req {
						t.Fatalf("unexpected request: %+v", got)
					}
					return "generated script", nil
				},
			},
			&MockPublishRunner{
				RunFunc: func(ctx context.Context, outputURI, content string) (string, error) {
					publishCalled = true
					if outputURI != req.OutputURI {
						t.Fatalf("unexpected outputURI: %s", outputURI)
					}
					if content != "generated script" {
						t.Fatalf("unexpected content: %s", content)
					}
					return "https://example.com/audio.wav", nil
				},
			},
			&MockNotifier{
				NotifyFunc: func(ctx context.Context, got domain.Request, publicURL string) error {
					notifyCalled = true
					if got != req {
						t.Fatalf("unexpected request: %+v", got)
					}
					if publicURL != "https://example.com/audio.wav" {
						t.Fatalf("unexpected publicURL: %s", publicURL)
					}
					return nil
				},
			},
		)

		if err := p.Execute(ctx, req); err != nil {
			t.Fatalf("Execute() failed: %v", err)
		}
		if !generateCalled || !publishCalled || !notifyCalled {
			t.Fatalf("unexpected calls: generate=%v publish=%v notify=%v", generateCalled, publishCalled, notifyCalled)
		}
	})

	t.Run("異常系: 生成失敗時は失敗通知を送ってエラーを返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("generate failed")
		failureNotified := false

		p := NewPipeline(
			&MockGenerateRunner{
				RunFunc: func(ctx context.Context, got domain.Request) (string, error) {
					return "", expectedErr
				},
			},
			&MockPublishRunner{},
			&MockNotifier{
				NotifyFailureFunc: func(ctx context.Context, got domain.Request, err error) error {
					failureNotified = true
					if got != req {
						t.Fatalf("unexpected request: %+v", got)
					}
					if !errors.Is(err, expectedErr) {
						t.Fatalf("unexpected error: %v", err)
					}
					return nil
				},
			},
		)

		err := p.Execute(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
		if !failureNotified {
			t.Fatal("failure notifier was not called")
		}
	})

	t.Run("異常系: 空文字生成時は失敗通知を送ってエラーを返すこと", func(t *testing.T) {
		t.Parallel()

		failureNotified := false

		p := NewPipeline(
			&MockGenerateRunner{
				RunFunc: func(ctx context.Context, got domain.Request) (string, error) {
					return "   ", nil
				},
			},
			&MockPublishRunner{},
			&MockNotifier{
				NotifyFailureFunc: func(ctx context.Context, got domain.Request, err error) error {
					failureNotified = true
					return nil
				},
			},
		)

		if err := p.Execute(ctx, req); err == nil {
			t.Fatal("expected error, got nil")
		}
		if !failureNotified {
			t.Fatal("failure notifier was not called")
		}
	})

	t.Run("異常系: 公開失敗時は失敗通知を送ってエラーを返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("publish failed")
		failureNotified := false

		p := NewPipeline(
			&MockGenerateRunner{
				RunFunc: func(ctx context.Context, got domain.Request) (string, error) {
					return "generated script", nil
				},
			},
			&MockPublishRunner{
				RunFunc: func(ctx context.Context, outputURI, content string) (string, error) {
					return "", expectedErr
				},
			},
			&MockNotifier{
				NotifyFailureFunc: func(ctx context.Context, got domain.Request, err error) error {
					failureNotified = true
					if !errors.Is(err, expectedErr) {
						t.Fatalf("unexpected error: %v", err)
					}
					return nil
				},
			},
		)

		err := p.Execute(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
		if !failureNotified {
			t.Fatal("failure notifier was not called")
		}
	})
}

func TestPipelineNotifications(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := domain.Request{OutputURI: "gs://bucket/output.wav"}

	t.Run("notifySuccess: notifierがnilでもパニックしないこと", func(t *testing.T) {
		t.Parallel()
		p := &Pipeline{}
		p.notifySuccess(ctx, req, "")
	})

	t.Run("notifySkipped: 正しくNotifySkippedが呼ばれること", func(t *testing.T) {
		t.Parallel()

		called := false
		reason := errors.New("skip reason")
		p := &Pipeline{
			notifier: &MockNotifier{
				NotifySkippedFunc: func(ctx context.Context, got domain.Request, gotReason error) error {
					called = true
					if got != req {
						t.Fatalf("unexpected request: %+v", got)
					}
					if !errors.Is(gotReason, reason) {
						t.Fatalf("unexpected reason: %v", gotReason)
					}
					return nil
				},
			},
		}

		p.notifySkipped(ctx, req, reason)
		if !called {
			t.Fatal("NotifySkipped was not called")
		}
	})
}
