package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/domain"
)

// LoadScriptStep loads the stored script by JobID (synthesize).
//
// The script does not ride in the payload (see domain.Request.JobID). A supplied script is also
// stored by the enqueuing side first, so there is one read path. There used to be a branch that
// preferred a script in the payload, but once no enqueuer put one there any more, it was a path
// only the tests took.
type LoadScriptStep struct {
	scripts domain.ScriptStore
}

// NewLoadScriptStep creates a LoadScriptStep.
func NewLoadScriptStep(scripts domain.ScriptStore) *LoadScriptStep {
	return &LoadScriptStep{scripts: scripts}
}

// Name is the step name.
func (*LoadScriptStep) Name() string { return "load_script" }

// Execute loads the script and puts it on sc.Script.
func (s *LoadScriptStep) Execute(ctx context.Context, sc *Context) error {
	jobID := sc.Request.JobID
	script, err := s.scripts.Load(ctx, jobID)
	if err != nil {
		err = fmt.Errorf("failed to load the stored script (%s): %w", jobID, err)
		// A missing script and an unreadable one are different. synthesize is enqueued only after
		// the script is stored, so one that arrives without it will not appear by waiting. The unreadable
		// case (a transient GCS failure) may be fixed by redelivery, so it is returned as is.
		if errors.Is(err, domain.ErrScriptNotFound) {
			return worker.Permanent(err)
		}
		return err
	}
	if len(script.Lines) == 0 {
		// An empty script will not be filled in later.
		return worker.Permanent(fmt.Errorf("stored script is empty (%s)", jobID))
	}
	sc.Script = script
	return nil
}
