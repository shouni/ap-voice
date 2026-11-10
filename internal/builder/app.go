// Package builder assembles each client and the DI container from the configuration.
package builder

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"cloud.google.com/go/firestore"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/gcp-kit/tasks"
	"github.com/shouni/go-http-kit/httpkit"
	"github.com/shouni/go-remote-io/remoteio/gcs"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/adapters"
	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// BuildContainer connects to the external services, assembles the dependencies and returns the app.Container.
func BuildContainer(ctx context.Context, cfg *config.Config) (container *app.Container, err error) {
	var resources []io.Closer
	defer func() {
		if err != nil {
			for _, r := range resources {
				if closeErr := r.Close(); closeErr != nil {
					slog.Warn("failed to close resource during cleanup", "error", closeErr)
				}
			}
		}
	}()

	// 2. I/O Infrastructure
	storage, err := gcs.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS factory: %w", err)
	}

	resources = append(resources, storage)

	store, err := storage.Store()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize IO components: %w", err)
	}

	// Where job states live. Artifacts stay in GCS; only the state is kept in Firestore.
	// Both roles need it — web writes queued and reads the history, the worker writes
	// running / succeeded / failed and reads it for the re-run guard.
	jobStatus, err := jobstatus.New(ctx,
		jobstatus.WithProjectID(cfg.GCP.ProjectID),
		jobstatus.WithDatabase(cfg.Storage.FirestoreDatabase),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize firestore: %w", err)
	}
	resources = append(resources, jobStatus)

	jobStatusClient, err := jobStatus.Client()
	if err != nil {
		return nil, fmt.Errorf("failed to get the firestore client: %w", err)
	}

	httpClient := httpkit.New(
		httpkit.WithTimeout(cfg.HTTP.Timeout),
		httpkit.WithMaxRetries(1),
		httpkit.WithSkipNetworkValidation(true),
	)

	// 3. Notifier
	notifier, err := buildNotifier(httpClient.WithoutRetry(), cfg.Notification.SlackWebhookURL, cfg.Server.ServiceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize notifier: %w", err)
	}

	// The speaker roster is an embedded resource, so it is built before any external connection.
	// If it is broken, startup fails rather than it being noticed after synthesis starts.
	speakers, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to load the speaker roster: %w", err)
	}

	// Prompt assembly can also be built from embedded resources alone. The generating step and
	// the catalogue use the same one, so exactly one is built here.
	prompt, err := adapters.NewPromptAdapter()
	if err != nil {
		return nil, fmt.Errorf("failed to assemble the prompts: %w", err)
	}

	// Both roles read artifacts: web to show the history,
	// the worker for synthesize to read stored scripts.
	//
	// Exactly one job state store is built and handed to both the Recorder and the Repository.
	// It is passed to the Recorder unwrapped (see repository.NewJobStatusStore).
	statusStore := repository.NewJobStatusStore(jobStatusClient)
	repo, err := repository.NewRepository(store, cfg.Storage.GCSBucket, statusStore)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the repository: %w", err)
	}

	appCtx := &app.Container{
		Config:     cfg,
		JobStatus:  jobstatus.NewRecorder[domain.JobStatus](statusStore),
		Speakers:   speakers,
		Prompt:     prompt,
		Repository: repo,
		Storage:    storage,
		Store:      store,
		HTTPClient: httpClient,
		Notifier:   notifier,
		// Store only wraps the client the factory holds; its lifetime belongs to the factory.
		// When assembly fails, the resources side closes storage directly, so
		// Closers only handles release after a successful return.
		Closers: []io.Closer{storage, jobStatus},
	}

	// Only the web side enqueues tasks. The worker side receives them, so by not building it
	// the worker carries no unused Cloud Tasks client and no dependency on CLOUD_TASKS_QUEUE_ID.
	// Sessions live in a different database from job states (SessionDatabase).
	var sessionStore session.Store
	if cfg.Server.Role.ServesWeb() {
		fsClient, fsErr := firestore.NewClientWithDatabase(ctx, cfg.GCP.ProjectID, cfg.Auth.SessionDatabase)
		if fsErr != nil {
			return nil, fmt.Errorf("failed to initialize the session Firestore: %w", fsErr)
		}
		resources = append(resources, fsClient)
		appCtx.Closers = append(appCtx.Closers, fsClient)

		sessionStore, fsErr = session.NewFirestoreStore(session.FirestoreConfig{
			Client:     fsClient,
			Collection: cfg.Auth.SessionCollection,
		})
		if fsErr != nil {
			return nil, fmt.Errorf("failed to build the session store: %w", fsErr)
		}
		appCtx.SessionStore = sessionStore
	}

	if cfg.Server.Role.ServesWeb() {
		queue, qErr := adapters.NewTaskQueueAdapter(ctx, tasks.Config{
			ProjectID:           cfg.GCP.ProjectID,
			LocationID:          cfg.GCP.LocationID,
			QueueID:             cfg.Tasks.QueueID,
			WorkerURL:           cfg.Tasks.WorkerURL,
			WorkerPath:          domain.WorkerTaskPath,
			ServiceAccountEmail: cfg.Tasks.CallerServiceAccountEmail,
			Audience:            cfg.Tasks.TaskAudienceURL,
			DispatchDeadline:    cfg.Tasks.DispatchDeadline,
		})
		if qErr != nil {
			return nil, qErr
		}
		appCtx.TaskQueue = queue
		appCtx.Closers = append(appCtx.Closers, queue)
		resources = append(resources, queue)
	}

	// Only the worker side runs the pipeline. By not building it, the web side carries
	// no unused Gemini client and no connection to the VOICEVOX engine.
	// Initialising VOICEVOX calls /speakers, so building it on the public side would also
	// hit the engine's cold start on every startup.
	if cfg.Server.Role.ServesWorker() {
		p, err := buildPipeline(ctx, appCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to build pipeline: %w", err)
		}
		appCtx.Pipeline = p
	}

	return appCtx, nil
}

// buildNotifier initialises notifications.
// When webhookURL is unset, the adapter returns an implementation that sends nothing.
func buildNotifier(httpClient httpkit.Poster, webhookURL, serviceURL string) (domain.Notifier, error) {
	if webhookURL == "" {
		slog.Info("Slack webhook URL is unset; notifications are disabled")
	}

	return adapters.NewSlackAdapter(httpClient, webhookURL, serviceURL)
}
