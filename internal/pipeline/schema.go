package pipeline

import (
	"github.com/shouni/genai-kit/gemini"

	"github.com/shouni/go-voicevox/speaker"
)

// scriptTextMaxLength is the rough character limit given to Gemini.
// The actual safeguard is go-voicevox's forced split at 200 characters (an internal detail, so its name is not relied on).
const scriptTextMaxLength = 200

// scriptTitleMaxLength is a rough length that fits in the history listing.
const scriptTitleMaxLength = 60

// scriptResponseSchema builds the gemini.Schema for receiving the narration script
// as an array of ScriptLine.
//
// The single source of the allowed vocabulary is assets/speakers.json (= the engine's /speakers response).
// speaker and style are independent enums, so this shape cannot express "the styles each speaker
// can use". If a non-existent pair is chosen, synthesis falls back to that speaker's default, so
// synthesis succeeds but the instruction is ignored. Per-speaker and per-mode constraints are carried by the prompt text
// (every mode currently pins "ノーマル", so in practice this enum is narrowed that far).
func scriptResponseSchema(speakers *speaker.Registry) *gemini.Schema {
	maxLength := int64(scriptTextMaxLength)
	titleMaxLength := int64(scriptTitleMaxLength)
	allowedSpeakers := speakers.SpeakerNames()
	allowedStyles := speakers.StyleNames()

	line := &gemini.Schema{
		Type: gemini.TypeObject,
		Properties: map[string]*gemini.Schema{
			"speaker": {
				Type:        gemini.TypeString,
				Enum:        allowedSpeakers,
				Description: "発言する話者名。",
			},
			"style": {
				Type:        gemini.TypeString,
				Enum:        allowedStyles,
				Description: "VOICEVOXのスタイル名。話者ごとに許可される組み合わせはプロンプトの指示に従うこと。",
			},
			"text": {
				Type:        gemini.TypeString,
				MaxLength:   &maxLength,
				Description: "合成対象のテキスト。句読点を含めて200文字を目安に収めること。",
			},
		},
		Required:         []string{"speaker", "style", "text"},
		PropertyOrdering: []string{"speaker", "style", "text"},
	}

	// It returns an object rather than an array. The title is shown in the history listing, and
	// producing it in the same generation as the script needs no extra API call.
	return &gemini.Schema{
		Type: gemini.TypeObject,
		Properties: map[string]*gemini.Schema{
			"title": {
				Type:        gemini.TypeString,
				MaxLength:   &titleMaxLength,
				Description: "この台本の内容を表す短い題名。履歴の一覧に表示する。",
			},
			"lines": {
				Type:        gemini.TypeArray,
				Items:       line,
				Description: "発言の並び。",
			},
		},
		Required:         []string{"title", "lines"},
		PropertyOrdering: []string{"title", "lines"},
	}
}
