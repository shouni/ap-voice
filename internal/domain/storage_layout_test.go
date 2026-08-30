package domain

import "testing"

// TestScriptPathAndScriptURIForAgree は、台本の場所を導く規則が
// 1 つしかないことを検証します。
//
// 書き込み側（VoiceAdapter）は音声の URI から導き、読み出し側（repository）は
// ジョブ ID から導きます。かつては別々に拡張子を組み替えていたため、
// 片方だけ変えると「保存はできるが誰も読まない場所」になり得ました。
func TestScriptPathAndScriptURIForAgree(t *testing.T) {
	t.Parallel()

	l := NewStorageLayout()
	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"

	// 読み出し側の導き方
	fromJobID := l.ScriptPath(jobID)
	// 書き込み側の導き方
	fromAudio := l.ScriptURIFor(l.AudioPath(jobID))

	if fromJobID != fromAudio {
		t.Errorf("台本の場所が一致しません: %q vs %q", fromJobID, fromAudio)
	}
	if fromJobID != "voice/"+jobID+"/audio.json" {
		t.Errorf("ScriptPath = %q", fromJobID)
	}

	// 完全な URI でも同じであること。
	bucket := "ap-voice"
	if got, want := l.ScriptURIFor(l.AudioURI(bucket, jobID)), l.ScriptURI(bucket, jobID); got != want {
		t.Errorf("URI が一致しません: %q vs %q", got, want)
	}
}
