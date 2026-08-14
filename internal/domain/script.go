package domain

// ScriptLine は、AIが生成する構造化ナレーションの1発言分を表すドメインモデルです。
// Gemini の ResponseSchema によって形が強制された JSON からデコードされます。
type ScriptLine struct {
	// Speaker は話者名です（例: "ずんだもん"）。角括弧は付けません。
	Speaker string `json:"speaker"`
	// Style はVOICEVOXのスタイル名です（例: "ノーマル"）。角括弧は付けません。
	Style string `json:"style"`
	// Text は合成対象のテキストです。
	Text string `json:"text"`
}

// Script は保存される台本です。
//
// **タイトルを持たせるために配列ではなくオブジェクトにしています。** 履歴の一覧で
// ジョブ ID だけが並ぶと、どれが何だったか開くまで分かりません。タイトルは台本と
// 同じ生成で作らせるので、追加の API 呼び出しは要りません。
//
// このまま `synthesize` へ貼り戻せる形でもあります。
type Script struct {
	// Title は一覧に出す短い題名です。
	Title string `json:"title"`
	// Lines は発言の並びです。
	Lines []ScriptLine `json:"lines"`
}
