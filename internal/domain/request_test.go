package domain

import (
	"errors"
	"strings"
	"testing"
)

// TestRequestValidate verifies the input each command must have.
//
// Incomplete input fails the same way however many times it runs, so it is rejected before
// any external call. The web form, the API and the worker all decide this with the same function,
// so loosening it here loosens all three.
func TestRequestValidate(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	const output = "gs://bucket/voice/" + jobID + "/audio.wav"

	tests := []struct {
		name string
		req  Request
		// If wantErr is empty it must pass; otherwise the error must contain that text.
		wantErr string
		// unknown is whether it must be wrapped in ErrUnknownCommand.
		unknown bool
	}{
		{
			name: "generate needs an input source and an output",
			req:  Request{Command: CommandGenerate, JobID: jobID, InputURI: "https://example.com", OutputURI: output},
		},
		{
			name:    "generate without an input source",
			req:     Request{Command: CommandGenerate, JobID: jobID, OutputURI: output},
			wantErr: "入力ソース",
		},
		{
			name: "generate_and_synthesize needs an input source too",
			req:  Request{Command: CommandGenerateAndSynthesize, JobID: jobID, InputURI: "https://example.com", OutputURI: output},
		},
		{
			name:    "generate_and_synthesize without an input source",
			req:     Request{Command: CommandGenerateAndSynthesize, JobID: jobID, OutputURI: output},
			wantErr: "入力ソース",
		},
		{
			// The script does not ride in the payload. Its location is the job ID alone.
			name: "synthesize names the script by job ID",
			req:  Request{Command: CommandSynthesize, JobID: jobID, OutputURI: output},
		},
		{
			name:    "synthesize without the script's location",
			req:     Request{Command: CommandSynthesize, OutputURI: output},
			wantErr: "台本が特定できません",
		},
		{
			// A whitespace-only value does not count as "given". It comes from the form.
			name:    "whitespace-only job ID",
			req:     Request{Command: CommandSynthesize, JobID: "   ", OutputURI: output},
			wantErr: "台本が特定できません",
		},
		{
			name:    "no output",
			req:     Request{Command: CommandGenerate, JobID: jobID, InputURI: "https://example.com"},
			wantErr: "出力先",
		},
		{
			// Empty is not taken as generate. A supplied script would be silently discarded,
			// a mix-up that changes both the billing and the output.
			name:    "empty command",
			req:     Request{JobID: jobID, InputURI: "https://example.com", OutputURI: output},
			wantErr: "command が指定されていません",
			unknown: true,
		},
		{
			name:    "unknown command",
			req:     Request{Command: "compose", JobID: jobID, OutputURI: output},
			wantErr: `"compose"`,
			unknown: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.req.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want it to contain %q", err, tt.wantErr)
			}
			// An unknown command must be distinguishable by type. Telling it apart by text would
			// silently break the caller's branch the moment the text is edited.
			if got := errors.Is(err, ErrUnknownCommand); got != tt.unknown {
				t.Errorf("errors.Is(err, ErrUnknownCommand) = %v, want %v", got, tt.unknown)
			}
		})
	}
}

// TestRequestValidateNamesEveryCommand verifies that the error guides the caller to fix a
// mistaken command.
//
// Three values are accepted, and which they are appears only in the error text.
// Forget to add a new command to the guidance and callers will assume the value does not exist.
func TestRequestValidateNamesEveryCommand(t *testing.T) {
	t.Parallel()

	err := Request{OutputURI: "gs://bucket/o.wav"}.Validate()
	if err == nil {
		t.Fatal("no error although command is empty")
	}
	for _, command := range []Command{CommandGenerate, CommandSynthesize, CommandGenerateAndSynthesize} {
		if !strings.Contains(err.Error(), string(command)) {
			t.Errorf("the guidance lacks %q: %v", command, err)
		}
	}
}
