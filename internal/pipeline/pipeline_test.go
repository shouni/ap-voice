package pipeline

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shouni/ap-voice/internal/domain"
)

var _ scriptGenerator = (*MockScriptStep)(nil)
var _ publisher = (*MockPublishStep)(nil)
var _ domain.Notifier = (*MockNotifier)(nil)

type MockScriptStep struct {
	RunFunc func(ctx context.Context, req domain.Request) (domain.Script, error)
}

func (m *MockScriptStep) Run(ctx context.Context, req domain.Request) (domain.Script, error) {
	if m.RunFunc == nil {
		return domain.Script{}, nil
	}
	return m.RunFunc(ctx, req)
}

type MockPublishStep struct {
	RunFunc           func(ctx context.Context, outputURI string, script domain.Script) (string, error)
	PublishScriptFunc func(ctx context.Context, outputURI string, script domain.Script) (string, error)
}

func (m *MockPublishStep) PublishScript(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if m.PublishScriptFunc == nil {
		return "", nil
	}
	return m.PublishScriptFunc(ctx, outputURI, script)
}

// MockScriptStore は保存済み台本の読み出しを差し替えます。
type MockScriptStore struct {
	LoadFunc func(ctx context.Context, jobID string) (domain.Script, error)
}

func (m *MockScriptStore) Load(ctx context.Context, jobID string) (domain.Script, error) {
	if m.LoadFunc == nil {
		return domain.Script{}, nil
	}
	return m.LoadFunc(ctx, jobID)
}

func (m *MockPublishStep) Run(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if m.RunFunc == nil {
		return "", nil
	}
	return m.RunFunc(ctx, outputURI, script)
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
		Command:   domain.CommandGenerate,
		InputURI:  "gs://bucket/input.txt",
		OutputURI: "gs://bucket/output.wav",
		Mode:      "solo",
		AIModel:   "gemini-2.5-flash",
	}
	sampleLines := []domain.ScriptLine{
		{Speaker: "ずんだもん", Style: "ノーマル", Text: "generated script"},
	}

	t.Run("正常系: 生成と公開と成功通知が呼ばれること", func(t *testing.T) {
		t.Parallel()

		generateCalled := false
		publishCalled := false
		notifyCalled := false

		p := NewPipeline(
			&MockScriptStep{
				RunFunc: func(_ context.Context, got domain.Request) (domain.Script, error) {
					generateCalled = true
					if !reflect.DeepEqual(got, req) {
						t.Fatalf("unexpected request: %+v", got)
					}
					return domain.Script{Title: "テスト台本", Lines: sampleLines}, nil
				},
			},
			&MockPublishStep{
				// generate は台本だけを保存します。Run（音声合成）は呼ばれてはいけません。
				PublishScriptFunc: func(_ context.Context, outputURI string, script domain.Script) (string, error) {
					publishCalled = true
					if outputURI != req.OutputURI {
						t.Fatalf("unexpected outputURI: %s", outputURI)
					}
					if len(script.Lines) != 1 || script.Lines[0].Text != "generated script" {
						t.Fatalf("unexpected lines: %+v", script.Lines)
					}
					return "https://example.com/script.json", nil
				},
				RunFunc: func(_ context.Context, _ string, _ domain.Script) (string, error) {
					t.Fatal("generate なのに音声合成が呼ばれた")
					return "", nil
				},
			},
			&MockNotifier{
				NotifyFunc: func(_ context.Context, got domain.Request, publicURL string) error {
					notifyCalled = true
					if !reflect.DeepEqual(got, req) {
						t.Fatalf("unexpected request: %+v", got)
					}
					if publicURL != "https://example.com/script.json" {
						t.Fatalf("unexpected publicURL: %s", publicURL)
					}
					return nil
				},
			},
			&MockScriptStore{},
			0,
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
			&MockScriptStep{
				RunFunc: func(_ context.Context, _ domain.Request) (domain.Script, error) {
					return domain.Script{}, expectedErr
				},
			},
			&MockPublishStep{},
			&MockNotifier{
				NotifyFailureFunc: func(_ context.Context, got domain.Request, err error) error {
					failureNotified = true
					if !reflect.DeepEqual(got, req) {
						t.Fatalf("unexpected request: %+v", got)
					}
					if !errors.Is(err, expectedErr) {
						t.Fatalf("unexpected error: %v", err)
					}
					return nil
				},
			},
			&MockScriptStore{},
			0,
		)

		err := p.Execute(ctx, req)
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
		if !failureNotified {
			t.Fatal("failure notifier was not called")
		}
	})

	t.Run("異常系: 空スクリプト生成時は失敗通知を送ってエラーを返すこと", func(t *testing.T) {
		t.Parallel()

		failureNotified := false

		p := NewPipeline(
			&MockScriptStep{
				RunFunc: func(_ context.Context, _ domain.Request) (domain.Script, error) {
					return domain.Script{}, nil
				},
			},
			&MockPublishStep{},
			&MockNotifier{
				NotifyFailureFunc: func(_ context.Context, _ domain.Request, _ error) error {
					failureNotified = true
					return nil
				},
			},
			&MockScriptStore{},
			0,
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
			&MockScriptStep{
				RunFunc: func(_ context.Context, _ domain.Request) (domain.Script, error) {
					return domain.Script{Title: "テスト台本", Lines: sampleLines}, nil
				},
			},
			&MockPublishStep{
				PublishScriptFunc: func(_ context.Context, _ string, _ domain.Script) (string, error) {
					return "", expectedErr
				},
			},
			&MockNotifier{
				NotifyFailureFunc: func(_ context.Context, _ domain.Request, err error) error {
					failureNotified = true
					if !errors.Is(err, expectedErr) {
						t.Fatalf("unexpected error: %v", err)
					}
					return nil
				},
			},
			&MockScriptStore{},
			0,
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

// synthesize は台本を生成しません。渡された台本がそのまま公開処理へ渡り、
// Gemini を呼ぶ経路には一切入らないことを固定します。
func TestPipelineExecute_Synthesize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	script := []domain.ScriptLine{
		{Speaker: "めたん", Style: "ノーマル", Text: "手で直した台本"},
	}
	req := domain.Request{
		Command:   domain.CommandSynthesize,
		OutputURI: "gs://bucket/output.wav",
		Script:    script,
	}

	generateCalled := false
	var published []domain.ScriptLine

	p := NewPipeline(
		&MockScriptStep{
			RunFunc: func(_ context.Context, _ domain.Request) (domain.Script, error) {
				generateCalled = true
				return domain.Script{}, errors.New("generator must not be called")
			},
		},
		&MockPublishStep{
			RunFunc: func(_ context.Context, _ string, script domain.Script) (string, error) {
				published = script.Lines
				return "", nil
			},
		},
		&MockNotifier{},
		&MockScriptStore{},
		0,
	)

	if err := p.Execute(ctx, req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if generateCalled {
		t.Fatal("synthesize なのに台本生成が呼ばれた")
	}
	if !reflect.DeepEqual(published, script) {
		t.Fatalf("渡した台本が公開処理へ届いていない: %+v", published)
	}
}

// 揃っていない入力は何度実行しても同じように失敗するため、
// 外部を1つも叩かずに弾き、失敗通知だけを出します。
func TestPipelineExecute_InvalidRequest(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		req  domain.Request
	}{
		{
			name: "command 未指定",
			req:  domain.Request{OutputURI: "gs://bucket/o.wav", InputURI: "https://example.com"},
		},
		{
			name: "未知の command",
			req:  domain.Request{Command: "compose", OutputURI: "gs://bucket/o.wav"},
		},
		{
			name: "generate なのに入力ソースが無い",
			req:  domain.Request{Command: domain.CommandGenerate, OutputURI: "gs://bucket/o.wav"},
		},
		{
			name: "synthesize なのに台本が無い",
			req:  domain.Request{Command: domain.CommandSynthesize, OutputURI: "gs://bucket/o.wav"},
		},
		{
			name: "出力先が無い",
			req:  domain.Request{Command: domain.CommandGenerate, InputURI: "https://example.com"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			touched := false
			failureNotified := false
			p := NewPipeline(
				&MockScriptStep{
					RunFunc: func(_ context.Context, _ domain.Request) (domain.Script, error) {
						touched = true
						return domain.Script{}, nil
					},
				},
				&MockPublishStep{
					RunFunc: func(_ context.Context, _ string, _ domain.Script) (string, error) {
						touched = true
						return "", nil
					},
				},
				&MockNotifier{
					NotifyFailureFunc: func(_ context.Context, _ domain.Request, _ error) error {
						failureNotified = true
						return nil
					},
				},
				&MockScriptStore{},
				0,
			)

			if err := p.Execute(context.Background(), tt.req); err == nil {
				t.Fatal("不正なリクエストが素通りした")
			}
			if touched {
				t.Fatal("検証前に外部処理が呼ばれた")
			}
			if !failureNotified {
				t.Fatal("失敗通知が呼ばれていない")
			}
		})
	}
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
				NotifySkippedFunc: func(_ context.Context, got domain.Request, gotReason error) error {
					called = true
					if !reflect.DeepEqual(got, req) {
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

// 上限を超えたら打ち切り、**失敗通知はその外側で送ります。**
//
// 通知に打ち切り済みの ctx を渡すと通知自体が送れず、「Cloud Tasks より先に諦めて
// 失敗を知らせる」という三段の目的が達成できません。ここはその退行を見ています。
func TestPipelineExecute_TimesOutAndStillNotifies(t *testing.T) {
	t.Parallel()

	notified := make(chan context.Context, 1)

	p := NewPipeline(
		&MockScriptStep{
			RunFunc: func(ctx context.Context, _ domain.Request) (domain.Script, error) {
				// 上限を超えるまで待ちます。実際の合成が長引いた状態の代わりです。
				<-ctx.Done()
				return domain.Script{}, ctx.Err()
			},
		},
		&MockPublishStep{},
		&MockNotifier{
			NotifyFailureFunc: func(ctx context.Context, _ domain.Request, _ error) error {
				notified <- ctx
				return nil
			},
		},
		&MockScriptStore{},
		20*time.Millisecond,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command:   domain.CommandGenerate,
		InputURI:  "gs://bucket/in.txt",
		OutputURI: "gs://bucket/out.wav",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Execute() = %v, want context.DeadlineExceeded", err)
	}

	select {
	case gotCtx := <-notified:
		if gotCtx.Err() != nil {
			t.Fatalf("通知に渡った ctx が既にキャンセルされている: %v", gotCtx.Err())
		}
	default:
		t.Fatal("失敗通知が呼ばれていない")
	}
}
