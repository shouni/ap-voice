package domain

import "testing"

// TestScriptPathAndScriptURIForAgree verifies that there is only one rule
// for deriving where the script lives.
//
// The writer (VoiceAdapter) derives it from the audio URI and the reader (repository)
// from the job ID. They used to rewrite the extension separately, so changing only one
// could produce "a place that can be written but that nobody reads".
func TestScriptPathAndScriptURIForAgree(t *testing.T) {
	t.Parallel()

	l := NewStorageLayout()
	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"

	// How the reader derives it
	fromJobID := l.ScriptPath(jobID)
	// How the writer derives it
	fromAudio := l.ScriptURIFor(l.AudioPath(jobID))

	if fromJobID != fromAudio {
		t.Errorf("script locations disagree: %q vs %q", fromJobID, fromAudio)
	}
	if fromJobID != "voice/"+jobID+"/audio.json" {
		t.Errorf("ScriptPath = %q", fromJobID)
	}

	// The same must hold for a full URI.
	bucket := "ap-voice"
	if got, want := l.ScriptURIFor(l.AudioURI(bucket, jobID)), l.ScriptURI(bucket, jobID); got != want {
		t.Errorf("URIs disagree: %q vs %q", got, want)
	}
}
