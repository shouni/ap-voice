package pipeline

import (
	"github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-voicevox/speaker"
)

// scriptTextMaxLength は、Gemini に対する目安の文字数上限です。
// 実際の安全策は go-voicevox 側の SplitByCharLimit による強制分割です。
const scriptTextMaxLength = 200

// scriptTitleMaxLength は、履歴の一覧に収まる長さの目安です。
const scriptTitleMaxLength = 60

// scriptResponseSchema は、ナレーションスクリプトを ScriptLine の配列として
// 受け取るための gemini.Schema を構築します。
//
// 許可語彙は assets/speakers.json（= エンジンの /speakers 応答）が単一の情報源です。
// speaker と style は独立した enum なので、**この形では「話者ごとに使えるスタイル」を
// 表現できません**。実在しない組み合わせを選ばれても getStyleID がその話者の既定へ落とすため、
// 合成は通りますが指示は無視されます。話者ごと・モードごとの制約はプロンプト文章側が担います
// （現在はどのモードも "ノーマル" 固定なので、この enum は実質そこまで絞られています）。
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

	// **配列ではなくオブジェクトを返させます。** タイトルは履歴の一覧に出すもので、
	// 台本と同じ生成で作らせれば追加の API 呼び出しが要りません。
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
