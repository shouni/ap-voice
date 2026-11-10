package domain

import (
	"testing"

	"github.com/shouni/gcp-kit/jobstatus"
)

// TestCarryFromKeepsWhatARebuildCannotKnow verifies that values a later rebuild cannot know
// are carried over.
//
// The record is rebuilt from the request at every state change. synthesize carries no mode,
// input source or model (it is a command that assumes the script already exists), so without
// carrying them over, only jobs that went generate → synthesize in two stages would lose
// "what was made and how" the moment running is written.
func TestCarryFromKeepsWhatARebuildCannotKnow(t *testing.T) {
	t.Parallel()

	prev := &JobStatus{
		AudioURI:  "gs://bucket/voice/job-1/audio.wav",
		ScriptURI: "gs://bucket/voice/job-1/audio.json",
		Mode:      "tech_solo",
		InputURI:  "https://example.com/article",
		AIModel:   "gemini-test",
	}

	// The synthesize construction. It carries none of them.
	next := NewJobStatus(Request{
		Command:   CommandSynthesize,
		JobID:     "job-1",
		OutputURI: "gs://bucket/voice/job-1/audio.wav",
	}, jobstatus.StateRunning)
	next.CarryFrom(prev)

	if next.Mode != prev.Mode {
		t.Errorf("Mode = %q, want %q", next.Mode, prev.Mode)
	}
	if next.InputURI != prev.InputURI {
		t.Errorf("InputURI = %q, the handle for remaking it is lost", next.InputURI)
	}
	if next.AIModel != prev.AIModel {
		t.Errorf("AIModel = %q, want %q", next.AIModel, prev.AIModel)
	}
	if next.AudioURI != prev.AudioURI || next.ScriptURI != prev.ScriptURI {
		t.Errorf("the location of the artifacts was lost: %+v", next)
	}
}

// TestCarryFromDoesNotOverwriteWhatIsKnownNow verifies that values learned this time are not
// overwritten by the old record.
//
// If re-synthesis moved the location, it must not be put back to the previous value.
func TestCarryFromDoesNotOverwriteWhatIsKnownNow(t *testing.T) {
	t.Parallel()

	next := NewJobStatus(Request{
		Command:  CommandGenerate,
		JobID:    "job-1",
		InputURI: "https://example.com/new",
		Mode:     "news_anchor",
		AIModel:  "gemini-new",
	}, jobstatus.StateRunning)
	next.CarryFrom(&JobStatus{
		Mode:     "tech_solo",
		InputURI: "https://example.com/old",
		AIModel:  "gemini-old",
	})

	if next.InputURI != "https://example.com/new" || next.Mode != "news_anchor" || next.AIModel != "gemini-new" {
		t.Errorf("this run's values were overwritten by the old record: %+v", next)
	}
}

// TestCarryFromTolerAtesNoPreviousRecord verifies that it does not fail without a previous
// record. The first enqueue is exactly that case.
func TestCarryFromToleratesNoPreviousRecord(t *testing.T) {
	t.Parallel()

	next := NewJobStatus(Request{Command: CommandGenerate, JobID: "job-1"}, jobstatus.StateQueued)
	next.CarryFrom(nil)

	if next.JobID != "job-1" || next.State != jobstatus.StateQueued {
		t.Errorf("the record was broken: %+v", next)
	}
}
