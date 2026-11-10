package builder

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/adapters"
	"github.com/shouni/ap-voice/internal/app"
	"github.com/shouni/ap-voice/internal/config"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/server/handlers"
)

const defaultSessionName = "ap-voice-session"

// AppHandlers holds every HTTP handler that was built.
// The server package receives it and sets up routing.
type AppHandlers struct {
	// Auth handles Google OAuth login and sessions. Only the web side has it.
	Auth *session.Handler
	// Web is the submission form and the intake of runs.
	Web *handlers.Handler
	// Worker runs the domain.Request delivered by Cloud Tasks.
	// gcp-kit/worker owns decoding the payload and deciding whether to retry, so
	// this app needs no HTTP-aware code for it.
	Worker *worker.Handler[domain.Request]
	// TaskAuth verifies the OIDC token from Cloud Tasks. It needs no OAuth settings, so
	// it can be built even in a worker process that has no web side.
	TaskAuth *oidc.Verifier
	// M2M verifies OIDC from machines (the MCP server and so on) rather than browsers.
	// It is not nil even when unset. Given a verifier that always fails,
	// auth.Protected falls back to session authentication and keeps working.
	M2M auth.Authenticator
}

// Validate checks that the assembled handlers make sense for the role.
//
// TaskAuth and Worker pair up as "verifying Cloud Tasks" and "what comes after it";
// only one of them being nil is a DI inconsistency. router.go skips registering nil routes, so
// left alone /tasks/generate would just silently 404, and the request could not tell whether
// the cause is configuration or code. Startup fails before the router can return a 404.
func (h *AppHandlers) Validate() error {
	if (h.TaskAuth == nil) != (h.Worker == nil) {
		return errors.New("TaskAuth and Worker must be configured together")
	}
	// Web and Auth are a pair too. Without Auth the form would be public without authentication.
	if (h.Auth == nil) != (h.Web == nil) {
		return errors.New("the auth handler and the web handler must be configured together")
	}
	return nil
}

// BuildHandlers assembles each handler's dependencies according to SERVER_ROLE and returns
// the AppHandlers. Handlers for sides this process does not serve stay nil, so
// the router skips registering their routes altogether.
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	h := &AppHandlers{}
	role := appCtx.Config.Server.Role

	if role.ServesWeb() {
		if err := buildWebHandlers(appCtx, h); err != nil {
			return nil, err
		}
	}

	if role.ServesWorker() {
		// Verification always fails (fail-closed) unless both the audience and the allowed caller SAs
		// are present, so the configuration is checked at startup.
		taskAuth, err := oidc.New(appCtx.Config.Tasks.TaskAudienceURL, appCtx.Config.Tasks.AllowedServiceAccounts)
		if err != nil {
			return nil, fmt.Errorf("cannot configure OIDC verification for incoming tasks; TASK_AUDIENCE_URL and ALLOWED_TASK_SERVICE_ACCOUNTS are required: %w", err)
		}
		h.TaskAuth = taskAuth
		h.Worker = worker.NewHandler[domain.Request](appCtx.Pipeline)
	}

	if err := h.Validate(); err != nil {
		return nil, err
	}

	return h, nil
}

// buildWebHandlers builds the web side's handlers (OAuth and the submission form).
func buildWebHandlers(appCtx *app.Container, h *AppHandlers) error {
	authHandler, err := createAuthHandler(appCtx.Config, appCtx.SessionStore)
	if err != nil {
		return fmt.Errorf("failed to initialize the auth handler: %w", err)
	}

	tmpl, err := assets.ParsePages()
	if err != nil {
		return fmt.Errorf("failed to load the templates: %w", err)
	}

	// The form's options come from the same embedding as generation. With a separate list,
	// a mode shown on the page might not exist on the worker.
	// Display names and descriptions come from the same files' front matter too.
	modes, err := assets.LoadModes()
	if err != nil {
		return fmt.Errorf("failed to load the generation modes: %w", err)
	}

	webHandler, err := handlers.NewHandler(handlers.HandlerOptions{
		Queue:       appCtx.TaskQueue,
		Templates:   tmpl,
		Modes:       modes,
		Models:      appCtx.Config.AI.GeminiModels,
		Bucket:      appCtx.Config.Storage.GCSBucket,
		MusicBucket: appCtx.Config.Storage.MusicBucket,
		Repo:        appCtx.Repository,
		Signer:      appCtx.Store,
		Speakers:    appCtx.Speakers,
		// To show the prompt text in the catalogue, it is given the same assembly as generation.
		// A separate one would let what the page shows drift from what is actually sent.
		Renderer:  appCtx.Prompt,
		Reading:   adapters.NewReadingAdapter(),
		JobStatus: appCtx.JobStatus,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize the submission form handler: %w", err)
	}

	// The audience is this service's own public URL. Callers fetch a token "addressed to this URL",
	// so if it drifts, everything becomes 401.
	// Machine calls are optional. When unset, the method is dropped altogether and auth.Protected
	// skips it (a nil Authenticator is not iterated).
	var m2m auth.Authenticator
	if v, err := oidc.New(appCtx.Config.Server.ServiceURL, appCtx.Config.Auth.AllowedM2MServiceAccounts); err != nil {
		slog.Info("ALLOWED_M2M_SERVICE_ACCOUNTS is unset, so the API does not accept machine calls", "reason", err)
	} else {
		m2m = v
	}

	h.Auth = authHandler
	h.Web = webHandler
	h.M2M = m2m
	return nil
}

// createAuthHandler initialises the Google OAuth authentication handler.
func createAuthHandler(cfg *config.Config, store session.Store) (*session.Handler, error) {
	return session.New(session.Config{
		ClientID:       cfg.Auth.GoogleClientID,
		ClientSecret:   cfg.Auth.GoogleClientSecret,
		ServiceURL:     cfg.Server.ServiceURL,
		Store:          store,
		SessionName:    defaultSessionName,
		AllowedEmails:  cfg.Auth.AllowedEmails,
		AllowedDomains: cfg.Auth.AllowedDomains,
	},
		// Only this app exposes /auth/logout. Without passing prompt, right after logging out and
		// being sent to the login page, Google approves without asking anything and
		// the user is back in their previous session (on a shared device "log out" does not work).
		session.WithPrompt(session.PromptSelectAccount),
	)
}
