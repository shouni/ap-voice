package domain

import (
	"fmt"
	"path"
	"strings"
)

// scriptExt は台本 JSON の拡張子です。音声と同じ名前で拡張子だけ違えます。
const scriptExt = ".json"

// StorageLayout は、成果物のオブジェクト名を1箇所に集めます。
//
// 成果物はジョブ ID ごとのプレフィックス配下へまとめます。バケット直下に散らばっていると、
// 削除のたびに「このジョブが何を作ったか」を人が数え上げることになります。
// プレフィックスにまとめておけば、消す側は中身を知らないまま一覧して消せます。
type StorageLayout struct{}

// NewStorageLayout は StorageLayout を構築します。
func NewStorageLayout() StorageLayout {
	return StorageLayout{}
}

// VoicePrefix は、音声ジョブが並ぶオブジェクトプレフィックスを返します。
func (l StorageLayout) VoicePrefix() string {
	return "voice/"
}

// VoiceJobPrefix は、1 つのジョブの成果物を収めるプレフィックスを返します。
func (l StorageLayout) VoiceJobPrefix(jobID string) string {
	return fmt.Sprintf("%s%s/", l.VoicePrefix(), jobID)
}

// AudioPath は、WAV の相対オブジェクトパスを返します。
func (l StorageLayout) AudioPath(jobID string) string {
	return l.VoiceJobPrefix(jobID) + "audio.wav"
}

// ScriptPath は、台本 JSON の相対オブジェクトパスを返します。
//
// 台本は**成果物であると同時に入力**です。generate はこれだけを書き、
// synthesize はこれを読んで音声を作ります。
func (l StorageLayout) ScriptPath(jobID string) string {
	return l.ScriptURIFor(l.AudioPath(jobID))
}

// ScriptURIFor は、音声の出力先から台本の出力先を導きます。
//
// **導出の規則をここ 1 箇所に置きます。** 台本の書き込み側は音声の URI しか
// 受け取らないため、拡張子を .json に替える計算がかつてアダプター側にもありました。
// 同じ規則が 2 箇所にあると、片方だけ変えたときに「保存はできるが誰も読まない場所」
// という気付きにくい壊れ方をします。
func (l StorageLayout) ScriptURIFor(audioURI string) string {
	return strings.TrimSuffix(audioURI, path.Ext(audioURI)) + scriptExt
}

// AudioURI は、WAV の完全な出力先 URI を返します。
func (l StorageLayout) AudioURI(bucket, jobID string) string {
	return l.uri(bucket, l.AudioPath(jobID))
}

// ScriptURI は、台本 JSON の完全な出力先 URI を返します。
func (l StorageLayout) ScriptURI(bucket, jobID string) string {
	return l.uri(bucket, l.ScriptPath(jobID))
}

func (l StorageLayout) uri(bucket, path string) string {
	return fmt.Sprintf("gs://%s/%s", bucket, path)
}
