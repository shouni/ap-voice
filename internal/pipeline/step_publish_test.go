package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shouni/ap-voice/internal/domain"
)

type mockVoice struct {
	uploadWavFunc    func(ctx context.Context, outputURI string, lines []domain.ScriptLine) error
	uploadScriptFunc func(ctx context.Context, outputURI string, script domain.Script) error
}

func (m *mockVoice) UploadWav(ctx context.Context, outputURI string, lines []domain.ScriptLine) error {
	return m.uploadWavFunc(ctx, outputURI, lines)
}

func (m *mockVoice) UploadScript(ctx context.Context, outputURI string, script domain.Script) error {
	return m.uploadScriptFunc(ctx, outputURI, script)
}

type mockURLSigner struct {
	generateSignedURLFunc func(ctx context.Context, path string, method string, expires time.Duration) (string, error)
}

func (m *mockURLSigner) SignURL(ctx context.Context, path string, method string, expires time.Duration) (string, error) {
	return m.generateSignedURLFunc(ctx, path, method, expires)
}

var _ Signer = (*mockURLSigner)(nil)

func TestPublishStepRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	outputURI := "gs://bucket/audio.wav"
	lines := []domain.ScriptLine{
		{Speaker: "ずんだもん", Style: "ノーマル", Text: "generated script"},
	}

	t.Run("正常系: 音声とスクリプトを書き込み署名付きURLを返すこと", func(t *testing.T) {
		t.Parallel()

		wavCalled := false
		scriptCalled := false
		signerCalled := false

		runner := NewPublishStep(
			&mockVoice{
				uploadWavFunc: func(_ context.Context, gotURI string, gotLines []domain.ScriptLine) error {
					wavCalled = true
					if gotURI != outputURI || len(gotLines) != len(lines) {
						t.Fatalf("unexpected wav args: %s %+v", gotURI, gotLines)
					}
					return nil
				},
				uploadScriptFunc: func(_ context.Context, gotURI string, gotScript domain.Script) error {
					scriptCalled = true
					if gotURI != outputURI || len(gotScript.Lines) != len(lines) {
						t.Fatalf("unexpected script args: %s %+v", gotURI, gotScript)
					}
					return nil
				},
			},
			&mockURLSigner{
				generateSignedURLFunc: func(_ context.Context, path string, method string, expires time.Duration) (string, error) {
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

		got, err := runner.Run(ctx, outputURI, domain.Script{Lines: lines})
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

		runner := NewPublishStep(
			&mockVoice{
				uploadWavFunc:    func(_ context.Context, _ string, _ []domain.ScriptLine) error { return nil },
				uploadScriptFunc: func(_ context.Context, _ string, _ domain.Script) error { return nil },
			},
			nil,
		)

		got, err := runner.Run(ctx, outputURI, domain.Script{Lines: lines})
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty url, got %s", got)
		}
	})

	t.Run("正常系: signer エラー時も公開成功としてURLなしで返すこと", func(t *testing.T) {
		t.Parallel()

		runner := NewPublishStep(
			&mockVoice{
				uploadWavFunc:    func(_ context.Context, _ string, _ []domain.ScriptLine) error { return nil },
				uploadScriptFunc: func(_ context.Context, _ string, _ domain.Script) error { return nil },
			},
			&mockURLSigner{
				generateSignedURLFunc: func(_ context.Context, _ string, _ string, _ time.Duration) (string, error) {
					return "", errors.New("sign failed")
				},
			},
		)

		got, err := runner.Run(ctx, outputURI, domain.Script{Lines: lines})
		if err != nil {
			t.Fatalf("Run() failed: %v", err)
		}
		if got != "" {
			t.Fatalf("expected empty url, got %s", got)
		}
	})

	t.Run("異常系: outputURI が空ならエラーになること", func(t *testing.T) {
		t.Parallel()

		runner := NewPublishStep(
			&mockVoice{
				uploadWavFunc:    func(_ context.Context, _ string, _ []domain.ScriptLine) error { return nil },
				uploadScriptFunc: func(_ context.Context, _ string, _ domain.Script) error { return nil },
			},
			nil,
		)

		_, err := runner.Run(ctx, "", domain.Script{Lines: lines})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("異常系: UploadWav 失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("wav failed")
		runner := NewPublishStep(
			&mockVoice{
				uploadWavFunc:    func(_ context.Context, _ string, _ []domain.ScriptLine) error { return expectedErr },
				uploadScriptFunc: func(_ context.Context, _ string, _ domain.Script) error { return nil },
			},
			nil,
		)

		_, err := runner.Run(ctx, outputURI, domain.Script{Lines: lines})
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("異常系: UploadScript 失敗を返すこと", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("script failed")
		runner := NewPublishStep(
			&mockVoice{
				uploadWavFunc:    func(_ context.Context, _ string, _ []domain.ScriptLine) error { return nil },
				uploadScriptFunc: func(_ context.Context, _ string, _ domain.Script) error { return expectedErr },
			},
			nil,
		)

		_, err := runner.Run(ctx, outputURI, domain.Script{Lines: lines})
		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected error %v, got %v", expectedErr, err)
		}
	})
}

// TestPublishStepRunSavesScriptBeforeAudio は、台本を音声より先に保存することを
// 検証します。
//
// **台本と音声をまとめて作る経路では、生成した台本はまだどこにも無い状態です。**
// 音声を先に作ると、合成が上限に達した時点で Gemini の生成結果ごと失われ、
// もう一度生成し直すしかなくなります。先に保存しておけば、詳細画面から
// 合成だけをやり直せます。
func TestPublishStepRunSavesScriptBeforeAudio(t *testing.T) {
	t.Parallel()

	var order []string
	voice := &mockVoice{
		uploadScriptFunc: func(_ context.Context, _ string, _ domain.Script) error {
			order = append(order, "script")
			return nil
		},
		uploadWavFunc: func(_ context.Context, _ string, _ []domain.ScriptLine) error {
			order = append(order, "wav")
			return errors.New("合成が打ち切られました")
		},
	}

	step := NewPublishStep(voice, nil)
	_, err := step.Run(context.Background(), "gs://bucket/voice/job/audio.wav", domain.Script{
		Title: "題名",
		Lines: []domain.ScriptLine{{Speaker: "ずんだもん", Style: "ノーマル", Text: "本文"}},
	})
	if err == nil {
		t.Fatal("合成が失敗したのにエラーになりません")
	}

	if len(order) == 0 || order[0] != "script" {
		t.Fatalf("保存の順序 = %v, want 台本が先", order)
	}
	// 合成が落ちても、台本は既に保存されています。
	if len(order) != 2 {
		t.Errorf("順序 = %v, want [script wav]", order)
	}
}
