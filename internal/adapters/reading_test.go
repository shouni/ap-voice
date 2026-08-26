package adapters

import (
	"strings"
	"testing"
)

// TestConvertToReadingShowsNonObviousReadings は、聴くまで分からない読みが
// 事前に見えることを検証します。
//
// **読みは自明ではありません。** ap-comp が同じ理由で preview_lyrics_reading を
// 持っており、その説明が挙げている「水面」はここでも スイメン になります。
// 合成してから気付くと、台本の長さぶんの合成時間がそのまま無駄になります。
func TestConvertToReadingShowsNonObviousReadings(t *testing.T) {
	t.Parallel()

	a := NewReadingAdapter()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "水面はミナモではない", text: "水面に映る月", want: "スイメンニウツルツキ"},
		{name: "助詞のはが発音どおりになる", text: "指示は大事故のもと", want: "シジワダイジコノモト"},
		{name: "カタカナはそのまま", text: "カタカナハソノママ", want: "カタカナハソノママ"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := a.ConvertToReading(tt.text)
			if err != nil {
				t.Fatalf("ConvertToReading() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ConvertToReading(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestConvertToReadingLeavesLatinAlone は、アルファベットが変換されずに
// そのまま残ることを検証します。
//
// **プロンプトが「略語はカタカナで書く」と求めている根拠です。** 変換が面倒を
// 見てくれないため、台本の側でカタカナにしておかないと VOICEVOX へ英字のまま
// 渡ります。この振る舞いが変わったら、プロンプトの指示も見直す必要があります。
func TestConvertToReadingLeavesLatinAlone(t *testing.T) {
	t.Parallel()

	got, err := NewReadingAdapter().ConvertToReading("APIとCloud Runの自動化")
	if err != nil {
		t.Fatalf("ConvertToReading() error = %v", err)
	}
	if !strings.Contains(got, "API") || !strings.Contains(got, "Cloud Run") {
		t.Errorf("英字が変換されています: %q", got)
	}
	// 日本語の部分は読みになります。
	if !strings.Contains(got, "ジドウカ") {
		t.Errorf("日本語が変換されていません: %q", got)
	}
}

// TestReadingAdapterLoadsDictionaryOnce は、辞書の読み込みが 1 度きりで
// あることを検証します。
//
// kagome の辞書は約 90MB あり、web 面のメモリは 512Mi です。呼ばれるたびに
// 読み直すと、読みを確かめるだけで応答が目に見えて遅くなります。
func TestReadingAdapterLoadsDictionaryOnce(t *testing.T) {
	t.Parallel()

	a := NewReadingAdapter()

	first, err := a.ConvertToReading("最初の行")
	if err != nil {
		t.Fatalf("1 回目: %v", err)
	}
	second, err := a.ConvertToReading("最初の行")
	if err != nil {
		t.Fatalf("2 回目: %v", err)
	}
	if first != second {
		t.Errorf("同じ入力で結果が違います: %q と %q", first, second)
	}
	if a.converter == nil {
		t.Error("変換器が保持されていません")
	}
}

// TestConvertToReadingReadsDigits は、算用数字が読みへ変換されることを検証します。
//
// **既定では変換されません。** phonetic は数字をそのまま通すため、VOICEVOX が
// 字面どおりに読みます（8日→ハチニチ、1人→イチニン、20歳→ニジュッサイ）。
// 台本には日付も人数も出るので、readingOptions が WithNumberReading を渡している
// ことがその防波堤です。落としても**静かに壊れます** — プレビューにも数字がそのまま
// 並ぶので、気づけるのは合成した音声を聴いたときだけです。
func TestConvertToReadingReadsDigits(t *testing.T) {
	t.Parallel()

	a := NewReadingAdapter()
	for _, text := range []string{"8日", "1人", "20歳", "2026年8月"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			got, err := a.ConvertToReading(text)
			if err != nil {
				t.Fatalf("ConvertToReading(%q) error = %v", text, err)
			}
			if strings.ContainsAny(got, "0123456789") {
				t.Errorf("ConvertToReading(%q) = %q, 算用数字が残っています", text, got)
			}
		})
	}
}

// TestReadingOptionsMatchSynthesis は、読みプレビューと合成が同じ設定を使うことを
// 検証します。
//
// ReadingAdapter は「go-voicevox が合成の直前に通すのと同じ変換」を名乗っています。
// 設定の宛先は違い（プレビューは phonetic.Option、合成は voicevox.Option）、
// 型が違うので取り違えてもコンパイルは通ります。**片方だけに足した誤りは、
// プレビューと実際の音声を突き合わせるまで表に出ません。**
//
// 対応は readingOptions と NewVoiceAdapter の 2 か所にあります。ここでは
// readingOptions が空でないことと、それが数字読みを含むことを固定します。
// NewVoiceAdapter 側は VOICEVOX エンジンが要るのでここでは呼べません。
func TestReadingOptionsMatchSynthesis(t *testing.T) {
	t.Parallel()

	if len(readingOptions()) == 0 {
		t.Fatal("readingOptions が空です。合成側にだけ設定が足された可能性があります")
	}

	got, err := NewReadingAdapter().ConvertToReading("8日")
	if err != nil {
		t.Fatalf("ConvertToReading() error = %v", err)
	}
	if strings.Contains(got, "8") {
		t.Errorf("プレビュー = %q。合成側は WithNumberReading を渡しているので食い違います", got)
	}
}
