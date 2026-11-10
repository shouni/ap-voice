package domain

// ScriptLine is the domain model for one utterance of the structured narration the AI generates.
// It is decoded from JSON whose shape is enforced by Gemini's ResponseSchema.
type ScriptLine struct {
	// Speaker is the speaker name (e.g. "ずんだもん"), without square brackets.
	Speaker string `json:"speaker"`
	// Style is the VOICEVOX style name (e.g. "ノーマル"), without square brackets.
	Style string `json:"style"`
	// Text is the text to synthesise.
	Text string `json:"text"`
}

// Script is the stored script.
//
// It is an object rather than an array so that it can carry a title. With only job IDs
// listed in the history, you cannot tell which was which without opening them. The title is
// produced by the same generation as the script, so it needs no extra API call.
//
// It is also in a shape that can be pasted straight back into `synthesize`.
type Script struct {
	// Title is the short title shown in listings.
	Title string `json:"title"`
	// Lines is the sequence of utterances.
	Lines []ScriptLine `json:"lines"`
}
