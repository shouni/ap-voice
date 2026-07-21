package domain

// ScriptLine は、AIが生成する構造化ナレーションの1発言分を表すドメインモデルです。
// Gemini の ResponseSchema によって形が強制された JSON からデコードされます。
type ScriptLine struct {
	// Speaker は話者名です（例: "ずんだもん"）。角括弧は付けません。
	Speaker string `json:"speaker"`
	// Style はVOICEVOXのスタイル名です（例: "ノーマル"）。角括弧は付けません。
	Style string `json:"style"`
	// Direction は任意の演出用感情タグです（例: "呼びかけ"）。
	Direction string `json:"direction,omitempty"`
	// Text は合成対象のテキストです。
	Text string `json:"text"`
}
