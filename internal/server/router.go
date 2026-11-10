// Package server configures HTTP routing and middleware.
package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/cloudlog"
	"github.com/shouni/gcp-kit/cloudrun"
	"github.com/shouni/go-serve-kit/secureheaders"
	"github.com/shouni/go-serve-kit/staticfiles"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/domain"
)

// compressionLevel is the gzip compression level.
const compressionLevel = 5

// gcsOrigin is the origin of GCS, where the synthesised audio actually lives. Pages point at
// same-origin endpoints, but those 302 to a signed URL, so the destination is named explicitly.
const gcsOrigin = "https://storage.googleapis.com"

// NewRouter builds an http.Handler combining middleware and routing.
// projectID is used only for Cloud Logging trace correlation; when empty, no correlation is done.
func NewRouter(h *builder.AppHandlers, projectID string) http.Handler {
	r := chi.NewRouter()
	registerMiddleware(r, projectID)
	registerRoutes(r, h)

	return r
}

// registerMiddleware configures the standard middleware.
func registerMiddleware(r *chi.Mux, projectID string) {
	// Trace correlation must take effect before logging, so it is registered first.
	r.Use(cloudlog.TraceMiddleware(projectID))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.CleanPath)
	// The pages are Japanese UTF-8 (3 bytes per character), so compression works well. Static files
	// take the same path (vendor is immutable, so recompression is rare).
	r.Use(middleware.Compress(compressionLevel))
	r.Use(secureheaders.Middleware(secureheaders.Config{
		MediaSources: []string{gcsOrigin},
		// Because Bootstrap's JS applies inline styles during transitions.
		AllowInlineStyle: true,
	}))
}

// registerRoutes registers each component's handlers with the router.
//
// Routes skip registration when a handler is nil. BuildHandlers leaves handlers for sides a
// process does not serve nil, so adding a role needs no change here.
func registerRoutes(r chi.Router, h *builder.AppHandlers) {
	// For why the path was chosen (not "/healthz"), see cloudrun.HealthPath.
	r.Get(cloudrun.HealthPath, cloudrun.Health)

	registerStaticRoutes(r)

	if h == nil {
		slog.Warn("AppHandlers is nil, skipping application routes registration")
		return
	}

	// Authentication (the OAuth2 flow). The login itself is not protected.
	if h.Auth != nil {
		r.Handle("/auth/*", h.Auth.Routes()) // login / callback / logout
	}

	// The web side. Auth's middleware sends unauthenticated access to /auth/login.
	// AppHandlers.Validate guarantees that Auth and Web come as a pair,
	// so checking one of them for nil is enough here.
	r.Group(func(r chi.Router) {
		if h.Web == nil {
			return
		}
		// Browsers (session + CSRF) and machines (OIDC Bearer) hit the same routes.
		// Combining the two is left to the library because building each path by hand lets one
		// get hardened alone and drift. For the order of checks and CSRF handling, see auth.Protected.
		r.Use(auth.Protected(h.M2M, h.Auth))

		r.Get("/", h.Web.Home)
		r.Get("/modes", h.Web.Modes)
		r.Get("/modes/{mode}", h.Web.ModeDetail)
		r.Get("/speakers", h.Web.Speakers)
		// Reading checks belong to no job (they send lines before saving).
		r.Post("/reading/preview", h.Web.PreviewReading)

		// The job is the only primary resource. It is named by the same /jobs/{jobID} from enqueue to
		// delete, and people and machines use the same routes, told apart by Accept and the body's shape
		// (public-docs URL naming conventions).
		r.Route("/jobs", func(r chi.Router) {
			r.Post("/", h.Web.JobCreate)
			r.Get("/", h.Web.JobList)
			r.Get("/{jobID}", h.Web.Job)
			r.Delete("/{jobID}", h.Web.JobDelete)
			r.Get("/{jobID}/audio", h.Web.Audio)
			r.Get("/{jobID}/script", h.Web.Script)
			r.Put("/{jobID}/script", h.Web.ScriptUpdate)
			r.Post("/{jobID}/synthesize", h.Web.Synthesize)
			r.Post("/{jobID}/regenerate", h.Web.Regenerate)
		})

	})

	// Route dedicated to Cloud Tasks (for the worker).
	// In a SERVER_ROLE=web process both TaskAuth and Worker are nil, so
	// this whole group is not registered and /tasks/generate is not exposed.
	// builder.AppHandlers.Validate rejects at startup the shape where only one is nil, so
	// checking whether TaskAuth exists is enough here.
	r.Group(func(r chi.Router) {
		if h.TaskAuth == nil {
			return
		}

		r.Use(auth.Require(h.TaskAuth))
		r.Post(domain.WorkerTaskPath, h.Worker.ProcessTask)
	})
}

// registerStaticRoutes serves the embedded static files at /static/*.
// go-serve-kit's staticfiles owns the Cache-Control decision (short-lived for our own files,
// immutable for vendor) and suppresses directory listings.
//
// It sits outside authentication. There is no reason to require login for a stylesheet,
// and the login page, shown unauthenticated, references it too.
func registerStaticRoutes(r chi.Router) {
	files, err := staticfiles.New(staticfiles.Config{FS: assets.StaticFiles, Dir: "static"})
	if err != nil {
		// A mix-up of embedded constants, so it stops before accepting any request.
		panic(fmt.Sprintf("static assets: %v", err))
	}
	r.Handle("/static/*", files)
}
