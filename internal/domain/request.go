package domain

// Request はパイプライン実行に必要な入力パラメータを保持するモデルです。
type Request struct {
	InputURI  string `json:"input_uri"`
	OutputURI string `json:"output_uri"`
	Mode      string `json:"mode"`
	AIModel   string `json:"ai_model"`
}
