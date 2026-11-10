package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Command says how far one request should go.
//
// Script generation and speech synthesis are separate entry points because the script is "both an
// artifact and an input". Wanting to fix a reading or a speaker and redo only the synthesis is common;
// with a single entry point every such fix would restart from Gemini's generation. That wastes cost and
// waiting time, and the output changes from last time, so more than "the one line to fix" becomes different.
type Command string

const (
	// CommandGenerate generates a script from an input source and stores it. It makes no audio,
	// so that the script can be checked and corrected before synthesis.
	CommandGenerate Command = "generate"
	// CommandSynthesize makes audio from a stored script. The script is named by JobID.
	// Gemini is not called.
	CommandSynthesize Command = "synthesize"
	// CommandGenerateAndSynthesize writes the script and goes straight on to the audio.
	//
	// It is the choice to skip the check. Now that scripts can be edited, the user decides whether
	// to put a check in between. For short modes (news, music promotion) where a fix is unlikely,
	// opening the history and pressing again costs more than it saves.
	//
	// It adds no branches. resolveScript treats "anything but synthesize" as generation, and
	// publish treats "anything but generate" as going on to audio, so this value falls on the
	// desired side of both.
	CommandGenerateAndSynthesize Command = "generate_and_synthesize"
)

// ErrUnknownCommand means an unknown Command was given.
var ErrUnknownCommand = errors.New("domain: unknown command")

// Request is the model holding the input parameters a pipeline run needs.
// It flows as the Cloud Tasks payload (JSON) as is, so its tags are the contract with the enqueuer.
type Request struct {
	// Command is what to run. It cannot be omitted.
	//
	// generate covers up to the script, synthesize up to the audio. They are split because the
	// script is both an artifact and an input. Allowing synthesis after fixing a reading or a speaker
	// means one line to fix does not require regenerating.
	//
	// Empty is not taken as generate because, if a caller that supplied a script forgot to write
	// command, the script would be silently discarded and a Gemini generation would run
	// (POST /jobs saves the JSON script before passing it here).
	// A mix-up that changes both the billing and the output is not worth absorbing with a default.
	Command Command `json:"command"`

	// JobID identifies one run. The artifacts' location is derived from it too.
	// The enqueuing side (web) issues it; the worker side only uses it in logs and notifications.
	//
	// For synthesize it is also where the script is. The script itself does not ride here —
	// a long script could hit Cloud Tasks' 1MB limit, so the enqueuer saves it first and passes
	// only the ID. A supplied script (the JSON script of POST /jobs, the page's
	// 「台本 JSON」 tab) takes the same path: it is saved, then named by this ID.
	JobID string `json:"job_id,omitempty"`

	// InputURI is generate's input source (a web URL / gs://).
	InputURI string `json:"input_uri,omitempty"`
	// OutputURI is where the WAV goes. The script sits next to it as a .json of the same name.
	OutputURI string `json:"output_uri"`
	// Mode is generate's script format (solo / dialogue / duet).
	Mode string `json:"mode,omitempty"`
	// AIModel is the Gemini model used for generate. Empty means the head of GEMINI_MODELS.
	AIModel string `json:"ai_model,omitempty"`
}

// Validate checks the Command and that the fields that Command needs are present.
//
// Incomplete input fails the same way however many times it runs, so it is rejected before reaching the pipeline.
func (r Request) Validate() error {
	if strings.TrimSpace(r.OutputURI) == "" {
		return errors.New("出力先(output_uri)が指定されていません")
	}

	switch r.Command {
	case CommandGenerate, CommandGenerateAndSynthesize:
		if strings.TrimSpace(r.InputURI) == "" {
			return errors.New("入力ソース(input_uri)が指定されていません")
		}
	case CommandSynthesize:
		// The script is the stored one, named by JobID. It does not ride in the payload.
		if strings.TrimSpace(r.JobID) == "" {
			return errors.New("台本が特定できません。保存済み台本の job_id を指定してください")
		}
	case "":
		return fmt.Errorf("%w: command が指定されていません（%q / %q / %q）",
			ErrUnknownCommand, CommandGenerate, CommandSynthesize, CommandGenerateAndSynthesize)
	default:
		return fmt.Errorf("%w: %q（%q / %q / %q）",
			ErrUnknownCommand, r.Command, CommandGenerate, CommandSynthesize, CommandGenerateAndSynthesize)
	}

	return nil
}
