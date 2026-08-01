package runner

import (
	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-voicevox/speaker"
)

// 許可される話者・スタイルは go-voicevox/speaker を単一の情報源とする（値の重複によるドリフトを防ぐ）。
// direction は動画演出用の独自タグでVOICEVOX側に対応物がないため、ここで定義する。
// 話者ごと・モードごとにどのスタイルを使うべきかという意味的な制約は、
// スキーマではなくプロンプト文章側の指示に委ねる。
var (
	allowedSpeakers   = speaker.SupportedSpeakerNames()
	allowedStyles     = speaker.SupportedStyleNames()
	allowedDirections = []string{
		"解説", "疑問", "驚き", "理解", "落ち着き", "納得", "断定", "呼びかけ",
	}
)

// scriptTextMaxLength は、Gemini に対する目安の文字数上限です。
// 実際の安全策は go-voicevox 側の SplitByCharLimit による強制分割です。
const scriptTextMaxLength = 200

// scriptResponseSchema は、ナレーションスクリプトを ScriptLine の配列として
// 受け取るための gemini.Schema を構築します。
func scriptResponseSchema() *gemini.Schema {
	maxLength := int64(scriptTextMaxLength)

	return &gemini.Schema{
		Type: gemini.TypeArray,
		Items: &gemini.Schema{
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
				"direction": {
					Type:        gemini.TypeString,
					Enum:        allowedDirections,
					Description: "任意の演出用感情タグ。合成音声には含まれない。",
				},
				"text": {
					Type:        gemini.TypeString,
					MaxLength:   &maxLength,
					Description: "合成対象のテキスト。句読点を含めて200文字を目安に収めること。",
				},
			},
			Required:         []string{"speaker", "style", "text"},
			PropertyOrdering: []string{"speaker", "style", "direction", "text"},
		},
	}
}
