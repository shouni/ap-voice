// Package app provides the DI container that assembles and holds the application's dependencies.
package app

import (
	"io"
	"log/slog"

	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/internal/adapters"
	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// Container holds the application's dependencies (the DI container).
type Container struct {
	Config *config.Config
	// I/O and Storage
	// Storage owns the GCS client's lifetime. It is kept to hand to parties that want
	// the factory itself, such as go-web-reader.
	Storage remoteio.Factory
	// Store is the read/write window taken from Storage.
	Store remoteio.Store
	// Speakers is the roster of speakers and styles in use. It is assets/speakers.json
	// interpreted, and both the response schema construction and synthesis read it.
	Speakers *speaker.Registry
	// Prompt assembles the embedded prompts. Both the generating step (worker) and the
	// catalogue that shows the text (web) use it. role=both used to build it twice,
	// so the container holds one. Going through the same assembly is also what makes
	// what the page shows match what reaches Gemini.
	Prompt *adapters.PromptAdapter
	// TaskQueue is how the web side hands runs to the worker side. It is nil on the worker side.
	TaskQueue domain.TaskQueue
	// SessionStore is where login sessions are stored.
	SessionStore session.Store
	// Repository reads the artifacts. It backs the history pages and synthesis from a stored script.
	Repository *repository.Repository
	// External Adapters
	HTTPClient httpkit.HTTPClient
	Notifier   domain.Notifier
	// JobStatus records job progress. The web side writes queued, and
	// the worker side writes running / succeeded / failed.
	JobStatus *jobstatus.Recorder[domain.JobStatus]
	// Business Logic
	Pipeline domain.Pipeline
	// Closers are the resources opened during assembly. Container.Close closes them all.
	// Close does not look at individual fields so that, when a resource is added, the builder
	// only has to append it.
	Closers []io.Closer
}

// Close safely releases every external resource the Container holds.
//
// It returns no error because its only caller is the single defer in server.Run, and a returned
// error would have nowhere to go but slog.Error.
func (c *Container) Close() {
	for _, closer := range c.Closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil {
			slog.Error("failed to close resource", "error", err)
		}
	}
}
