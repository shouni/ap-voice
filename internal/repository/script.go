// Package repository reads artifacts on GCS and reads and writes job state on Firestore.
// The history listing is built from state alone, without touching artifacts.
package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/internal/domain"
)

// ErrJobNotFound means the job pointed at exists neither as artifacts nor as a record.
//
// It is not "has no artifacts". A job that failed before writing a script has no artifacts at all,
// but its record remains, and unless it can be deleted it stays in the history.
var ErrJobNotFound = errors.New("repository: job not found")

// Repository reads job artifacts.
//
// PublishStep does the writing; this only reads. Artifacts are grouped under a prefix per
// job ID, so listing and deleting work without knowing the contents.
type Repository struct {
	// store is scoped to gs://{bucket}. Every path below is relative to it,
	// so the bucket need not be carried around on every call.
	store  remoteio.Store
	layout domain.StorageLayout
	// status is the job's progress. It lives somewhere other than the artifacts (Firestore), so
	// a bulk delete of the prefix does not clean it up. Delete removes it explicitly.
	//
	// It is held as an interface rather than the concrete type so that verifying the listing
	// does not require Firestore (checks that need the real thing belong to the emulator).
	status statusStore
}

// statusStore is what Repository does to job state.
// *jobstatus.Store[domain.JobStatus] satisfies it as is.
type statusStore interface {
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
	Save(ctx context.Context, jobID string, status domain.JobStatus) error
	Delete(ctx context.Context, jobID string) error
	List(ctx context.Context, page, perPage int, opts ...jobstatus.ListOption) ([]domain.JobStatus, jobstatus.PageMeta, error)
}

// Get reads the job's state. The history detail page uses it.
func (r *Repository) Get(ctx context.Context, jobID string) (domain.JobStatus, error) {
	return r.status.Get(ctx, jobID)
}

// statusCollection is the Firestore collection that holds job states.
//
// It uses the same vocabulary as the artifact bucket. There is one collection per service,
// not one shared collection with a discriminator field (a forgotten filter would not fail and would
// mix other services' jobs into the history). It is a constant rather than a setting because it is
// the service's identity, not a value that changes per deployment.
const statusCollection = "ap-voice"

// NewJobStatusStore returns the Firestore store that holds job states.
//
// The builder makes exactly one and hands it to both the Recorder and NewRepository.
// Pass this *jobstatus.Store to the Recorder unwrapped. The Recorder finds the Store's
// Modify (doing the re-run guard in one transaction) by type. Passing a wrapping type
// hides Modify, and with compilation and tests still passing, it falls back to the
// interruptible path that calls Get and Save separately.
//
// It can be built with a nil client. Then only job state reads and writes fail
// (for tests that verify artifacts alone).
func NewJobStatusStore(client *firestore.Client) *jobstatus.Store[domain.JobStatus] {
	return jobstatus.NewStore[domain.JobStatus](client, statusCollection)
}

// If Store stops satisfying Modifier (gcp-kit downgraded, the type wrapped), compilation stops
// here. Running without it, the re-run guard would silently become non-atomic again.
var _ jobstatus.Modifier[domain.JobStatus] = (*jobstatus.Store[domain.JobStatus])(nil)

// NewRepository builds a Repository. status is the return value of NewJobStatusStore.
func NewRepository(store remoteio.Store, bucket string, status *jobstatus.Store[domain.JobStatus]) (*Repository, error) {
	if store == nil {
		return nil, fmt.Errorf("no storage was given")
	}
	if bucket == "" {
		return nil, fmt.Errorf("no bucket was given")
	}
	if status == nil {
		return nil, fmt.Errorf("no job state store was given")
	}
	layout := domain.NewStorageLayout()
	scoped := store.Sub(remoteio.BuildURI(remoteio.SchemeGCS, bucket, ""))
	return &Repository{store: scoped, layout: layout, status: status}, nil
}

// SaveScript writes an edited script back over the stored one.
//
// It writes back to the same place Load reads (StorageLayout decides both).
// If they drifted, it would break in a hard-to-notice way: edited, yet synthesised from the old script.
func (r *Repository) SaveScript(ctx context.Context, jobID string, script domain.Script) error {
	if err := jobid.Validate(jobID); err != nil {
		return fmt.Errorf("invalid job ID (%s): %w", jobID, err)
	}

	body, err := json.MarshalIndent(script, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode the script as JSON: %w", err)
	}

	uri := r.layout.ScriptPath(jobID)
	if err := r.store.Write(ctx, uri, bytes.NewReader(body),
		remoteio.WithContentType("application/json; charset=utf-8")); err != nil {
		return fmt.Errorf("failed to save the script (%s): %w", uri, err)
	}
	// The listing takes titles from the state, so unless it follows here the old title remains.
	// Back when titles came from reading the script, a save was reflected immediately.
	// Even if this fails, the save itself is reported as a success (the script matters more than the title's freshness).
	r.refreshTitle(ctx, jobID, script.Title)

	slog.InfoContext(ctx, "saved the edited script", "job_id", jobID, "lines", len(script.Lines))
	return nil
}

// refreshTitle rewrites only the title on the stored state.
func (r *Repository) refreshTitle(ctx context.Context, jobID, title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}

	status, err := r.status.Get(ctx, jobID)
	if err != nil {
		// With no record there is nothing to follow. It happens when a script is edited before any record exists.
		if !errors.Is(err, jobstatus.ErrNotFound) {
			slog.WarnContext(ctx, "could not read the job state to update the title",
				"job_id", jobID, "error", err)
		}
		return
	}
	if status.Title == title {
		return
	}

	status.Title = title
	if err := r.status.Save(ctx, jobID, status); err != nil {
		slog.WarnContext(ctx, "could not update the title on the job state",
			"job_id", jobID, "error", err)
	}
}

// Load reads the stored script. It satisfies domain.ScriptStore.
//
// When not written yet it returns ErrScriptNotFound. A missing script is not an anomaly but
// the normal shape of a job before generate finishes or after it fails. If the caller could not
// tell "not yet" from "could not read", it would have to treat both as the same failure, and
// the detail page could not show the state.
func (r *Repository) Load(ctx context.Context, jobID string) (domain.Script, error) {
	// Job IDs also come from forms, so they are always validated before going into a path.
	if err := jobid.Validate(jobID); err != nil {
		return domain.Script{}, fmt.Errorf("invalid job ID (%s): %w", jobID, err)
	}

	path := r.layout.ScriptPath(jobID)
	stream, err := r.store.Open(ctx, path)
	if err != nil {
		return domain.Script{}, r.openFailure(ctx, jobID, path, err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			slog.WarnContext(ctx, "failed to close the script stream", "error", closeErr)
		}
	}()

	body, err := io.ReadAll(stream)
	if err != nil {
		return domain.Script{}, fmt.Errorf("failed to read the script: %w", err)
	}

	var script domain.Script
	if err := json.Unmarshal(body, &script); err != nil {
		return domain.Script{}, fmt.Errorf("failed to decode the script JSON: %w", err)
	}
	return script, nil
}

// openFailure splits why the script could not be opened into "not yet" and "could not read".
//
// How absence is spelled differs per store (different error values for GCS and local), so the
// returned error's contents are not inspected. It asks once more whether the object exists.
// The extra round trip rides only on the failing path; successful reads are unchanged.
func (r *Repository) openFailure(ctx context.Context, jobID, path string, err error) error {
	if exists, existsErr := r.store.Exists(ctx, path); existsErr == nil && !exists {
		return fmt.Errorf("no script yet (%s): %w", jobID, domain.ErrScriptNotFound)
	}
	return fmt.Errorf("failed to read the script: %w", err)
}

// HasAudio reports whether the job's audio already exists.
//
// It does not search a listing. It used to search the List result linearly for the job, which
// had two problems. One, it read every job's script to learn a single boolean.
// Two, old jobs beyond the cap (50) were always false, so the playback section vanished from
// the detail page although the audio existed.
func (r *Repository) HasAudio(ctx context.Context, jobID string) (bool, error) {
	if err := jobid.Validate(jobID); err != nil {
		return false, fmt.Errorf("invalid job ID (%s): %w", jobID, err)
	}
	return r.store.Exists(ctx, r.layout.AudioPath(jobID))
}

// Job is one entry of the history listing.
type Job struct {
	ID string
	// Title is the script's title. If it cannot be read, the job ID goes here.
	// The audio may survive even when the script is broken, and vanishing from the listing
	// would lose the way to delete it.
	Title string
	// CreatedAt is the creation time recovered from the job ID.
	CreatedAt time.Time
	// HasAudio reports whether the audio has been made. It distinguishes the script-only stage.
	HasAudio bool
	// State is the progress state. Going by artifacts alone cannot tell running from failed.
	// The record had the value all along; the listing used to read it and throw it away.
	State jobstatus.State
	// Error is the failure reason. Set only when State is failed.
	Error string
}

// List returns the given page of jobs, newest first. The caller decides the filter
// (jobstatus.WithState and so on). Re-creating the filter vocabulary here would duplicate
// what the library already has.
//
// It reads no artifacts at all. It used to scan the whole bucket to collect job IDs and audio
// presence, and read a page's scripts in parallel to get titles. The scan grew with the number
// of jobs, without bound. Now one page is fetched by query, and the three values needed
// (title, audio presence, creation time) all come from the state.
func (r *Repository) List(ctx context.Context, page, perPage int, opts ...jobstatus.ListOption) ([]Job, jobstatus.PageMeta, error) {
	statuses, meta, err := r.status.List(ctx, page, perPage, opts...)
	if err != nil {
		return nil, jobstatus.PageMeta{}, fmt.Errorf("failed to list the history: %w", err)
	}

	jobs := make([]Job, 0, len(statuses))
	for _, status := range statuses {
		job := Job{
			ID:    status.JobID,
			Title: status.Title,
			// Only synthesize made audio. generate ends at the script, so the location
			// is not set (it can be told apart without counting artifacts).
			HasAudio: status.AudioURI != "",
			State:    status.State,
			Error:    status.Error,
		}
		// It stays in the listing even without a title. Jobs whose generation failed before the title
		// was settled are exactly the ones to delete, and vanishing from the listing would lose the way to.
		if job.Title == "" {
			job.Title = status.JobID
		}
		job.CreatedAt, _ = jobid.CreatedAt(status.JobID)
		jobs = append(jobs, job)
	}
	return jobs, meta, nil
}

// Delete removes one job's artifacts together.
//
// It lists and deletes everything under the prefix. Being deletable without the caller knowing
// what was placed is the reason artifacts are grouped per job ID.
func (r *Repository) Delete(ctx context.Context, jobID string) error {
	if err := jobid.Validate(jobID); err != nil {
		return fmt.Errorf("invalid job ID (%s): %w", jobID, err)
	}

	deleted, err := remoteio.DeletePrefix(ctx, r.store, r.layout.VoiceJobPrefix(jobID))
	if err != nil {
		return fmt.Errorf("delete failed (%s): %w", jobID, err)
	}

	// A job without a single artifact is one that failed before writing a script. This used to
	// answer "not found", but that was exactly the job to delete.
	// Only its record stayed in the history, the detail page could not open without a script, and
	// deletion was refused by this branch, so it sat there undeletable from anywhere.
	if deleted == 0 {
		return r.deleteRecord(ctx, jobID)
	}
	// The state lives apart from the artifacts, so unless it is deleted here an orphan remains.
	// The state file used to sit under the job directory and was cleaned up by the bulk delete
	// above. Moving it to Firestore lost that link.
	//
	// Even if this fails, the deletion itself is reported as a success. The artifacts are already gone
	// and the caller's request has been fulfilled. What remains is only an observational record, so
	// returning an error here would do more harm by looking like "it was deleted yet failed".
	if err := r.status.Delete(ctx, jobID); err != nil {
		slog.WarnContext(ctx, "failed to delete the job state; an orphan remains",
			"job_id", jobID, "error", err)
	}

	slog.InfoContext(ctx, "deleted the job artifacts", "job_id", jobID, "objects", deleted)
	return nil
}

// deleteRecord deletes only the record of a job with no artifacts left.
//
// With no record either, it is ErrJobNotFound. "Pointed at an unknown job" and
// "cleaned up a job that failed halfway" are answered differently by the caller
// (404 for the former, success for the latter).
//
// Unlike the path with artifacts, a failure to delete the record returns an error. There,
// the requested artifacts are already gone so a warning is enough; here, if the record cannot
// be deleted nothing has happened.
func (r *Repository) deleteRecord(ctx context.Context, jobID string) error {
	if _, err := r.status.Get(ctx, jobID); err != nil {
		if errors.Is(err, jobstatus.ErrNotFound) {
			return fmt.Errorf("job not found (%s): %w", jobID, ErrJobNotFound)
		}
		return fmt.Errorf("failed to check the job state (%s): %w", jobID, err)
	}
	if err := r.status.Delete(ctx, jobID); err != nil {
		return fmt.Errorf("failed to delete the job state (%s): %w", jobID, err)
	}

	slog.InfoContext(ctx, "deleted the record of a job with no artifacts", "job_id", jobID)
	return nil
}
