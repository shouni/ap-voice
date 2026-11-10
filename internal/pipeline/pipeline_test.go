package pipeline

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/domain"
)

var _ Step = (*MockScriptStep)(nil)
var _ Step = (*MockPublishStep)(nil)
var _ domain.Notifier = (*MockNotifier)(nil)

// MockScriptStep replaces the script generation step. It puts the script RunFunc returns on as is.
type MockScriptStep struct {
	RunFunc func(ctx context.Context, req domain.Request) (domain.Script, error)
}

func (*MockScriptStep) Name() string { return "generate_script" }

func (m *MockScriptStep) Execute(ctx context.Context, sc *Context) error {
	script, err := m.Run(ctx, sc.Request)
	if err != nil {
		return err
	}
	sc.Script = script
	return nil
}

func (m *MockScriptStep) Run(ctx context.Context, req domain.Request) (domain.Script, error) {
	if m.RunFunc == nil {
		return domain.Script{}, nil
	}
	return m.RunFunc(ctx, req)
}

// MockPublishStep replaces the save and synthesis step. Like the real one, it routes generate to
// PublishScriptFunc and everything else to RunFunc.
type MockPublishStep struct {
	RunFunc           func(ctx context.Context, outputURI string, script domain.Script) (string, error)
	PublishScriptFunc func(ctx context.Context, outputURI string, script domain.Script) (string, error)
}

func (*MockPublishStep) Name() string { return "publish" }

func (m *MockPublishStep) Execute(ctx context.Context, sc *Context) error {
	var (
		publicURL string
		err       error
	)
	if sc.Request.Command == domain.CommandGenerate {
		publicURL, err = m.PublishScript(ctx, sc.Request.OutputURI, sc.Script)
	} else {
		publicURL, err = m.Run(ctx, sc.Request.OutputURI, sc.Script)
	}
	if err != nil {
		return err
	}
	sc.PublicURL = publicURL
	return nil
}

func (m *MockPublishStep) PublishScript(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if m.PublishScriptFunc == nil {
		return "", nil
	}
	return m.PublishScriptFunc(ctx, outputURI, script)
}

// MockScriptStore replaces reading stored scripts.
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

	t.Run("ok: generation, publishing and the success notification are called", func(t *testing.T) {
		t.Parallel()

		generateCalled := false
		publishCalled := false
		notifyCalled := false

		p := NewRunner(
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
				// generate stores only the script. Run (speech synthesis) must not be called.
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
					t.Fatal("speech synthesis was called for generate")
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

	t.Run("error: a generation failure sends a failure notification and returns an error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("generate failed")
		failureNotified := false

		p := NewRunner(
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

	t.Run("error: a publish failure sends a failure notification and returns an error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("publish failed")
		failureNotified := false

		p := NewRunner(
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

// synthesize does not generate a script. This pins that the stored script goes to publishing as is
// and that the path calling Gemini is never entered.
//
// The script is located by JobID alone. There used to be a branch that preferred a script in the
// payload, and this test went through it, but no enqueuer puts a script there
// (a long script hits Cloud Tasks' 1MB limit, so it is saved first and the ID is passed).
func TestPipelineExecute_Synthesize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	script := []domain.ScriptLine{
		{Speaker: "めたん", Style: "ノーマル", Text: "手で直した台本"},
	}
	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: "gs://bucket/output.wav",
	}

	generateCalled := false
	var published []domain.ScriptLine

	p := NewRunner(
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
		&MockScriptStore{
			LoadFunc: func(_ context.Context, gotJobID string) (domain.Script, error) {
				if gotJobID != jobID {
					t.Errorf("Load(%q), want %q", gotJobID, jobID)
				}
				return domain.Script{Title: "保存済みの題名", Lines: script}, nil
			},
		},
		nil,
		0,
	)

	if err := p.Execute(ctx, req); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}
	if generateCalled {
		t.Fatal("script generation was called for synthesize")
	}
	if diff := cmp.Diff(script, published); diff != "" {
		t.Fatalf("the given script did not reach publishing (-want +got):\n%s", diff)
	}
}

// Incomplete input fails the same way however many times it runs, so
// it is rejected without any external call, and only a failure notification goes out.
func TestPipelineExecute_InvalidRequest(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		req  domain.Request
	}{
		{
			name: "command not given",
			req:  domain.Request{OutputURI: "gs://bucket/o.wav", InputURI: "https://example.com"},
		},
		{
			name: "unknown command",
			req:  domain.Request{Command: "compose", OutputURI: "gs://bucket/o.wav"},
		},
		{
			name: "generate without an input source",
			req:  domain.Request{Command: domain.CommandGenerate, OutputURI: "gs://bucket/o.wav"},
		},
		{
			// The script is located by JobID alone. Without it, there is nothing to speak.
			name: "synthesize not pointing at a script",
			req:  domain.Request{Command: domain.CommandSynthesize, OutputURI: "gs://bucket/o.wav"},
		},
		{
			name: "no output",
			req:  domain.Request{Command: domain.CommandGenerate, InputURI: "https://example.com"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			touched := false
			failureNotified := false
			p := NewRunner(
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
				t.Fatal("an invalid request got through")
			}
			if touched {
				t.Fatal("external processing was called before validation")
			}
			if !failureNotified {
				t.Fatal("no failure notification was sent")
			}
		})
	}
}

func TestPipelineNotifications(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := domain.Request{OutputURI: "gs://bucket/output.wav"}

	t.Run("notifySuccess: does not panic when notifier is nil", func(t *testing.T) {
		t.Parallel()
		p := &Runner{}
		p.notifySuccess(ctx, req, "")
	})

}

// Past the limit the run is cut off, and the failure notification is sent outside it.
//
// Passing the cut-off ctx to the notification would keep it from being sent, defeating the three tiers'
// purpose of "giving up before Cloud Tasks and reporting the failure". This guards against that regression.
func TestPipelineExecute_TimesOutAndStillNotifies(t *testing.T) {
	t.Parallel()

	type failure struct {
		// ctxErr is the ctx's state when the notification is called. Lifecycle cuts Finish's ctx out
		// with a limit, so after Execute returns it is already cancelled; only by checking at call
		// time can we tell whether it was detached.
		ctxErr error
		err    error
	}
	notified := make(chan failure, 1)

	p := NewRunner(
		&MockScriptStep{
			RunFunc: func(ctx context.Context, _ domain.Request) (domain.Script, error) {
				// Wait until past the limit. It stands in for a synthesis that runs long.
				<-ctx.Done()
				return domain.Script{}, ctx.Err()
			},
		},
		&MockPublishStep{},
		&MockNotifier{
			NotifyFailureFunc: func(ctx context.Context, _ domain.Request, err error) error {
				notified <- failure{ctxErr: ctx.Err(), err: err}
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
		if got.ctxErr != nil {
			t.Fatalf("the ctx passed to the notification is already cancelled: %v", got.ctxErr)
		}
		// The notifier must be able to tell it was cut off. If this link breaks,
		// SlackAdapter reports the timeout as an ordinary failure.
		// Changing even one %w along the way to %v breaks it.
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Errorf("cannot tell a cutoff from the error passed to the notification: %v", got.err)
		}
	default:
		t.Fatal("no failure notification was sent")
	}
}

// memoryStatusStore is a fake that remembers the last state written.
type memoryStatusStore struct {
	saved domain.JobStatus
	found bool
	// getErr reproduces a read failure (permissions, outage).
	getErr error
}

func (s *memoryStatusStore) Get(context.Context, string) (domain.JobStatus, error) {
	if s.getErr != nil {
		return domain.JobStatus{}, s.getErr
	}
	if !s.found {
		// The real Store returns ErrNotFound for an unrecorded job. A bare error here would let checks
		// on the side that treats "unrecorded" and "unreadable" separately (the re-run guard) slip through.
		return domain.JobStatus{}, fmt.Errorf("%w: no record", jobstatus.ErrNotFound)
	}
	return s.saved, nil
}

func (s *memoryStatusStore) Save(_ context.Context, _ string, status domain.JobStatus) error {
	s.saved, s.found = status, true
	return nil
}

// TestPipelineRecordsArtifactLocations verifies that the completed state carries the artifact
// locations.
//
// "Done" alone does not let the enqueuer reach the artifacts. The audio is set only when publish
// made it; generate goes only as far as the script, so it is not set. Without the distinction,
// a job with only a script would get an audio URI.
func TestPipelineRecordsArtifactLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      domain.Command
		wantAudioURI string
	}{
		{
			name:         "generate stops at the script",
			command:      domain.CommandGenerate,
			wantAudioURI: "",
		},
		{
			name:         "making both sets the audio too",
			command:      domain.CommandGenerateAndSynthesize,
			wantAudioURI: "gs://bucket/voice/job-1/audio.wav",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &memoryStatusStore{}
			p := NewRunner(
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
				jobstatus.NewRecorder[domain.JobStatus](store),
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

			if store.saved.State != jobstatus.StateSucceeded {
				t.Fatalf("State = %q, want succeeded", store.saved.State)
			}
			if store.saved.AudioURI != tt.wantAudioURI {
				t.Errorf("AudioURI = %q, want %q", store.saved.AudioURI, tt.wantAudioURI)
			}
			// The script is stored on either path.
			if store.saved.ScriptURI != "gs://bucket/voice/job-1/audio.json" {
				t.Errorf("ScriptURI = %q", store.saved.ScriptURI)
			}
			if store.saved.Title != "題名" {
				t.Errorf("Title = %q", store.saved.Title)
			}
		})
	}
}

// TestPipelineKeepsArtifactsOnFailure verifies that the previous artifact locations remain after
// a failure.
//
// The worker rebuilds the state from the task every time, so without carrying them over
// a synthesis failure would erase even the script's location, and it could not be retried from the detail page.
func TestPipelineKeepsArtifactsOnFailure(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			ScriptURI: "gs://bucket/voice/job-1/audio.json",
			AudioURI:  "gs://bucket/voice/job-1/audio.wav",
		},
	}

	p := NewRunner(
		&MockScriptStep{
			RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
				return domain.Script{}, errors.New("generation failed")
			},
		},
		&MockPublishStep{},
		&MockNotifier{},
		&MockScriptStore{},
		jobstatus.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerate, JobID: "job-1",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err == nil {
		t.Fatal("succeeded although it should fail")
	}

	if store.saved.State != jobstatus.StateFailed {
		t.Errorf("State = %q, want failed", store.saved.State)
	}
	if store.saved.ScriptURI == "" || store.saved.AudioURI == "" {
		t.Errorf("the failure erased the artifact locations: %+v", store.saved)
	}
}

// A completed job that arrives again is not synthesised again.
//
// Cloud Tasks delivers at least once, so the same task may arrive again.
// Synthesis takes minutes, so letting it through would remake the same audio.
func TestPipelineExecute_SkipsAlreadySucceededJob(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			JobID:    "job-1",
			State:    jobstatus.StateSucceeded,
			AudioURI: "gs://bucket/voice/job-1/audio.wav",
		},
	}

	var generated, published, notified int
	p := NewRunner(
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
		jobstatus.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerateAndSynthesize, JobID: "job-1",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil (a completed job ends normally)", err)
	}

	if generated != 0 || published != 0 {
		t.Errorf("ran although complete: generate=%d publish=%d", generated, published)
	}
	if notified != 0 {
		t.Errorf("notified again although complete: %d times", notified)
	}
	if store.saved.State != jobstatus.StateSucceeded {
		t.Errorf("State = %q, want succeeded (the record is not rewritten)", store.saved.State)
	}
}

// A regeneration enqueue is not cut off.
//
// Every enqueue path writes queued before the enqueue, so a second request for the same
// job ID arrives as queued, not succeeded. Cutting it off here would break the path of fixing
// the script and then re-synthesising.
func TestPipelineExecute_RunsResubmittedJob(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			// The preceding generate was succeeded, but the enqueue put it back to queued.
			JobID:     "job-1",
			State:     jobstatus.StateQueued,
			ScriptURI: "gs://bucket/voice/job-1/audio.json",
		},
	}

	var published int
	p := NewRunner(
		&MockScriptStep{},
		&MockPublishStep{RunFunc: func(context.Context, string, domain.Script) (string, error) {
			published++
			return "", nil
		}},
		&MockNotifier{},
		&MockScriptStore{LoadFunc: func(context.Context, string) (domain.Script, error) {
			return domain.Script{Title: "題名", Lines: []domain.ScriptLine{{Text: "本文"}}}, nil
		}},
		jobstatus.NewRecorder[domain.JobStatus](store),
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
		t.Errorf("synthesis count = %d, want 1 (a regeneration is not cut off)", published)
	}
}

// When the state cannot be read, the decision is returned and the record is not rewritten.
//
// Writing failed here would overwrite a record that may have been succeeded, and on the next
// delivery the guard would not work. The guard itself would cause the re-run it exists to prevent.
func TestPipelineExecute_UnreadableStatusDoesNotOverwrite(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{
		found:  true,
		saved:  domain.JobStatus{JobID: "job-1", State: jobstatus.StateSucceeded},
		getErr: fmt.Errorf("%w: storage down", jobstatus.ErrUnavailable),
	}

	var generated int
	var notifiedFailure int
	p := NewRunner(
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
		jobstatus.NewRecorder[domain.JobStatus](store),
		0,
	)

	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandGenerate, JobID: "job-1",
		InputURI: "gs://bucket/in.txt", OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if !errors.Is(err, jobstatus.ErrUnavailable) {
		t.Fatalf("Execute() error = %v, want wrapping ErrUnavailable", err)
	}

	if generated != 0 {
		t.Errorf("ran although it cannot decide: %d times", generated)
	}
	if store.saved.State != jobstatus.StateSucceeded {
		t.Errorf("State = %q, want succeeded (merely unreadable must not clobber the record)", store.saved.State)
	}
	// The run is skipped, but not silently. If no queue retry comes, the job stays queued
	// unnoticed, so a notification without a record prompts a person to resubmit (worker conventions 2.1).
	if notifiedFailure != 1 {
		t.Errorf("did not notify that the state could not be read: %d times", notifiedFailure)
	}
}

// TestPipelineKeepsModeOnSynthesize verifies that a job that went in two stages does not lose
// its mode.
//
// synthesize has no mode, because it is a command that assumes the script already exists.
// Jobs made from the page always split into generate → synthesize, so without carrying it over
// "only the jobs made from the page have an empty mode", the least useful shape for a record.
func TestPipelineKeepsModeOnSynthesize(t *testing.T) {
	t.Parallel()

	// Reproduces the state generate wrote and the enqueue put back to queued.
	store := &memoryStatusStore{
		found: true,
		saved: domain.JobStatus{
			JobID:     "job-1",
			State:     jobstatus.StateQueued,
			Mode:      "tech_duet",
			ScriptURI: "gs://bucket/voice/job-1/audio.json",
		},
	}

	p := NewRunner(
		&MockScriptStep{},
		&MockPublishStep{},
		&MockNotifier{},
		&MockScriptStore{
			LoadFunc: func(context.Context, string) (domain.Script, error) {
				return domain.Script{Title: "題名", Lines: []domain.ScriptLine{{Speaker: "ずんだもん", Text: "本文"}}}, nil
			},
		},
		jobstatus.NewRecorder[domain.JobStatus](store),
		0,
	)

	// The synthesis request carries no mode.
	err := p.Execute(context.Background(), domain.Request{
		Command: domain.CommandSynthesize, JobID: "job-1",
		OutputURI: "gs://bucket/voice/job-1/audio.wav",
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if store.saved.State != jobstatus.StateSucceeded {
		t.Fatalf("State = %q, want succeeded", store.saved.State)
	}
	if store.saved.Mode != "tech_duet" {
		t.Errorf("Mode = %q, want tech_duet (lost in synthesis)", store.saved.Mode)
	}
}

// TestPipelineRecordsMode verifies that generation puts the mode on the state.
func TestPipelineRecordsMode(t *testing.T) {
	t.Parallel()

	store := &memoryStatusStore{}
	p := NewRunner(
		&MockScriptStep{
			RunFunc: func(context.Context, domain.Request) (domain.Script, error) {
				return domain.Script{Title: "題名", Lines: []domain.ScriptLine{{Speaker: "ずんだもん", Text: "本文"}}}, nil
			},
		},
		&MockPublishStep{},
		&MockNotifier{},
		&MockScriptStore{},
		jobstatus.NewRecorder[domain.JobStatus](store),
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

// TestExecuteClassifiesRetry pins how Execute classifies the failures it returns.
//
// A mix-up breaks quietly in either direction. Sending a permanent failure to retry delivers the
// same notification twice; treating a transient one as permanent cuts off, as if successful, a job
// that redelivery would have fixed, and it never runs again.
func TestExecuteClassifiesRetry(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		req           domain.Request
		loadErr       error
		loadScript    domain.Script
		wantPermanent bool
	}{
		"no output": {
			req:           domain.Request{JobID: "voice-20260830-090000-aaaaaaaaaaaa", Command: domain.CommandSynthesize},
			wantPermanent: true,
		},
		"invalid command": {
			req:           domain.Request{JobID: "voice-20260830-090000-aaaaaaaaaaaa", OutputURI: "gs://b/o.wav", Command: "nope"},
			wantPermanent: true,
		},
		"no script": {
			req:           domain.Request{JobID: "voice-20260830-090000-aaaaaaaaaaaa", OutputURI: "gs://b/o.wav", Command: domain.CommandSynthesize},
			loadErr:       fmt.Errorf("no script yet: %w", domain.ErrScriptNotFound),
			wantPermanent: true,
		},
		"empty script": {
			req:           domain.Request{JobID: "voice-20260830-090000-aaaaaaaaaaaa", OutputURI: "gs://b/o.wav", Command: domain.CommandSynthesize},
			loadScript:    domain.Script{},
			wantPermanent: true,
		},
		"script unreadable (transient failure)": {
			req:     domain.Request{JobID: "voice-20260830-090000-aaaaaaaaaaaa", OutputURI: "gs://b/o.wav", Command: domain.CommandSynthesize},
			loadErr: errors.New("googleapi: Error 503: backend error"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := &Runner{
				scripts: &MockScriptStore{
					LoadFunc: func(context.Context, string) (domain.Script, error) {
						return tt.loadScript, tt.loadErr
					},
				},
				notifier: &MockNotifier{},
			}
			err := p.Execute(context.Background(), tt.req)
			if err == nil {
				t.Fatal("Execute() error = nil, want error")
			}
			if permanent := errors.Is(err, worker.ErrPermanent); permanent != tt.wantPermanent {
				t.Errorf("permanent = %v, want %v (err=%v)", permanent, tt.wantPermanent, err)
			}
		})
	}
}
