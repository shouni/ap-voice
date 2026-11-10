// Package handlers implements the web side's HTTP handlers.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/jobstatus"
	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
	"github.com/shouni/ap-voice/internal/repository"
)

// jobIDPrefix is the prefix of issued job IDs (voice-{date}-{time}-{hex12}).
const jobIDPrefix = "voice"

// Signer represents only the signing the handlers need.
// remoteio.Store satisfies it as is.
type Signer interface {
	SignURL(ctx context.Context, name, method string, expires time.Duration) (string, error)
}

// Handler is the web side's handler.
type Handler struct {
	queue     domain.TaskQueue
	templates map[string]*template.Template
	// modes are the generation modes shown on the submission form. They come from the same embedded
	// templates as generation. If the form had its own list, a mode shown on the page might not exist
	// on the worker. Display names and descriptions are the prompts' front matter.
	modes []assets.Mode
	// models is GEMINI_MODELS. The first is the default, and the form offers them as options.
	models []string
	// bucket and layout decide the output location. Users do not enter it.
	bucket string
	layout domain.StorageLayout
	// musicBucket is used to resolve the music promotion mode's input from a music generation service job ID.
	// It is the same rule as the video generation service's (gs://<musicBucket>/music/<jobID>/recipe.json).
	musicBucket string
	// repo reads the history listing and scripts.
	repo ScriptRepository
	// signer makes signed URLs for audio. The app does not serve the bytes.
	signer Signer
	// status records queued at enqueue time (for the ordering, see recordQueued).
	status *jobstatus.Recorder[domain.JobStatus]
	// reading is used to check readings before synthesis.
	reading ReadingConverter
	// renderer is used to show the prompt text in the catalogue.
	// It goes through the same assembly as generation, so what the page shows matches what reaches Gemini.
	renderer PromptRenderer
	// speakers are the editor's options. They are kept to show only styles that exist for each speaker
	// (for why non-existent pairs are rejected, see validateScript).
	speakers *speaker.Registry
	// styles maps a speaker name to that speaker's styles, and stylesJSON is its JSON.
	// The roster does not change after startup, so it is built once at construction. The detail page
	// used to rebuild it and re-encode it to JSON every time it opened.
	styles     map[string][]string
	stylesJSON string
}

// ReadingConverter returns how text will be read at synthesis.
// It is the same conversion go-voicevox runs just before synthesis.
type ReadingConverter interface {
	ConvertToReading(text string) (string, error)
}

// PromptRenderer assembles a mode's prompt text.
// It is given the same implementation the generating side (ScriptStep) uses.
type PromptRenderer interface {
	Generate(mode, content string) (string, error)
}

// ScriptRepository reads the history listing and scripts.
type ScriptRepository interface {
	List(ctx context.Context, page, perPage int, opts ...jobstatus.ListOption) ([]repository.Job, jobstatus.PageMeta, error)
	Load(ctx context.Context, jobID string) (domain.Script, error)
	SaveScript(ctx context.Context, jobID string, script domain.Script) error
	Get(ctx context.Context, jobID string) (domain.JobStatus, error)
	HasAudio(ctx context.Context, jobID string) (bool, error)
	Delete(ctx context.Context, jobID string) error
}

// signedURLExpiry is how long an audio signed URL stays valid.
// Long enough not to expire during playback, not long enough to pass the link around.
const signedURLExpiry = time.Hour

// HandlerOptions are the Handler's dependencies.
type HandlerOptions struct {
	Queue domain.TaskQueue
	// Templates are the per-page template sets (keyed by file name, such as "history.html").
	// All pages cannot go into one set because each page defines content / title / scripts
	// under the same names. Use assets.ParsePages.
	Templates map[string]*template.Template
	Modes     []assets.Mode
	Models    []string
	Bucket    string
	// MusicBucket is used to resolve the music promotion mode's input. Startup proceeds when it is unset
	// (that tab just stops accepting job IDs; the other modes work).
	MusicBucket string
	Repo        ScriptRepository
	Signer      Signer
	Speakers    *speaker.Registry
	Renderer    PromptRenderer
	Reading     ReadingConverter
	JobStatus   *jobstatus.Recorder[domain.JobStatus]
}

// NewHandler creates a Handler.
func NewHandler(opts HandlerOptions) (*Handler, error) {
	if opts.Queue == nil {
		return nil, errors.New("no TaskQueue was given")
	}
	if len(opts.Templates) == 0 {
		return nil, errors.New("no templates were given")
	}
	if len(opts.Modes) == 0 {
		return nil, errors.New("no generation modes were loaded")
	}
	if len(opts.Models) == 0 {
		return nil, errors.New("no models were given")
	}
	if opts.Bucket == "" {
		return nil, errors.New("no output bucket was given")
	}
	if opts.Repo == nil {
		return nil, errors.New("no repository was given")
	}
	if opts.Speakers == nil {
		return nil, errors.New("no speaker roster was given")
	}
	if opts.Renderer == nil {
		return nil, errors.New("no prompt renderer was given")
	}
	if opts.Reading == nil {
		return nil, errors.New("no reading converter was given")
	}

	styles := stylesBySpeaker(opts.Speakers)
	stylesJSON, err := json.Marshal(styles)
	if err != nil {
		// The roster comes from an embedded resource, so this fails only at startup.
		// Refusing to start when it cannot be built is noticed sooner than after opening a page.
		return nil, fmt.Errorf("failed to build the speaker styles: %w", err)
	}

	return &Handler{
		queue:       opts.Queue,
		templates:   opts.Templates,
		modes:       append([]assets.Mode(nil), opts.Modes...),
		models:      append([]string(nil), opts.Models...),
		bucket:      opts.Bucket,
		musicBucket: opts.MusicBucket,
		layout:      domain.NewStorageLayout(),
		repo:        opts.Repo,
		signer:      opts.Signer,
		speakers:    opts.Speakers,
		renderer:    opts.Renderer,
		reading:     opts.Reading,
		status:      opts.JobStatus,
		styles:      styles,
		stylesJSON:  string(stylesJSON),
	}, nil
}

// baseView holds the values every page shares. The nav reads .Path and .DefaultModel.
type baseView struct {
	CSRFToken string
	// Path is the current location, used to highlight the nav.
	Path string
	// DefaultModel is the head of GEMINI_MODELS. Model IDs change on Google's schedule,
	// so it comes from configuration rather than being written into the page.
	DefaultModel string
	// JS are the scripts this page loads in addition. renderTemplate fills them from pageScripts,
	// so handlers do not set them (the layout loads app.js, which every page shares).
	JS []string
}

// setScripts puts the scripts for the page being rendered.
// Only baseView satisfies pageView, and each page's view inherits it by embedding.
func (b *baseView) setScripts(js []string) { b.JS = js }

// pageView is the page value renderTemplate receives.
//
// Only a pointer satisfies it, so passing a value does not compile. Back when values went straight
// to the template, adding a script meant adding a define "scripts", and forgetting it left that
// page alone without its JS. Now pageScripts holds which page loads what, and pages do nothing.
type pageView interface {
	setScripts(js []string)
}

// pageScripts are the scripts each page loads in addition.
//
// They live here rather than in the templates because the mapping is fixed. In the templates,
// whoever adds a page would copy the loading details too (defer or not, where it goes).
// What every page shares is app.js, which the layout loads.
var pageScripts = map[string][]string{
	"home.html":   {"/static/js/mode_description.js"},
	"detail.html": {"/static/js/script_editor.js", "/static/js/job_status.js"},
}

// base builds the values every page shares.
//
// The CSRF token is the one CSRFContextMiddleware put on the context.
// Forms send it back as a hidden field, and the middleware verifies it.
func (h *Handler) base(r *http.Request) baseView {
	return baseView{
		CSRFToken:    session.CSRFTokenFromContext(r.Context()),
		Path:         r.URL.Path,
		DefaultModel: h.defaultModel(),
	}
}

// submit gathers the procedure for handing a run to the worker side.
//
// Validate → record queued → enqueue. The order has reasons (for recording first see
// recordQueued, for validating before the enqueue see Request.Validate), and when it was copied to
// 5 places one of them lacked validation (the detail page's save path). The page still
// worked normally and only tasks that always fail piled up, so there was no chance to notice.
//
// It returns an HTTP status ready to use. How a failure is returned (re-rendering the page or
// returning JSON) differs per caller, but the decision of which stage failed is the same.
func (h *Handler) submit(ctx context.Context, req domain.Request) (int, error) {
	if err := req.Validate(); err != nil {
		return http.StatusBadRequest, err
	}
	h.recordQueued(ctx, req)
	if err := h.queue.Enqueue(ctx, req); err != nil {
		return http.StatusBadGateway, err
	}
	return http.StatusAccepted, nil
}

// recordQueued records the enqueue. It is called before enqueue.
//
// Cloud Tasks delivers within tens of milliseconds, so in the opposite order a late queued could
// overwrite the running the worker wrote. The enqueue proceeds even if recording fails.
func (h *Handler) recordQueued(ctx context.Context, req domain.Request) {
	if h.status == nil {
		return
	}
	status := domain.NewJobStatus(req, jobstatus.StateQueued)
	// The listing sorts by this. Without it every entry gets the zero value and newest-first
	// does not hold. On a regeneration CarryOver keeps the first enqueue time, so
	// the history position does not move (the enqueue time is known only here, so
	// it is added here rather than in NewJobStatus).

	h.status.Record(ctx, req.JobID, status, func(next, prev *domain.JobStatus) {
		// On a regeneration, values this construction cannot know are kept (the list is CarryFrom).
		next.CarryFrom(prev)
	})
}

// defaultModel returns the head of the list. NewHandler rejects an empty list.
func (h *Handler) defaultModel() string {
	if len(h.models) == 0 {
		return ""
	}
	return h.models[0]
}

// renderTemplate renders the given page. name is the page's file name.
//
// It writes to a buffer before writing out. If rendering failed midway, the headers would already
// be sent, the status could not change, and broken HTML would remain.
func (h *Handler) renderTemplate(w http.ResponseWriter, status int, name string, view pageView) {
	view.setScripts(pageScripts[name])

	tmpl, ok := h.templates[name]
	if !ok {
		slog.Error("page template not found", "page", name)
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, assets.PageTemplate, view); err != nil {
		slog.Error("failed to render the page", "page", name, "error", err)
		http.Error(w, "画面の描画に失敗しました", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
