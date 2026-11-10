package adapters

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shouni/go-voicevox/voicevox"
)

// ReadingAdapter returns how text will be read before it is synthesised.
//
// It is the same conversion go-voicevox runs just before synthesis (voicevox.NewReadingPreview).
// Readings are not obvious: words like 「田中」 or 「同姓同名」 cannot be checked until heard.
// Noticing after synthesis wastes the whole script's synthesis time.
type ReadingAdapter struct {
	// once defers loading the dictionary. kagome's dictionary is about 90MB and
	// the web side has 512Mi of memory. There is no reason to keep it loaded for users who never
	// check readings, so it is loaded only on first use.
	// It is loaded exactly once; later requests do not wait.
	once    sync.Once
	preview *voicevox.ReadingPreview
	err     error
}

// NewReadingAdapter builds a ReadingAdapter. It does not load the dictionary here.
func NewReadingAdapter() *ReadingAdapter {
	return &ReadingAdapter{}
}

// ConvertToReading converts text into its reading (katakana).
//
// It splits at the same 200-character boundaries as synthesis before converting, so even a long line reads as it will be synthesised.
// The result is joined into one line for display.
func (a *ReadingAdapter) ConvertToReading(text string) (string, error) {
	a.once.Do(func() {
		// Pass the same Options as the synthesis side (voicevox.New). If they differ,
		// the reading that was checked and the one that is synthesised diverge, and checking loses its point.
		a.preview, a.err = voicevox.NewReadingPreview(readingOptions()...)
	})
	if a.err != nil {
		return "", fmt.Errorf("failed to initialize reading conversion: %w", a.err)
	}
	return strings.Join(a.preview.Read(text), ""), nil
}

// readingOptions are the voicevox.Options that affect reading conversion.
//
// It is the one place that hands the same list to the synthesis side (NewVoiceAdapter) and the reading preview.
// They share a type, so adding to only one of them is not possible.
func readingOptions() []voicevox.Option {
	return []voicevox.Option{voicevox.WithNumberReading()}
}
