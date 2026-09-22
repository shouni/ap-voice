package adapters

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shouni/go-voicevox/voicevox"
)

// ReadingAdapter は、合成前にテキストがどう読まれるかを返します。
//
// go-voicevox が合成の直前に通すのと同じ変換です（voicevox.NewReadingPreview）。
// 読みは自明ではなく、「田中」「同姓同名」のような語は聴くまで確かめられません。
// 合成してから気付くと、台本ぶんの合成時間がそのまま無駄になります。
type ReadingAdapter struct {
	// once は辞書の読み込みを遅らせます。kagome の辞書は約 90MB あり、
	// web 面のメモリは 512Mi です。読みを確かめない利用者にまで常時
	// 積ませる理由はないので、最初に使われたときだけ読み込みます。
	// 読み込みは 1 度きりで、以降のリクエストは待ちません。
	once    sync.Once
	preview *voicevox.ReadingPreview
	err     error
}

// NewReadingAdapter は ReadingAdapter を構築します。ここでは辞書を読みません。
func NewReadingAdapter() *ReadingAdapter {
	return &ReadingAdapter{}
}

// ConvertToReading は、テキストを読み（カタカナ）へ変換します。
//
// 合成と同じ 200 文字の切れ目で分けてから変換するので、長い行でも合成と同じ読みになります。
// 表示は 1 行にまとめます。
func (a *ReadingAdapter) ConvertToReading(text string) (string, error) {
	a.once.Do(func() {
		// 合成側（voicevox.New）と同じ Option を渡します。ここが食い違うと、
		// 確認した読みと実際に合成される読みが別物になり、確認の意味が無くなります。
		a.preview, a.err = voicevox.NewReadingPreview(readingOptions()...)
	})
	if a.err != nil {
		return "", fmt.Errorf("読み変換の初期化に失敗しました: %w", a.err)
	}
	return strings.Join(a.preview.Read(text), ""), nil
}

// readingOptions は、読み変換に関わる voicevox.Option です。
//
// 合成側（NewVoiceAdapter）と読みプレビューで同じものを渡すための 1 か所です。
// 型が同じなので、片方にだけ足す書き方ができません。
func readingOptions() []voicevox.Option {
	return []voicevox.Option{voicevox.WithNumberReading()}
}
