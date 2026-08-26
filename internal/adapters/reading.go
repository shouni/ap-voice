package adapters

import (
	"fmt"
	"sync"

	"github.com/shouni/audio/phonetic"
)

// ReadingAdapter は、合成前にテキストがどう読まれるかを返します。
//
// **go-voicevox が合成の直前に通すのと同じ変換です**（`phonetic.Converter`）。
// 読みは自明ではなく、「田中」「同姓同名」のような語は聴くまで確かめられません。
// 合成してから気付くと、台本ぶんの合成時間がそのまま無駄になります。
type ReadingAdapter struct {
	// once は辞書の読み込みを遅らせます。**kagome の辞書は約 90MB あり**、
	// web 面のメモリは 512Mi です。読みを確かめない利用者にまで常時
	// 積ませる理由はないので、最初に使われたときだけ読み込みます。
	// 読み込みは 1 度きりで、以降のリクエストは待ちません。
	once      sync.Once
	converter *phonetic.Converter
	err       error
}

// NewReadingAdapter は ReadingAdapter を構築します。**ここでは辞書を読みません。**
func NewReadingAdapter() *ReadingAdapter {
	return &ReadingAdapter{}
}

// ConvertToReading は、テキストを読み（カタカナ）へ変換します。
//
// go-voicevox は 200 文字を超える行を先に分割してから変換しますが、ここでは
// 行をそのまま変換します。プロンプトが 1 行 200 文字以内を求めているため
// 通常は同じ結果になり、超える行では分割の境目だけ差が出ることがあります。
func (a *ReadingAdapter) ConvertToReading(text string) (string, error) {
	a.once.Do(func() {
		// **合成側（voicevox.New）と同じ設定にします。** ここが食い違うと、
		// 確認した読みと実際に合成される読みが別物になり、確認の意味が無くなります。
		a.converter, a.err = phonetic.NewConverter(readingOptions()...)
	})
	if a.err != nil {
		return "", fmt.Errorf("読み変換の初期化に失敗しました: %w", a.err)
	}
	return a.converter.ConvertToReading(text), nil
}

// readingOptions は、読み変換器に与える設定です。
//
// **合成側と共有するための 1 か所です。** go-voicevox には voicevox.Option として
// 渡し（NewVoiceAdapter）、読みプレビューには phonetic.Option として渡します。
// 口が違うだけで中身は同じでなければならないので、対応をここに集めます。
func readingOptions() []phonetic.Option {
	return []phonetic.Option{phonetic.WithNumberReading()}
}
