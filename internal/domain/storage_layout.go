package domain

import "fmt"

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
//
// 台本はこのパスの拡張子を .json に替えた位置へ PublishRunner が書きます。
// 隣に置くのは、貼り戻して synthesize に渡す入力でもあるためです。
func (l StorageLayout) AudioPath(jobID string) string {
	return l.VoiceJobPrefix(jobID) + "audio.wav"
}

// AudioURI は、WAV の完全な出力先 URI を返します。
func (l StorageLayout) AudioURI(bucket, jobID string) string {
	return fmt.Sprintf("gs://%s/%s", bucket, l.AudioPath(jobID))
}
