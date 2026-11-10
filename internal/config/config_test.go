package config

import (
	"maps"
	"strings"
	"testing"
	"time"
)

// managedEnvKeys are the environment variables loadFor takes care of.
// Those not passed are always emptied. A value left in the environment would make
// a test pass (or fail) only on one machine.
var managedEnvKeys = []string{
	"SERVER_ROLE", "SERVICE_URL", "PORT",
	"CLOUD_TASKS_QUEUE_ID", "WORKER_URL", "TASK_AUDIENCE_URL",
	"TASK_CALLER_SERVICE_ACCOUNT_EMAIL", "ALLOWED_TASK_SERVICE_ACCOUNTS",
	"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "SESSION_SECRET", "SESSION_ENCRYPT_KEY",
	"ALLOWED_EMAILS", "ALLOWED_DOMAINS",
	"GCP_PROJECT_ID", "GCP_LOCATION_ID", "GEMINI_MODELS", "GCS_VOICE_BUCKET",
	"VOICEVOX_API_URL", "VOICEVOX_MAX_PARALLEL_SEGMENTS",
	"VOICEVOX_SEGMENT_RATE_LIMIT", "VOICEVOX_SEGMENT_TIMEOUT",
	"SLACK_WEBHOOK_URL", "HTTP_TIMEOUT",
	"PIPELINE_TIMEOUT", "TASK_DISPATCH_DEADLINE",
}

// essentialEnv is the minimum every role needs.
var essentialEnv = map[string]string{
	"GEMINI_MODELS":    "model-a",
	"GCP_PROJECT_ID":   "proj",
	"GCS_VOICE_BUCKET": "ap-voice",
	// The deployment decides the three timeout tiers, so the app has no defaults.
	// Only the worker side needs PIPELINE_TIMEOUT, but the web side ignores it, so
	// it is set here for both.
	"TASK_DISPATCH_DEADLINE": "30m",
	"PIPELINE_TIMEOUT":       "25m",
}

// webEnv returns a full set the web side can start with. overrides can blank individual values.
func webEnv(overrides map[string]string) map[string]string {
	envs := map[string]string{
		"SERVER_ROLE":                       "web",
		"CLOUD_TASKS_QUEUE_ID":              "voice-queue",
		"WORKER_URL":                        "https://ap-voice-worker.example.run.app",
		"TASK_CALLER_SERVICE_ACCOUNT_EMAIL": "web-runner@example.iam.gserviceaccount.com",
		"GOOGLE_CLIENT_ID":                  "client-id",
		"GOOGLE_CLIENT_SECRET":              "client-secret",
		"SESSION_SECRET":                    "session-secret",
		"SESSION_ENCRYPT_KEY":               "0123456789abcdef",
		"ALLOWED_EMAILS":                    "someone@example.com",
	}
	maps.Copy(envs, essentialEnv)
	maps.Copy(envs, overrides)
	return envs
}

// setEnv replaces managedEnvKeys with the contents of envs.
// SERVER_ROLE is filled with both unless given. Tests other than those about role validation
// do not care about the role, and writing it in every case would bury the point.
func setEnv(t *testing.T, envs map[string]string) {
	t.Helper()

	for _, key := range managedEnvKeys {
		t.Setenv(key, envs[key])
	}
	if envs["SERVER_ROLE"] == "" {
		t.Setenv("SERVER_ROLE", "both")
	}
}

// loadFor swaps the environment variables and calls LoadConfig.
func loadFor(t *testing.T, envs map[string]string) *Config {
	t.Helper()

	setEnv(t, envs)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	return cfg
}

// The model list is comma-separated, and the first is the default model.
func TestLoadConfig_GeminiModels(t *testing.T) {
	cfg := loadFor(t, map[string]string{
		"GEMINI_MODELS":  " model-a , model-b ,, model-a , model-c ",
		"GCP_PROJECT_ID": "proj",
	})

	want := []string{"model-a", "model-b", "model-c"}
	if len(cfg.AI.GeminiModels) != len(want) {
		t.Fatalf("GeminiModels = %v, want %v", cfg.AI.GeminiModels, want)
	}
	for i, w := range want {
		if cfg.AI.GeminiModels[i] != w {
			t.Errorf("GeminiModels[%d] = %q, want %q", i, cfg.AI.GeminiModels[i], w)
		}
	}

	// The singular is filled from the head of the list, not from an environment variable.
	if cfg.AI.GeminiModel != "model-a" {
		t.Errorf("GeminiModel = %q, want model-a", cfg.AI.GeminiModel)
	}
}

// Silently falling back to a default would keep an outdated model in use unnoticed.
func TestValidateEssentialConfig_ModelsRequired(t *testing.T) {
	cfg := loadFor(t, map[string]string{"GCP_PROJECT_ID": "proj"})

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("a missing model name got through")
	}
	if !strings.Contains(err.Error(), "GEMINI_MODELS") {
		t.Errorf("the error does not say how to set it: %v", err)
	}
}

// Gemini is called only through Vertex AI, so it cannot start without ProjectID.
func TestValidateEssentialConfig_ProjectIDRequired(t *testing.T) {
	t.Run("fails when unset", func(t *testing.T) {
		cfg := loadFor(t, map[string]string{"GEMINI_MODELS": "model-a"})

		err := cfg.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("a missing ProjectID got through")
		}
		if !strings.Contains(err.Error(), "GCP_PROJECT_ID") {
			t.Errorf("the error does not say how to set it: %v", err)
		}
	})

	t.Run("passes when complete", func(t *testing.T) {
		cfg := loadFor(t, webEnv(nil))
		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("ValidateEssentialConfig() = %v, want nil", err)
		}
	})
}

// VOICEVOX_API_URL may be unset. go-voicevox falls back to localhost:50021,
// which suits both local runs and the sidecar setup.
func TestLoadConfig_VoicevoxAPIURL(t *testing.T) {
	t.Run("trims surrounding whitespace", func(t *testing.T) {
		cfg := loadFor(t, map[string]string{"VOICEVOX_API_URL": "  https://voicevox.example.run.app  "})
		if cfg.Voicevox.APIURL != "https://voicevox.example.run.app" {
			t.Fatalf("APIURL = %q, want https://voicevox.example.run.app", cfg.Voicevox.APIURL)
		}
	})

	t.Run("stays empty when unset", func(t *testing.T) {
		cfg := loadFor(t, nil)
		if cfg.Voicevox.APIURL != "" {
			t.Fatalf("APIURL = %q, want empty", cfg.Voicevox.APIURL)
		}
	})
}

// Checks that an empty string is also filled with the default.
// envDefault only applies when the variable is unset, so normalize takes care of it.
func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadFor(t, nil)

	if cfg.GCP.LocationID != defaultLocationID {
		t.Errorf("LocationID = %q, want %q", cfg.GCP.LocationID, defaultLocationID)
	}
	if cfg.HTTP.Timeout != DefaultHTTPTimeout {
		t.Errorf("HTTP.Timeout = %v, want %v", cfg.HTTP.Timeout, DefaultHTTPTimeout)
	}
}

// SERVER_ROLE must be explicit. Treating unset as both would bring the worker routes back on the
// public web side just because one environment variable went missing.
func TestLoadConfig_ServerRoleRequired(t *testing.T) {
	for _, tt := range []struct {
		name      string
		raw       string
		ok        bool
		wantInErr string
	}{
		// Without a value the decoder does not call UnmarshalText, so normalize
		// returns the error with the environment variable name.
		{name: "unset fails", raw: "", ok: false, wantInErr: "SERVER_ROLE"},
		// An unknown value is rejected while env decodes it (serverrole.Role implements
		// encoding.TextUnmarshaler). env wraps it, so the field name appears rather than the
		// environment variable name, but what was invalid and what would pass remain.
		{name: "unknown value fails", raw: "batch", ok: false, wantInErr: `"batch"`},
		{name: "web", raw: "web", ok: true},
		{name: "worker", raw: "worker", ok: true},
		{name: "both", raw: "both", ok: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range managedEnvKeys {
				t.Setenv(key, "")
			}
			t.Setenv("SERVER_ROLE", tt.raw)

			_, err := LoadConfig()
			if tt.ok && err != nil {
				t.Fatalf("LoadConfig() = %v, want nil", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatal("an invalid SERVER_ROLE got through")
				}
				if !strings.Contains(err.Error(), tt.wantInErr) {
					t.Errorf("the error lacks %q: %v", tt.wantInErr, err)
				}
				// On either path, the values that would pass must be shown.
				if !strings.Contains(err.Error(), `"web"`) {
					t.Errorf("the error lists no choices: %v", err)
				}
			}
		})
	}
}

// The Cloud Tasks verification settings are a worker-only requirement. Requiring them of the web side
// would hand out settings for a side it does not serve.
func TestValidateEssentialConfig_WorkerOnlyRequirements(t *testing.T) {
	base := essentialEnv

	t.Run("worker needs the allowlist", func(t *testing.T) {
		envs := map[string]string{"SERVER_ROLE": "worker"}
		maps.Copy(envs, base)
		cfg := loadFor(t, envs)

		err := cfg.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("a missing allowlist got through")
		}
		if !strings.Contains(err.Error(), "ALLOWED_TASK_SERVICE_ACCOUNTS") {
			t.Errorf("the error does not say how to set it: %v", err)
		}
	})

	t.Run("web does not need the allowlist", func(t *testing.T) {
		cfg := loadFor(t, webEnv(nil))

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("ValidateEssentialConfig() = %v, want nil", err)
		}
	})

	t.Run("worker passes when complete", func(t *testing.T) {
		envs := map[string]string{
			"SERVER_ROLE":                   "worker",
			"TASK_AUDIENCE_URL":             "https://ap-voice-worker.example.run.app",
			"ALLOWED_TASK_SERVICE_ACCOUNTS": "web-runner@example.iam.gserviceaccount.com",
		}
		maps.Copy(envs, base)
		cfg := loadFor(t, envs)

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("ValidateEssentialConfig() = %v, want nil", err)
		}
	})
}

// A missing TASK_AUDIENCE_URL is filled from SERVICE_URL.
func TestLoadConfig_TaskAudienceFallsBackToServiceURL(t *testing.T) {
	cfg := loadFor(t, map[string]string{"SERVICE_URL": "https://ap-voice.example.run.app"})

	if cfg.Tasks.TaskAudienceURL != "https://ap-voice.example.run.app" {
		t.Fatalf("TaskAudienceURL = %q, want the SERVICE_URL value", cfg.Tasks.TaskAudienceURL)
	}
}

// An explicit value is not overwritten by the default.
func TestLoadConfig_ExplicitValuesWin(t *testing.T) {
	cfg := loadFor(t, map[string]string{
		"GCP_LOCATION_ID": "asia-northeast1",
		"HTTP_TIMEOUT":    "15s",
	})

	if cfg.GCP.LocationID != "asia-northeast1" {
		t.Errorf("LocationID = %q, want asia-northeast1", cfg.GCP.LocationID)
	}
	if cfg.HTTP.Timeout != 15*time.Second {
		t.Errorf("HTTP.Timeout = %v, want 15s", cfg.HTTP.Timeout)
	}
}

// Task enqueueing and OAuth are web-only requirements. Requiring them of the worker side
// would hand out access to credentials it never uses.
func TestValidateEssentialConfig_WebOnlyRequirements(t *testing.T) {
	for _, tt := range []struct {
		name    string
		missing string
		wantIn  string
	}{
		{name: "queue name", missing: "CLOUD_TASKS_QUEUE_ID", wantIn: "CLOUD_TASKS_QUEUE_ID"},
		{name: "worker URL", missing: "WORKER_URL", wantIn: "WORKER_URL"},
		{name: "caller SA", missing: "TASK_CALLER_SERVICE_ACCOUNT_EMAIL", wantIn: "TASK_CALLER_SERVICE_ACCOUNT_EMAIL"},
		{name: "OAuth client", missing: "GOOGLE_CLIENT_ID", wantIn: "GOOGLE_CLIENT_ID"},
		{name: "allowlist", missing: "ALLOWED_EMAILS", wantIn: "allowed email"},
	} {
		t.Run(tt.name+" missing fails", func(t *testing.T) {
			cfg := loadFor(t, webEnv(map[string]string{tt.missing: ""}))

			err := cfg.ValidateEssentialConfig()
			if err == nil {
				t.Fatalf("a missing %s got through", tt.missing)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("the error lacks %q: %v", tt.wantIn, err)
			}
		})
	}

	// The worker side requires none of the above.
	t.Run("worker does not require the web settings", func(t *testing.T) {
		envs := map[string]string{
			"SERVER_ROLE":                   "worker",
			"TASK_AUDIENCE_URL":             "https://ap-voice-worker.example.run.app",
			"ALLOWED_TASK_SERVICE_ACCOUNTS": "web-runner@example.iam.gserviceaccount.com",
		}
		maps.Copy(envs, essentialEnv)
		cfg := loadFor(t, envs)

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("ValidateEssentialConfig() = %v, want nil", err)
		}
	})
}

// The output bucket is needed by both roles: web to list the history and build output locations,
// the worker for synthesize to read stored scripts.
func TestValidateEssentialConfig_BucketRequiredForBothRoles(t *testing.T) {
	for _, role := range []string{"web", "worker", "both"} {
		t.Run(role, func(t *testing.T) {
			envs := webEnv(map[string]string{
				"SERVER_ROLE":                   role,
				"GCS_VOICE_BUCKET":              "",
				"TASK_AUDIENCE_URL":             "https://worker.example.run.app",
				"ALLOWED_TASK_SERVICE_ACCOUNTS": "caller@example.iam.gserviceaccount.com",
			})
			cfg := loadFor(t, envs)

			err := cfg.ValidateEssentialConfig()
			if err == nil {
				t.Fatal("a missing bucket got through")
			}
			if !strings.Contains(err.Error(), "GCS_VOICE_BUCKET") {
				t.Errorf("the error does not say how to set it: %v", err)
			}
		})
	}
}

// Synthesis throughput depends on the size of the engine, so it can be tuned through env.
// This pins that unset gives the defaults, so it can be throttled without touching code.
func TestLoadConfig_VoicevoxThroughput(t *testing.T) {
	t.Run("defaults when unset", func(t *testing.T) {
		cfg := loadFor(t, nil)

		if cfg.Voicevox.MaxParallelSegments != DefaultMaxParallelSegments {
			t.Errorf("MaxParallelSegments = %d, want %d", cfg.Voicevox.MaxParallelSegments, DefaultMaxParallelSegments)
		}
		if cfg.Voicevox.SegmentRateLimit != DefaultSegmentRateLimit {
			t.Errorf("SegmentRateLimit = %v, want %v", cfg.Voicevox.SegmentRateLimit, DefaultSegmentRateLimit)
		}
		if cfg.Voicevox.SegmentTimeout != DefaultSegmentTimeout {
			t.Errorf("SegmentTimeout = %v, want %v", cfg.Voicevox.SegmentTimeout, DefaultSegmentTimeout)
		}
	})

	// When measurements overturn a value, reverting it without a rebuild must take only env.
	// It sets a value different from the default; with the default value it would pass even if env were ignored.
	t.Run("can be overridden", func(t *testing.T) {
		cfg := loadFor(t, map[string]string{
			"VOICEVOX_MAX_PARALLEL_SEGMENTS": "8",
			"VOICEVOX_SEGMENT_RATE_LIMIT":    "1s",
			"VOICEVOX_SEGMENT_TIMEOUT":       "90s",
		})

		if cfg.Voicevox.MaxParallelSegments != 8 {
			t.Errorf("MaxParallelSegments = %d, want 8", cfg.Voicevox.MaxParallelSegments)
		}
		if cfg.Voicevox.SegmentRateLimit != time.Second {
			t.Errorf("SegmentRateLimit = %v, want 1s", cfg.Voicevox.SegmentRateLimit)
		}
		if cfg.Voicevox.SegmentTimeout != 90*time.Second {
			t.Errorf("SegmentTimeout = %v, want 90s", cfg.Voicevox.SegmentTimeout)
		}
	})
}

// The worker must fail when it is unset. Unlimited, Cloud Tasks cuts the request first, and
// nothing is recorded or notified, and it is not retried either.
func TestPipelineTimeoutRequiredOnWorker(t *testing.T) {
	envs := map[string]string{
		"SERVER_ROLE":                   "worker",
		"TASK_AUDIENCE_URL":             "https://ap-voice-worker.example.run.app",
		"ALLOWED_TASK_SERVICE_ACCOUNTS": "web-runner@example.iam.gserviceaccount.com",
		"PIPELINE_TIMEOUT":              "",
	}
	for k, v := range essentialEnv {
		if k != "PIPELINE_TIMEOUT" {
			envs[k] = v
		}
	}
	cfg := loadFor(t, envs)

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("unset got through")
	}
	if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
		t.Errorf("the error lacks the variable name: %v", err)
	}
}
