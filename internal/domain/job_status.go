package domain

import "github.com/shouni/gcp-kit/jobstatus"

// JobStatus is the job's progress plus where ap-voice's artifacts are.
//
// gcp-kit's Status does not hold artifact locations. Their shape differs per service (a single URI,
// an output directory, several URIs), so by convention the using side defines the embedding type.
// Embedding flattens both in Firestore and in JSON, so the document and the response JSON
// have the same shape.
//
// Do not drop the firestore tags. Without them the stored field names become the Go identifiers
// (AudioURI), which disagree with the existing responses built from the json tags.
//
// Without this, it could only say "done". The state alone does not say where the audio is,
// and the enqueuer cannot reach the artifacts.
type JobStatus struct {
	jobstatus.Status
	// AudioURI is where the audio is stored (gs://). Set only for jobs whose synthesis completed.
	//
	// Signed URLs do not go here. They expire in an hour and cost computation to issue, so they
	// do not suit being returned on a 30-second poll. When a playable link is needed, a separate
	// endpoint issues one each time.
	AudioURI string `json:"audio_uri,omitempty" firestore:"audio_uri,omitempty"`
	// ScriptURI is where the script is stored (gs://). Set from generate onwards.
	ScriptURI string `json:"script_uri,omitempty" firestore:"script_uri,omitempty"`
	// InputURI is the input source the script was made from. Set only for generate-type jobs.
	//
	// Without it a failed job cannot be retried from the page. What was read is left in neither
	// the script nor the artifacts, and a failed job has no script at all. Once the submitting page
	// is closed, the original URL exists only in the user's memory.
	InputURI string `json:"input_uri,omitempty" firestore:"input_uri,omitempty"`
	// AIModel is the model that wrote the script. Empty means the default at enqueue time.
	//
	// It is kept so a retry uses the same model. The default model is the head of GEMINI_MODELS
	// and may change with every deploy, so "empty = the default at the time" cannot be
	// restored later.
	AIModel string `json:"ai_model,omitempty" firestore:"ai_model,omitempty"`
	// Mode is the format the script was made in (tech_solo and so on).
	//
	// Unless it is kept in the state, there is no way to look back. A finished script reveals only
	// the combination of speakers; a script for ずんだもん alone cannot tell tech_solo from
	// tech_howto. Revisiting length and tone targets against measurements needs to know which
	// mode made what.
	Mode string `json:"mode,omitempty" firestore:"mode,omitempty"`
}

// CarryFrom carries over from the previous record the values this construction cannot know.
//
// The worker rebuilds the state from the task at every state change, so without this the
// previous location disappears the moment running is written. The mode is the same —
// synthesize is a command that assumes the script already exists, so it carries no mode,
// and without carrying it over only jobs that went generate → synthesize in two stages lose their mode
// (jobs made from the page always take this path).
//
// Values already filled are not overwritten. What this construction learned is newer, and
// if re-synthesis moved the location it must not be reverted to the old value.
func (s *JobStatus) CarryFrom(prev *JobStatus) {
	if prev == nil {
		return
	}
	if s.AudioURI == "" {
		s.AudioURI = prev.AudioURI
	}
	if s.ScriptURI == "" {
		s.ScriptURI = prev.ScriptURI
	}
	if s.Mode == "" {
		s.Mode = prev.Mode
	}
	// The same goes for the input source and the model. synthesize carries neither, so
	// without carrying them over a job that went in two stages loses what it needs for a retry.
	if s.InputURI == "" {
		s.InputURI = prev.InputURI
	}
	if s.AIModel == "" {
		s.AIModel = prev.AIModel
	}
}

// NewJobStatus builds this record's state from the request.
//
// Both the web side (queued) and the worker side (running / succeeded / failed) write the record,
// and both rebuild it from the request every time. With two constructions, adding a value to keep
// would keep it on one side only, and the value would vanish just then. Paired with CarryFrom,
// this is the single definition of "what the request tells us".
func NewJobStatus(req Request, state jobstatus.State) JobStatus {
	return JobStatus{
		JobID:    req.JobID,
		Command:  string(req.Command),
		State:    state,
		Mode:     req.Mode,
		InputURI: req.InputURI,
		AIModel:  req.AIModel,
	}
}
