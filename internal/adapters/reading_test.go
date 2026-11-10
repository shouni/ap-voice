package adapters

import (
	"strings"
	"testing"
)

// TestConvertToReadingShowsNonObviousReadings verifies that readings you would only learn by
// listening are visible beforehand.
//
// Readings are not obvious. The music generation service has preview_lyrics_reading for the same
// reason, and the 「水面」 its description cites becomes スイメン here too.
// Noticing after synthesis wastes the script's whole length in synthesis time.
func TestConvertToReadingShowsNonObviousReadings(t *testing.T) {
	t.Parallel()

	a := NewReadingAdapter()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "reads suimen, not minamo", text: "水面に映る月", want: "スイメンニウツルツキ"},
		{name: "the particle ha is read as wa", text: "指示は大事故のもと", want: "シジワダイジコノモト"},
		{name: "katakana stays as is", text: "カタカナハソノママ", want: "カタカナハソノママ"},
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

// TestConvertToReadingLeavesLatinAlone verifies that Latin letters are left unconverted.
//
// It is why the prompts ask for abbreviations in katakana. The conversion does not take care of
// them, so unless the script writes them in katakana they reach VOICEVOX as Latin letters.
// If this behaviour changes, the prompts' instructions need revisiting too.
func TestConvertToReadingLeavesLatinAlone(t *testing.T) {
	t.Parallel()

	got, err := NewReadingAdapter().ConvertToReading("APIとCloud Runの自動化")
	if err != nil {
		t.Fatalf("ConvertToReading() error = %v", err)
	}
	if !strings.Contains(got, "API") || !strings.Contains(got, "Cloud Run") {
		t.Errorf("Latin letters were converted: %q", got)
	}
	// The Japanese part becomes a reading.
	if !strings.Contains(got, "ジドウカ") {
		t.Errorf("Japanese was not converted: %q", got)
	}
}

// TestReadingAdapterLoadsDictionaryOnce verifies that the dictionary is loaded exactly once.
//
// kagome's dictionary is about 90MB and the web side has 512Mi of memory. Reloading it on every
// call would visibly slow the response just to check a reading.
func TestReadingAdapterLoadsDictionaryOnce(t *testing.T) {
	t.Parallel()

	a := NewReadingAdapter()

	first, err := a.ConvertToReading("最初の行")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := a.ConvertToReading("最初の行")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Errorf("same input gave different results: %q and %q", first, second)
	}
	if a.preview == nil {
		t.Error("the converter is not retained")
	}
}

// TestConvertToReadingReadsDigits verifies that Arabic numerals are converted into readings.
//
// They are not converted by default. phonetic passes digits through, so VOICEVOX reads them
// literally (8日→ハチニチ, 1人→イチニン, 20歳→ニジュッサイ).
// Scripts contain dates and head counts, so readingOptions passing WithNumberReading
// is the breakwater. Dropping it breaks things quietly — the preview shows the digits as is too,
// so it is noticed only when listening to the synthesised audio.
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
				t.Errorf("ConvertToReading(%q) = %q, Arabic numerals remain", text, got)
			}
		})
	}
}

// TestReadingOptionsMatchSynthesis verifies that the reading preview and synthesis use the same
// settings.
//
// ReadingAdapter claims to be "the same conversion go-voicevox runs just before synthesis".
// The preview used to take phonetic.Option and synthesis voicevox.Option, different types, and a
// mistake added to only one stayed hidden until the preview was compared with the actual audio. Now
// the single []voicevox.Option readingOptions returns goes to both NewVoiceAdapter (synthesis) and
// NewReadingPreview (preview). This pins that readingOptions includes number reading.
// NewVoiceAdapter needs a VOICEVOX engine, so it cannot be called
// here.
func TestReadingOptionsMatchSynthesis(t *testing.T) {
	t.Parallel()

	if len(readingOptions()) == 0 {
		t.Fatal("readingOptions is empty; a setting may have been added to the synthesis side only")
	}

	got, err := NewReadingAdapter().ConvertToReading("8日")
	if err != nil {
		t.Fatalf("ConvertToReading() error = %v", err)
	}
	if strings.Contains(got, "8") {
		t.Errorf("preview = %q; synthesis passes WithNumberReading, so they disagree", got)
	}
}
