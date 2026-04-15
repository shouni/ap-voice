package builder

import (
	"testing"

	"ap-voice/internal/config"
)

func TestRequiresGCS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{
			name: "nil config",
			cfg:  nil,
			want: false,
		},
		{
			name: "web input and local output",
			cfg: &config.Config{
				InputFile:  "https://example.com/article",
				OutputFile: "out/audio.wav",
			},
			want: false,
		},
		{
			name: "gcs input",
			cfg: &config.Config{
				InputFile:  "gs://bucket/input.txt",
				OutputFile: "out/audio.wav",
			},
			want: true,
		},
		{
			name: "gcs output",
			cfg: &config.Config{
				InputFile:  "https://example.com/article",
				OutputFile: "gs://bucket/audio.wav",
			},
			want: true,
		},
		{
			name: "trimmed gcs uri",
			cfg: &config.Config{
				InputFile: "  gs://bucket/input.txt  ",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := requiresGCS(tt.cfg); got != tt.want {
				t.Fatalf("requiresGCS() = %v, want %v", got, tt.want)
			}
		})
	}
}
