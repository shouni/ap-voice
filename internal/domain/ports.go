package domain

import (
	"context"
	"errors"
)

// Pipeline is the interface that does the processing.
type Pipeline interface {
	// Execute builds every dependency and runs.
	Execute(ctx context.Context, req Request) error
}

// Voice is the interface that synthesises audio and stores the artifacts.
type Voice interface {
	UploadWav(ctx context.Context, outputURI string, lines []ScriptLine) error
	UploadScript(ctx context.Context, outputURI string, script Script) error
}

// ErrScriptNotFound means the job's script has not been stored yet.
//
// It is part of the port's contract, so it lives here. On the implementation side, a caller that
// wants to detect "not found" would have to import the implementation package, defeating the
// point of the port.
var ErrScriptNotFound = errors.New("domain: script not found")

// ScriptStore reads and writes stored scripts.
//
// generate writes it, and synthesize and the detail page read it. This port exists because the
// script is not carried in the task payload (see Request.JobID).
//
// When there is no script yet it returns ErrScriptNotFound. That differs from failing to read it
// (a transient failure): the former is not fixed by waiting, the latter may be.
type ScriptStore interface {
	Load(ctx context.Context, jobID string) (Script, error)
}

// TaskQueue is the interface that hands a run over to the worker side.
//
// The web side never calls the worker directly; it always goes through the queue. Synthesis takes
// minutes, so it cannot be awaited within a request, and it also separates the permissions of the public and executing sides.
type TaskQueue interface {
	Enqueue(ctx context.Context, req Request) error
}
