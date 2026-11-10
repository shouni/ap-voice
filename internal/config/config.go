// Package config loads and validates the application configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-serve-kit/serverrole"
	"github.com/shouni/go-utils/strlist"
)

const (
	// DefaultHTTPTimeout is the default timeout for outgoing HTTP calls.
	// It bounds one HTTP round trip, not one segment (see HTTPConfig).
	DefaultHTTPTimeout = 60 * time.Second
	// DefaultMaxParallelSegments is the default number of segments sent concurrently within one job.
	// It matches the engine's vCPU count (see the measurements on VoicevoxConfig below).
	DefaultMaxParallelSegments = 4
	// DefaultSegmentRateLimit is the default interval between segment submissions.
	DefaultSegmentRateLimit = 100 * time.Millisecond
	// DefaultSegmentTimeout is the default limit per segment.
	DefaultSegmentTimeout = 120 * time.Second
	// defaultLocationID is the default Cloud Tasks region.
	// The whole fleet uses asia-northeast1.
	defaultLocationID = "asia-northeast1"
)

// ServerConfig is the HTTP server configuration.
type ServerConfig struct {
	ServiceURL string `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role is the role this process plays. It must be explicit; unset is a startup error.
	// The graceful shutdown period is left to the kit's default (a value within Cloud Run's SIGKILL grace).
	Role serverrole.Role `env:"SERVER_ROLE"`
}

// TasksConfig is the Cloud Tasks queue configuration and the OIDC verification settings for receiving.
// These settings are specific to Cloud Tasks, neither general GCP nor HTTP server settings, so
// they are gathered here as in the sibling apps.
type TasksConfig struct {
	QueueID         string `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"`
	// CallerServiceAccountEmail is the caller SA set as oidcToken.serviceAccountEmail on enqueued
	// tasks. It is Cloud Tasks that mints and attaches the token;
	// this process signs nothing. It is a setting for the enqueuing side, the web side, only.
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// DispatchDeadline is the response-wait limit stamped on enqueued tasks.
	//
	// It is not "time spent waiting" but the effective cap on the worker's run time. Past it,
	// Cloud Tasks stops waiting even while the worker is still processing. However far Cloud Run's
	// timeout is raised, this cap does not move.
	DispatchDeadline time.Duration `env:"TASK_DISPATCH_DEADLINE"`
	// AllowedServiceAccounts is the allowlist of caller SAs the worker accepts (comma-separated).
	// The receiver must allow the enqueuer's SA, not its own, and web and worker run as different
	// SAs, so the worker's list holds "someone else's SA".
	// Empty, the verifier fails closed, so it is required on the worker.
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
}

// GCPConfig is the Google Cloud Platform configuration.
//
// Gemini is called only through Vertex AI. There is no API key path (GEMINI_API_KEY).
// On Cloud Run the runtime SA's roles/aiplatform.user authenticates, so handing out a key would
// hand out access to a secret nobody uses. Local runs need
// ADC (gcloud auth application-default login).
type GCPConfig struct {
	ProjectID string `env:"GCP_PROJECT_ID"`
	// LocationID is the region where the Cloud Tasks queue lives (the deployment configuration
	// passes asia-northeast1). It is separate from the Vertex AI endpoint, which is
	// pinned in adapters.defaultVertexLocationID. Mixing them up either loses
	// the queue or points Vertex at a region where it does not exist.
	LocationID string `env:"GCP_LOCATION_ID"`
}

// AIConfig is the AI model configuration.
type AIConfig struct {
	// The model list is comma-separated and the first is the default model. LoadConfig fills the
	// singular from the head of the list, so it is not read from the environment.
	//
	// There is no default. Model IDs age on Google's release cycle, not
	// this repository's, so a default would keep an outdated model in use without anyone
	// noticing. Empty, ValidateEssentialConfig fails startup.
	GeminiModels []string `env:"GEMINI_MODELS"`
	GeminiModel  string   `env:"-"`
}

// VoicevoxConfig is the speech synthesis engine configuration.
//
// The three throughput values depend on the size of the deployed engine, so they live in env.
// That the engine is a 4 vCPU / 3 GiB sidecar is a fact held by the deployment configuration,
// not by this repository, and throttling to match it should not require a code build.
//
//	throughput = min(1/SegmentRateLimit, MaxParallelSegments ÷ time per segment)
//
// Measured on 2026-08-14 (12 segments / defaults unchanged / a 4 vCPU, 3 GiB sidecar)
//
//	elapsed     50.0 s (12 segments)  20-25 s per segment
//	effective   0.24 segments/s       the rate limit allows 2.0/s
//	CPU         2.31 / 5 vCPU         memory 0.94 / 4 GiB
//
// The right-hand term was the one in effect.
//
// On 2026-08-24 this went down to the level of parallelism, with Cloud Monitoring and Cloud Logging.
// The progress log prints the character count and duration of every segment, so the synthesis
// time per character can be computed per effective parallelism (a batch's effective parallelism is min(segments, 8)).
//
//	parallelism  samples  ms/char (median)  throughput
//	~4            7       233               17.2 chars/s
//	8           108       430               18.6 chars/s
//
// Throughput is flat, and only the time per segment doubles. 430 being about twice
// 233, matching the 8/4 parallelism ratio, is the signature of saturation. It also fits CPU
// utilisation at p99 of 3.7 / 5 vCPU (4 of which are allocated to the engine).
// Synthesis is CPU-bound, so 4 vCPUs are used up by 4 concurrent requests, as reason says.
// The default was lowered from 8 to 4 on the strength of this measurement.
//
// The weak points are recorded too. The low-parallelism side has only 7 samples (the progress log
// prints every 5 segments plus the last, so a 4-segment batch yields only one). In addition,
// small batches may land on startup_cpu_boost right after a cold start, which would make
// low parallelism look better than it is. That bias works against the change to 4, so
// if durations get worse, set it back to 8 through env and measure again.
//
// On 2026-08-24 this was confirmed against 90 days of Cloud Logging (29 batches),
// comparing the segment_duration_avg/min/max go-voicevox logs at the end of a batch with the
// difference from the start log (the measured duration).
//
//	batch size    4-60 segments
//	per segment   mean 2.0-35.3 s (minimum 1.0 s)
//	duration      tracks waves × mean segment time closely
//
// From the single run above, this used to say "the rate limit has 8x headroom",
// but that held only for long scripts. The old 500ms default becomes the bottleneck when
// a segment takes less than parallelism × interval = 4 seconds; 13 of the 29 batches had
// segments under 4 seconds, and in 4 batches the mean was under 4 seconds (2.0-3.2 s).
// Those 4 batches (all 4 segments) measured 4.5-6.4 seconds, while synthesis itself was
// 2.0-3.2 seconds. The shorter the script, the larger the share of waiting — it hit
// exactly where it is most noticeable.
type VoicevoxConfig struct {
	// When APIURL is empty, go-voicevox falls back to http://localhost:50021.
	// That value suits both local runs and the Cloud Run sidecar setup, so
	// unlike model names, unset is allowed.
	APIURL string `env:"VOICEVOX_API_URL"`

	// MaxParallelSegments is the number of segments sent concurrently within one job.
	//
	// This is what actually bounds the throughput above. The default is 4, the same as the
	// engine's vCPU count. Synthesis is CPU-bound, so 4 vCPUs are used up by 4 concurrent
	// requests. Measurements show that more does not raise throughput and only lengthens
	// the wait per segment (below).
	//
	// This value is the app's concern, not an infrastructure setting. The deployment configuration
	// decides the engine's size (4 vCPU / 3 GiB), but how to feed it is a synthesis strategy, so
	// it lives here. The deployment configuration does not set this env;
	// the env remains so it can be reverted — if measurements overturn it, it can be overridden with
	// VOICEVOX_MAX_PARALLEL_SEGMENTS without a rebuild.
	//
	// The peak does not rise with the script's length. The number in flight is capped here, so
	// 12 lines or 200, the memory peak is the same height; only the duration changes.
	MaxParallelSegments int `env:"VOICEVOX_MAX_PARALLEL_SEGMENTS"`

	// SegmentRateLimit is the interval between segment submissions.
	//
	// VOICEVOX has no API rate limit. It is an engine we run ourselves, and in the sidecar
	// setup it is in the same instance. This is not compliance with an external spec but
	// voluntary throttling so as not to hammer the engine.
	//
	// It is not the throughput knob. What bounds the number in flight is
	// MaxParallelSegments; this interval starts to matter only when
	// "time per segment < parallelism × interval", i.e. only once the measured
	// 20-25 seconds became faster than 1 second.
	//
	// It still has a cost. The burst is 1, so the head of every batch carries a wait of
	// (parallelism - 1) × interval. The old 500ms default cost 3.5 seconds at the then
	// parallelism of 8, and 1.5 seconds even at today's 4. That paid seconds per job for a throttle
	// that had no effect, so it is 100ms, the minimum that keeps the intent (not hitting the engine
	// all at once at startup) (0.3 seconds at parallelism 4).
	SegmentRateLimit time.Duration `env:"VOICEVOX_SEGMENT_RATE_LIMIT"`

	// SegmentTimeout is the limit per segment.
	// The sidecar listens from startup, so no margin for a cold start is needed.
	//
	// "One segment" is two round trips, the query (audio_query) and synthesis, each
	// with at most one retry. The limit per round trip is HTTP_TIMEOUT —
	// the smaller one takes effect first, so raising only this does not lengthen a round trip (see HTTPConfig).
	SegmentTimeout time.Duration `env:"VOICEVOX_SEGMENT_TIMEOUT"`
}

// PipelineConfig is the configuration for running one job.
type PipelineConfig struct {
	// Timeout is the cap on one job's run time.
	//
	// It is the shortest of the three tiers. By giving up first itself, the app can record the failure
	// and notify Slack before it ends. In the reverse order Cloud Tasks cuts the request
	// first, and the process dies on SIGTERM, losing the chance to notify. voice-queue has
	// max_attempts = 2, but unless this inequality holds the second attempt may overlap
	// the first. The re-run guard depends on redeliveries arriving serially.
	Timeout time.Duration `env:"PIPELINE_TIMEOUT"`
}

// StorageConfig is the storage configuration.
type StorageConfig struct {
	// GCSBucket is where artifacts go. Users do not enter the output location.
	// Deriving the path from the job ID guarantees one job's artifacts are gathered under one prefix,
	// so the history listing and deletion work without knowing the contents.
	GCSBucket string `env:"GCS_VOICE_BUCKET"`
	// MusicBucket is used to resolve the music promotion mode's input (a recipe.json in music.Recipe
	// form) from a job ID.
	// It is the same environment variable and rule as the video generation service's (gs://<MusicBucket>/music/<jobID>/recipe.json).
	// There are now two readers, so renaming it on one side would cause mix-ups when moving settings.
	// There is no default (as in the video generation service). The bucket name is the deployment's
	// to decide; unset, that tab just does not accept job IDs, and the other modes work.
	MusicBucket string `env:"AP_MUSIC_BUCKET"`
	// FirestoreDatabase is the Firestore database that holds job states.
	//
	// A named database is used so as not to occupy the (default) slot.
	// The collection name is not a setting (it is the service's identity, not a value that changes
	// per deployment; see statusCollection in repository).
	FirestoreDatabase string `env:"FIRESTORE_DATABASE" envDefault:"job-status"`
}

// AuthConfig is the authentication and authorization configuration. Only the web side reads it.
type AuthConfig struct {
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// SessionDatabase is the Firestore database that holds sessions.
	//
	// It is separate from the one for job states. A database name is an identifier that cannot be changed later,
	// so sharing one would leave its name wrong for one of the uses.
	SessionDatabase string `env:"SESSION_FIRESTORE_DATABASE" envDefault:"sessions"`
	// SessionCollection is the collection that holds sessions.
	SessionCollection string   `env:"SESSION_FIRESTORE_COLLECTION" envDefault:"sessions"`
	AllowedEmails     []string `env:"ALLOWED_EMAILS"`
	AllowedDomains    []string `env:"ALLOWED_DOMAINS"`

	// AllowedM2MServiceAccounts are the service accounts allowed when a machine rather than a browser
	// calls /api (the MCP server and so on).
	//
	// It starts even when empty. Unset, M2M verification always fails and every
	// request falls through to session authentication. So a setup that does not use the API
	// needs nothing, and a setup that does but forgot it shows up as "only the agent gets
	// sent to the login page". It is the same env name as in the sibling apps.
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
}

// NotificationConfig is the notification configuration.
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// HTTPConfig is the configuration for outgoing HTTP calls.
//
// Synthesis has a two-tier limit. This value bounds only one HTTP round trip
// (http.Client.Timeout); a segment is two round trips, query and synthesis, and
// go-http-kit retries each once (WithMaxRetries(1) in builder), so
// what bounds the whole segment is VOICEVOX_SEGMENT_TIMEOUT.
//
// 60 seconds and 120 seconds are not a contradiction but these two tiers. Do not raise the
// round-trip limit to the segment limit — the first round trip could then use up the budget,
// leaving no room to retry a round trip that hit the limit. At half, retrying a timed-out
// round trip still fits within the segment's budget.
//
// Beware of confusing the tiers. To lengthen one round trip beyond 60 seconds, raising
// VOICEVOX_SEGMENT_TIMEOUT does not work (the smaller one takes effect first).
// Conversely, raising this value affects notifications too — Slack is called through the same
// client (with only the retries removed), so the wait when the webhook does not respond
// grows with it.
//
// For reference, the longest measured segment is 35.3 seconds, far from either limit.
// What decides the duration is the parallelism (see VoicevoxConfig).
type HTTPConfig struct {
	// The default is filled by normalize, for the same reason as LocationID.
	Timeout time.Duration `env:"HTTP_TIMEOUT"`
}

// Config is the application configuration.
//
// It holds only values the deployment target decides; values that change per run, such as the input,
// the output location and the generation mode, are held by domain.Request. Mixing the two in one
// struct would make per-run values look as if they came from environment variables.
type Config struct {
	Server       ServerConfig
	Tasks        TasksConfig
	Pipeline     PipelineConfig
	Storage      StorageConfig
	Auth         AuthConfig
	GCP          GCPConfig
	AI           AIConfig
	Voicevox     VoicevoxConfig
	Notification NotificationConfig
	HTTP         HTTPConfig
}

// LoadConfig loads the configuration from environment variables.
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// normalize tidies up variations in how the loaded values are written.
func (c *Config) normalize() error {
	// Environment variable names are the app's concern, so context is added to the kit's error here.
	role, err := serverrole.Parse(string(c.Server.Role))
	if err != nil {
		return fmt.Errorf("SERVER_ROLE: %w", err)
	}
	c.Server.Role = role

	c.Server.ServiceURL = strings.TrimSpace(c.Server.ServiceURL)

	c.Tasks.QueueID = strings.TrimSpace(c.Tasks.QueueID)
	workerURL, err := normalizeWorkerURL(c.Server.Role, c.Tasks.WorkerURL, c.Server.ServiceURL)
	if err != nil {
		return err
	}
	c.Tasks.WorkerURL = workerURL
	c.Tasks.TaskAudienceURL = strings.TrimSpace(c.Tasks.TaskAudienceURL)
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Tasks.CallerServiceAccountEmail = strings.TrimSpace(c.Tasks.CallerServiceAccountEmail)
	c.Tasks.AllowedServiceAccounts = strlist.Normalize(c.Tasks.AllowedServiceAccounts)

	c.Auth.AllowedEmails = strlist.Normalize(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = strlist.Normalize(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = strlist.Normalize(c.Auth.AllowedM2MServiceAccounts)

	c.GCP.ProjectID = strings.TrimSpace(c.GCP.ProjectID)
	c.GCP.LocationID = strings.TrimSpace(c.GCP.LocationID)
	if c.GCP.LocationID == "" {
		c.GCP.LocationID = defaultLocationID
	}

	// env only splits on commas, so surrounding whitespace and duplicates are dropped here.
	c.AI.GeminiModels = strlist.Normalize(c.AI.GeminiModels)
	c.AI.GeminiModel = firstModel(c.AI.GeminiModels)

	c.Storage.GCSBucket = remoteio.NormalizeBucketName(c.Storage.GCSBucket)
	c.Storage.MusicBucket = remoteio.NormalizeBucketName(c.Storage.MusicBucket)
	c.Voicevox.APIURL = strings.TrimSpace(c.Voicevox.APIURL)
	if c.Voicevox.MaxParallelSegments <= 0 {
		c.Voicevox.MaxParallelSegments = DefaultMaxParallelSegments
	}
	if c.Voicevox.SegmentRateLimit <= 0 {
		c.Voicevox.SegmentRateLimit = DefaultSegmentRateLimit
	}
	if c.Voicevox.SegmentTimeout <= 0 {
		c.Voicevox.SegmentTimeout = DefaultSegmentTimeout
	}
	c.Notification.SlackWebhookURL = strings.TrimSpace(c.Notification.SlackWebhookURL)

	if c.HTTP.Timeout <= 0 {
		c.HTTP.Timeout = DefaultHTTPTimeout
	}

	return nil
}

// firstModel returns the head of the list (= the model used as the default).
// The list is empty only when misconfigured, and that value is rejected by startup validation before use.
func firstModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

// normalizeWorkerURL tidies the worker service URL tasks are delivered to. It returns only up to
// the service URL; the task path is appended just before enqueueing (internal/builder).
//
// Only a process that serves the worker side derives it from SERVICE_URL. On a split deployment
// SERVICE_URL is pinned to the public side, so deriving it on web alone would make every task 404.
func normalizeWorkerURL(role serverrole.Role, workerURL string, serviceURL string) (string, error) {
	workerURL = strings.TrimSpace(workerURL)
	if workerURL != "" {
		if _, err := url.Parse(workerURL); err != nil {
			return "", fmt.Errorf("invalid worker URL %q: %w", workerURL, err)
		}
		return workerURL, nil
	}
	if !role.ServesWorker() {
		return "", nil
	}

	serviceURL = strings.TrimSpace(serviceURL)
	if serviceURL == "" {
		return "", nil
	}
	if _, err := url.Parse(serviceURL); err != nil {
		return "", fmt.Errorf("invalid service URL %q: %w", serviceURL, err)
	}
	return serviceURL, nil
}
