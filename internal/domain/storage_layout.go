package domain

import (
	"fmt"
	"path"
	"strings"
)

// scriptExt is the script JSON's extension. It shares the audio's name and differs only in extension.
const scriptExt = ".json"

// StorageLayout gathers the artifacts' object names in one place.
//
// Artifacts are grouped under a prefix per job ID. Scattered at the bucket root, every deletion
// would require a person to enumerate "what this job produced".
// Grouped under a prefix, the deleting side can list and delete without knowing the contents.
type StorageLayout struct{}

// NewStorageLayout builds a StorageLayout.
func NewStorageLayout() StorageLayout {
	return StorageLayout{}
}

// VoicePrefix returns the object prefix under which voice jobs are listed.
func (l StorageLayout) VoicePrefix() string {
	return "voice/"
}

// VoiceJobPrefix returns the prefix that holds one job's artifacts.
func (l StorageLayout) VoiceJobPrefix(jobID string) string {
	return fmt.Sprintf("%s%s/", l.VoicePrefix(), jobID)
}

// AudioPath returns the WAV's relative object path.
func (l StorageLayout) AudioPath(jobID string) string {
	return l.VoiceJobPrefix(jobID) + "audio.wav"
}

// ScriptPath returns the script JSON's relative object path.
//
// The script is both an artifact and an input. generate writes only this, and
// synthesize reads it to produce the audio.
func (l StorageLayout) ScriptPath(jobID string) string {
	return l.ScriptURIFor(l.AudioPath(jobID))
}

// ScriptURIFor derives the script's output location from the audio's.
//
// The derivation rule lives here alone. The script writer receives only the audio URI,
// so the adapter once had its own computation that swapped the extension for .json.
// With the same rule in two places, changing only one breaks things in a hard-to-notice way:
// "a place that can be written but that nobody reads".
func (l StorageLayout) ScriptURIFor(audioURI string) string {
	return strings.TrimSuffix(audioURI, path.Ext(audioURI)) + scriptExt
}

// AudioURI returns the WAV's full output URI.
func (l StorageLayout) AudioURI(bucket, jobID string) string {
	return l.uri(bucket, l.AudioPath(jobID))
}

// ScriptURI returns the script JSON's full output URI.
func (l StorageLayout) ScriptURI(bucket, jobID string) string {
	return l.uri(bucket, l.ScriptPath(jobID))
}

func (l StorageLayout) uri(bucket, path string) string {
	return fmt.Sprintf("gs://%s/%s", bucket, path)
}
