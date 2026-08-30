package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/shouni/go-job-firestore/jobfirestore"

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
					if diff := cmp.Diff(req, got); diff != "" {
						t.Fatalf("unexpected request (-want +got):\n%s", diff)
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
					if diff := cmp.Diff(req, got); diff != "" {
						t.Fatalf("unexpected request (-want +got):\n%s", diff)
					}
					if publicURL != "https://example.com/script.json" {
						t.Fatalf("unexpected publicURL: %s", publicURL)
					}
					return nil
				},
			},
			&MockScriptStore{},
			nil,
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
					if diff := cmp.Diff(req, got); diff != "" {
						t.Fatalf("unexpected request (-want +got):\n%s", diff)
					}
					if !errors.Is(err, expectedErr) {
						t.Fatalf("unexpected error: %v", err)
					}
					return nil
				},
			},
			&MockScriptStore{},
			nil,
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
			nil,
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
			nil,
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
		nil,
		0,
	)

	if err := p.Execute(ctx, req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if generateCalled {
		t.Fatal("synthesize なのに台本生成が呼ばれた")
	}
	if diff := cmp.Diff(script, published); diff != "" {
		t.Fatalf("渡した台本が公開処理へ届いていない (-want +got):\n%s", diff)
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
				nil,
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

}

// 上限を超えたら打ち切り、**失敗通知はその外側で送ります。**
//
// 通知に打ち切り済みの ctx を渡すと通知自体が送れず、「Cloud Tasks より先に諦めて
// 失敗を知らせる」という三段の目的が達成できません。ここはその退行を見ています。
func TestPipelineExecute_TimesOutAndStillNotifies(t *testing.T) {
	t.Parallel()

	type failure struct {
		ctx context.Context
		err error
	}
	notified := make(chan failure, 1)

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
			NotifyFailureFunc: func(ctx context.Context, _ domain.Request, err error) error {
				notified <- failure{ctx: ctx, err: err}
				return nil
			},
		},
		&MockScriptStore{},
		nil,
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
	case got := <-notified:
		if got.ctx.Err() != nil {
			t.Fatalf("通知に渡った ctx が既にキャンセルされている: %v", got.ctx.Err())
		}
		// **通知側が打ち切りだと判別できること。** ここが切れていると、
		// SlackAdapter は時間切れを普通の失敗として出してしまいます。
		// 途中の 1 箇所でも %w を %v に変えると落ちます。
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Errorf("通知に渡ったエラーから打ち切りを判別できません: %v", got.err)
		}
	default:
		t.Fatal("失敗通知が呼ばれていない")
	}
}

// memoryStatusStore は、最後に書かれた状態を覚えておくフェイクです。
type memoryStatusStore struct {
	saved domain.JobStatus
	found bool
	// getErr は読み取りの失敗（権限・障害）を再現します。
	getErr error
}

func (s *memoryStatusStore) Get(context.Context, string) (domain.JobStatus, error) {
	if s.getErr != nil {
		return domain.JobStatus{}, s.getErr
	}
	if !s.found {
		// 本物の Store は未記録を ErrNotFound で返します。ここを素のエラーにすると、
		// 「未記録」と「読めない」を分けて扱う側（再実行ガード）の検証がすり抜けます。
		return domain.JobStatus{}, fmt.Errorf("%w: 記録がありません", jobfirestore.ErrNotFound)
	}
	return s.saved, nil
}

func (s *memoryStatusStore) Save(_ context.Context, _ string, status domain.JobStatus) error {
	s.saved, s.found = status, true
	return nil
}

// TestPipelineRecordsArtifactLocations は、完了した状態に成果物の在り処が
// 載ることを検証します。
//
// **「できた」だけでは投入した側が成果物へ辿り着けません。** 音声は publish が
// 作ったときだけ入り、generate は台本までなので入りません。ここを分けないと、
// 台本しか無いジョブに音声の URI を書くことになります。
func TestPipelineRecordsArtifactLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      domain.Command
		wantAudioURI string
	}{
		{
			name:         "generate は台本まで",
			command:      domain.CommandGenerate,
			wantAudioURI: "",
		},
		{
			name:         "まとめて作ると音声も入る",
			command:      domain.CommandGenerateAndSynthesize,
			wantAudioURI: "gs://bucket/voice/job-1/audio.wav",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &memoryStatusStore{}
			p := NewPipeline(
				&MockScriptStep{
					RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
						return domain.Script{
							Title: "題名",
							Lines: []domain.ScriptLine{{Speaker: "ずんだもん", Style: "ノーマル", Text: "本文"}},
						}, nil
					},
				},
				&MockPublishStep{},
				&MockNotifier{},
				&MockScriptStore{},
				jobfirestore.NewRecorder[domain.JobStatus](store),
				0,
			)

			err := p.Execute(context.Background(), domain.Request{
				Command:   tt.command,
				JobID:     "job-1",
				InputURI:  "gs://bucket/in.txt",
				OutputURI: "gs://bucket/voice/job-1/audio.wav",
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if store.saved.State != jobfirestore.StateSucceeded {
				t.Fatalf("State = %q, want succeeded", store.saved.State)
			}
			if store.saved.AudioURI != tt.wantAudioURI {
				t.Errorf("AudioURI = %q, want %q", store.saved.AudioURI, tt.wantAudioURI)
			}
			// 台本はどちらの経路でも保存されます。
			if store.saved.ScriptURI != "gs://bucket/voice/job-1/audio.json" {
				t.Errorf("ScriptURI = %q", store.saved.ScriptURI)
			}
			if store.saved.Title != "題名" {
				t.Errorf("Title = %q", store.saved.Title)
			}
		})
	}
}

// TestPipelineKeepsArtifactsOnFailure は、失敗しても前回の成果物の在り処が
// 残ることを検証します。
//
// ワーカーは毎回タスクから状態を組み立て直すため、引き継ぎが無いと
// 合成の失敗で台本の在り処まで消え、詳細画面からやり直せなくなります。
func TestPipelineKeepsArtifactsOnFailure(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			ScriptURI: "gs://bucket/voice/job-1/audio.json",
			AudioURI:  "gs://bucket/voice/job-1/audio.wav",
		},
	}

	p := NewPipeline(
		&MockScriptStep{
			RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
				return domain.Script{}, errors.New("生成に失敗しました")
			},
		},
		&MockPublishStep{},
		&MockNotifier{},
		&MockScriptStore{},
		jobfirestore.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerate, JobID: "job-1",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err == nil {
		t.Fatal("失敗するはずが成功しています")
	}

	if store.saved.State != jobfirestore.StateFailed {
		t.Errorf("State = %q, want failed", store.saved.State)
	}
	if store.saved.ScriptURI == "" || store.saved.AudioURI == "" {
		t.Errorf("失敗で成果物の在り処が消えています: %+v", store.saved)
	}
}

// 完了済みのジョブが再び届いても、合成をやり直さないこと。
//
// Cloud Tasks は at-least-once 配信なので、同じタスクが再び届くことがあります。
// 合成は数分かかるため、素通りさせると同じ音声を作り直します。
func TestPipelineExecute_SkipsAlreadySucceededJob(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			Status:   jobfirestore.Status{JobID: "job-1", State: jobfirestore.StateSucceeded},
			AudioURI: "gs://bucket/voice/job-1/audio.wav",
		},
	}

	var generated, published, notified int
	p := NewPipeline(
		&MockScriptStep{RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
			generated++
			return domain.Script{Lines: []domain.ScriptLine{{Text: "本文"}}}, nil
		}},
		&MockPublishStep{RunFunc: func(context.Context, string, domain.Script) (string, error) {
			published++
			return "", nil
		}},
		&MockNotifier{NotifyFunc: func(context.Context, domain.Request, string) error {
			notified++
			return nil
		}},
		&MockScriptStore{},
		jobfirestore.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerateAndSynthesize, JobID: "job-1",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil（完了済みは正常に打ち切る）", err)
	}

	if generated != 0 || published != 0 {
		t.Errorf("完了済みなのに実行している: generate=%d publish=%d", generated, published)
	}
	if notified != 0 {
		t.Errorf("完了済みなのに再通知している: %d 回", notified)
	}
	if store.saved.State != jobfirestore.StateSucceeded {
		t.Errorf("State = %q, want succeeded（記録を書き換えない）", store.saved.State)
	}
}

// 作り直しの投入は打ち切らないこと。
//
// 投入経路はどれも enqueue の前に queued を書くため、同じジョブ ID への 2 度目の
// 依頼は succeeded ではなく queued として届きます。ここで打ち切ると、台本を直して
// から合成し直す経路が動かなくなります。
func TestPipelineExecute_RunsResubmittedJob(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			// 直前の generate は succeeded だったが、投入時に queued へ戻っている。
			Status:    jobfirestore.Status{JobID: "job-1", State: jobfirestore.StateQueued},
			ScriptURI: "gs://bucket/voice/job-1/audio.json",
		},
	}

	var published int
	p := NewPipeline(
		&MockScriptStep{},
		&MockPublishStep{RunFunc: func(context.Context, string, domain.Script) (string, error) {
			published++
			return "", nil
		}},
		&MockNotifier{},
		&MockScriptStore{LoadFunc: func(context.Context, string) (domain.Script, error) {
			return domain.Script{Title: "題名", Lines: []domain.ScriptLine{{Text: "本文"}}}, nil
		}},
		jobfirestore.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandSynthesize, JobID: "job-1",
		OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if published != 1 {
		t.Errorf("合成した回数 = %d, want 1（作り直しは打ち切らない）", published)
	}
}

// 状態を読めないときは、判断を返して記録を書き換えないこと。
//
// ここで failed を書くと、succeeded だったかもしれない記録を上書きし、次の配信で
// ガードが効かなくなります。防ぐはずの再実行をガード自身が招くことになります。
func TestPipelineExecute_UnreadableStatusDoesNotOverwrite(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found:  true,
		saved:  domain.JobStatus{Status: jobfirestore.Status{JobID: "job-1", State: jobfirestore.StateSucceeded}},
		getErr: fmt.Errorf("%w: storage down", jobfirestore.ErrUnavailable),
	}

	var generated int
	var notifiedFailure int
	p := NewPipeline(
		&MockScriptStep{RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
			generated++
			return domain.Script{Lines: []domain.ScriptLine{{Text: "本文"}}}, nil
		}},
		&MockPublishStep{},
		&MockNotifier{NotifyFailureFunc: func(context.Context, domain.Request, error) error {
			notifiedFailure++
			return nil
		}},
		&MockScriptStore{},
		jobfirestore.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerate, JobID: "job-1",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if !errors.Is(err, jobfirestore.ErrUnavailable) {
		t.Fatalf("Execute() error = %v, want wrapping ErrUnavailable", err)
	}

	if generated != 0 {
		t.Errorf("判断できないのに実行している: %d 回", generated)
	}
	if store.saved.State != jobfirestore.StateSucceeded {
		t.Errorf("State = %q, want succeeded（読めないだけで記録を潰さない）", store.saved.State)
	}
	if notifiedFailure != 0 {
		t.Errorf("判断を保留しただけで失敗通知を出している: %d 回", notifiedFailure)
	}
}

// TestPipelineKeepsModeOnSynthesize は、二段で進めたジョブがモードを失わないことを
// 検証します。
//
// **synthesize はモードを持ちません。** 台本がすでにある前提のコマンドだからです。
// 画面から作ると必ず generate → synthesize と分かれるため、引き継ぎが無いと
// 「画面から作ったジョブだけモードが空」という、記録として一番使えない形になります。
func TestPipelineKeepsModeOnSynthesize(t *testing.T) {
	t.Parallel()

	// generate が書き、投入時に queued へ戻された状態を再現します。
	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			Status:    jobfirestore.Status{JobID: "job-1", State: jobfirestore.StateQueued},
			Mode:      "tech_duet",
			ScriptURI: "gs://bucket/voice/job-1/audio.json",
		},
	}

	p := NewPipeline(
		&MockScriptStep{},
		&MockPublishStep{},
		&MockNotifier{},
		&MockScriptStore{
			LoadFunc: func(context.Context, string) (domain.Script, error) {
				return domain.Script{Title: "題名", Lines: []domain.ScriptLine{{Speaker: "ずんだもん", Text: "本文"}}}, nil
			},
		},
		jobfirestore.NewRecorder[domain.JobStatus](store),
		0,
	)

	// 合成のリクエストにモードは載りません。
	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandSynthesize, JobID: "job-1",
		OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if store.saved.State != jobfirestore.StateSucceeded {
		t.Fatalf("State = %q, want succeeded", store.saved.State)
	}
	if store.saved.Mode != "tech_duet" {
		t.Errorf("Mode = %q, want tech_duet（合成で失われています）", store.saved.Mode)
	}
}

// TestPipelineRecordsMode は、生成のときにモードが状態へ載ることを検証します。
func TestPipelineRecordsMode(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{}
	p := NewPipeline(
		&MockScriptStep{
			RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
				return domain.Script{Title: "題名", Lines: []domain.ScriptLine{{Speaker: "ずんだもん", Text: "本文"}}}, nil
			},
		},
		&MockPublishStep{},
		&MockNotifier{},
		&MockScriptStore{},
		jobfirestore.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerate, JobID: "job-1", Mode: "tech_dialogue",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if store.saved.Mode != "tech_dialogue" {
		t.Errorf("Mode = %q, want tech_dialogue", store.saved.Mode)
	}
}
