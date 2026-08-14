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
