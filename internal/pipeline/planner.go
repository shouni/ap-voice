package pipeline

import (
	"fmt"

	"github.com/shouni/ap-voice/internal/domain"
)

// Planner decides the sequence of steps (the execution plan) to run for a request.
// This is the only place that maps a command to steps (public-docs worker conventions 2.5).
type Planner interface {
	Plan(req domain.Request) ([]Step, error)
}

// DefaultPlanner is the production Planner.
//
// generate goes as far as writing and storing the script, synthesize as far as reading the stored
// script and producing audio, and generate_and_synthesize does both. Only the source of the script
// (generated or read back) differs; storing and synthesis are the same steps.
type DefaultPlanner struct {
	Generate Step
	Load     Step
	Publish  Step
}

// Plan returns the sequence of steps for the command.
func (p DefaultPlanner) Plan(req domain.Request) ([]Step, error) {
	switch req.Command {
	case domain.CommandGenerate, domain.CommandGenerateAndSynthesize:
		return []Step{p.Generate, p.Publish}, nil
	case domain.CommandSynthesize:
		return []Step{p.Load, p.Publish}, nil
	default:
		return nil, fmt.Errorf("%w: %q", domain.ErrUnknownCommand, req.Command)
	}
}
