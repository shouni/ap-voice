package domain

import (
	"testing"

	"github.com/shouni/go-job-firestore/jobfirestore"
)

// TestCarryFromKeepsWhatARebuildCannotKnow は、後から組み立て直しても分からない
// 値が引き継がれることを検証します。
//
// 記録は状態が変わるたびにリクエストから組み立て直されます。synthesize は
// モードも入力ソースもモデルも持たない（台本が既にある前提のコマンドです）ので、
// 引き継がないと generate → synthesize と 2 段で進めたジョブだけが、
// 「何をどう作ったか」を running を書いた時点で失います。
func TestCarryFromKeepsWhatARebuildCannotKnow(t *testing.T) {
	t.Parallel()

	prev := &JobStatus{
		AudioURI:  "gs://bucket/voice/job-1/audio.wav",
		ScriptURI: "gs://bucket/voice/job-1/audio.json",
		Mode:      "tech_solo",
		InputURI:  "https://example.com/article",
		AIModel:   "gemini-test",
	}

	// synthesize の組み立てです。どれも持ちません。
	next := NewJobStatus(Request{
		Command:   CommandSynthesize,
		JobID:     "job-1",
		OutputURI: "gs://bucket/voice/job-1/audio.wav",
	}, jobfirestore.StateRunning)
	next.CarryFrom(prev)

	if next.Mode != prev.Mode {
		t.Errorf("Mode = %q, want %q", next.Mode, prev.Mode)
	}
	if next.InputURI != prev.InputURI {
		t.Errorf("InputURI = %q, 作り直しの手がかりが消えます", next.InputURI)
	}
	if next.AIModel != prev.AIModel {
		t.Errorf("AIModel = %q, want %q", next.AIModel, prev.AIModel)
	}
	if next.AudioURI != prev.AudioURI || next.ScriptURI != prev.ScriptURI {
		t.Errorf("成果物の在り処が消えました: %+v", next)
	}
}

// TestCarryFromDoesNotOverwriteWhatIsKnownNow は、今回分かった値が古い記録で
// 上書きされないことを検証します。
//
// 合成し直しで在り処が変わったのに前回の値へ戻されては困ります。
func TestCarryFromDoesNotOverwriteWhatIsKnownNow(t *testing.T) {
	t.Parallel()

	next := NewJobStatus(Request{
		Command:  CommandGenerate,
		JobID:    "job-1",
		InputURI: "https://example.com/new",
		Mode:     "news_anchor",
		AIModel:  "gemini-new",
	}, jobfirestore.StateRunning)
	next.CarryFrom(&JobStatus{
		Mode:     "tech_solo",
		InputURI: "https://example.com/old",
		AIModel:  "gemini-old",
	})

	if next.InputURI != "https://example.com/new" || next.Mode != "news_anchor" || next.AIModel != "gemini-new" {
		t.Errorf("今回の値が古い記録で上書きされています: %+v", next)
	}
}

// TestCarryFromTolerAtesNoPreviousRecord は、前回の記録が無くても落ちないことを
// 検証します。最初の投入がこれです。
func TestCarryFromToleratesNoPreviousRecord(t *testing.T) {
	t.Parallel()

	next := NewJobStatus(Request{Command: CommandGenerate, JobID: "job-1"}, jobfirestore.StateQueued)
	next.CarryFrom(nil)

	if next.JobID != "job-1" || next.State != jobfirestore.StateQueued {
		t.Errorf("記録が壊れました: %+v", next)
	}
}
