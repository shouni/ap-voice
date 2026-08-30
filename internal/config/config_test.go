package config

import (
	"maps"
	"strings"
	"testing"
	"time"
)

// managedEnvKeys は loadFor が面倒を見る環境変数です。
// 渡されなかったものも必ず空にします。実行環境に値が残っていると、
// テストが手元でだけ通る（あるいは落ちる）ためです。
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

// essentialEnv は、どのロールでも要る最低限です。
var essentialEnv = map[string]string{
	"GEMINI_MODELS":    "model-a",
	"GCP_PROJECT_ID":   "proj",
	"GCS_VOICE_BUCKET": "ap-voice",
	// 三段のタイムアウトはデプロイ設定が決めるため、アプリは既定値を持ちません。
	// PIPELINE_TIMEOUT が要るのは worker 面だけですが、web 面では無視されるので
	// ここにまとめて置きます。
	"TASK_DISPATCH_DEADLINE": "30m",
	"PIPELINE_TIMEOUT":       "25m",
}

// webEnv は Web 面が起動できる一式を返します。overrides で個別に潰せます。
func webEnv(overrides map[string]string) map[string]string {
	envs := map[string]string{
		"SERVER_ROLE":                       "web",
		"CLOUD_TASKS_QUEUE_ID":              "voice-queue",
		"WORKER_URL":                        "https://ap-voice-worker.example.run.app/tasks/generate",
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

// setEnv は managedEnvKeys を envs の内容で差し替えます。
// SERVER_ROLE は指定が無ければ both で埋めます。役割の検証そのものを見るテスト以外は
// 役割に関心が無く、全ケースに書くと本題が埋もれるためです。
func setEnv(t *testing.T, envs map[string]string) {
	t.Helper()

	for _, key := range managedEnvKeys {
		t.Setenv(key, envs[key])
	}
	if envs["SERVER_ROLE"] == "" {
		t.Setenv("SERVER_ROLE", "both")
	}
}

// loadFor は環境変数を差し替えたうえで LoadConfig を呼びます。
func loadFor(t *testing.T, envs map[string]string) *Config {
	t.Helper()

	setEnv(t, envs)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	return cfg
}

// モデル一覧はカンマ区切りで、先頭が既定モデルになります。
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

	// 単数形は環境変数からではなく一覧の先頭から埋まります。
	if cfg.AI.GeminiModel != "model-a" {
		t.Errorf("GeminiModel = %q, want model-a", cfg.AI.GeminiModel)
	}
}

// 既定値へ黙って落ちると、古いモデルを使い続けたまま気付けません。
func TestValidateEssentialConfig_ModelsRequired(t *testing.T) {
	cfg := loadFor(t, map[string]string{"GCP_PROJECT_ID": "proj"})

	err := cfg.ValidateEssentialConfig()
	if err == nil {
		t.Fatal("モデル名未指定が素通りした")
	}
	if !strings.Contains(err.Error(), "GEMINI_MODELS") {
		t.Errorf("エラーに指定方法が無い: %v", err)
	}
}

// Gemini は Vertex AI 経由でのみ呼ぶため、ProjectID が無ければ起動できません。
func TestValidateEssentialConfig_ProjectIDRequired(t *testing.T) {
	t.Run("未設定なら落ちる", func(t *testing.T) {
		cfg := loadFor(t, map[string]string{"GEMINI_MODELS": "model-a"})

		err := cfg.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("ProjectID の未設定が素通りした")
		}
		if !strings.Contains(err.Error(), "GCP_PROJECT_ID") {
			t.Errorf("エラーに指定方法が無い: %v", err)
		}
	})

	t.Run("揃っていれば通る", func(t *testing.T) {
		cfg := loadFor(t, webEnv(nil))
		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("ValidateEssentialConfig() = %v, want nil", err)
		}
	})
}

// VOICEVOX_API_URL は未設定を許します。go-voicevox 側が localhost:50021 へ落とし、
// ローカル実行とサイドカー構成のどちらもその値でよいためです。
func TestLoadConfig_VoicevoxAPIURL(t *testing.T) {
	t.Run("前後の空白を落とす", func(t *testing.T) {
		cfg := loadFor(t, map[string]string{"VOICEVOX_API_URL": "  https://voicevox.example.run.app  "})
		if cfg.Voicevox.APIURL != "https://voicevox.example.run.app" {
			t.Fatalf("APIURL = %q, want https://voicevox.example.run.app", cfg.Voicevox.APIURL)
		}
	})

	t.Run("未設定なら空のまま", func(t *testing.T) {
		cfg := loadFor(t, nil)
		if cfg.Voicevox.APIURL != "" {
			t.Fatalf("APIURL = %q, want empty", cfg.Voicevox.APIURL)
		}
	})
}

// 空文字を渡された場合も既定値で埋まることを確かめます。
// envDefault は変数が未設定のときしか効かないため、normalize 側が担っています。
func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadFor(t, nil)

	if cfg.GCP.LocationID != defaultLocationID {
		t.Errorf("LocationID = %q, want %q", cfg.GCP.LocationID, defaultLocationID)
	}
	if cfg.HTTP.Timeout != DefaultHTTPTimeout {
		t.Errorf("HTTP.Timeout = %v, want %v", cfg.HTTP.Timeout, DefaultHTTPTimeout)
	}
}

// SERVER_ROLE は明示が必須です。未設定を both とみなすと、環境変数が1つ欠けただけで
// 公開している Web 面に Worker のルートが復活します。
func TestLoadConfig_ServerRoleRequired(t *testing.T) {
	for _, tt := range []struct {
		name      string
		raw       string
		ok        bool
		wantInErr string
	}{
		// 値が無いとデコーダは UnmarshalText を呼ばないため、normalize が
		// 環境変数名つきで返します。
		{name: "未設定は落ちる", raw: "", ok: false, wantInErr: "SERVER_ROLE"},
		// 未知の値は env のデコード時点で弾かれます（serverrole.Role が
		// encoding.TextUnmarshaler を実装しているため）。env が包むので環境変数名では
		// なくフィールド名が出ますが、何が不正で何なら通るかは残ります。
		{name: "未知の値は落ちる", raw: "batch", ok: false, wantInErr: `"batch"`},
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
					t.Fatal("不正な SERVER_ROLE が素通りした")
				}
				if !strings.Contains(err.Error(), tt.wantInErr) {
					t.Errorf("エラーに %q が無い: %v", tt.wantInErr, err)
				}
				// どちらの経路でも、通る値が何かは示されている必要があります。
				if !strings.Contains(err.Error(), `"web"`) {
					t.Errorf("エラーに選択肢が無い: %v", err)
				}
			}
		})
	}
}

// Cloud Tasks の検証設定は Worker 面だけの要件です。Web 面に要求すると、
// 担当しない面の設定まで配ることになります。
func TestValidateEssentialConfig_WorkerOnlyRequirements(t *testing.T) {
	base := essentialEnv

	t.Run("worker は許可リストが要る", func(t *testing.T) {
		envs := map[string]string{"SERVER_ROLE": "worker"}
		maps.Copy(envs, base)
		cfg := loadFor(t, envs)

		err := cfg.ValidateEssentialConfig()
		if err == nil {
			t.Fatal("許可リストの未設定が素通りした")
		}
		if !strings.Contains(err.Error(), "ALLOWED_TASK_SERVICE_ACCOUNTS") {
			t.Errorf("エラーに指定方法が無い: %v", err)
		}
	})

	t.Run("web は許可リストが要らない", func(t *testing.T) {
		cfg := loadFor(t, webEnv(nil))

		if err := cfg.ValidateEssentialConfig(); err != nil {
			t.Fatalf("ValidateEssentialConfig() = %v, want nil", err)
		}
	})

	t.Run("worker も揃っていれば通る", func(t *testing.T) {
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

// TASK_AUDIENCE_URL の未指定は SERVICE_URL で埋めます。
func TestLoadConfig_TaskAudienceFallsBackToServiceURL(t *testing.T) {
	cfg := loadFor(t, map[string]string{"SERVICE_URL": "https://ap-voice.example.run.app"})

	if cfg.Tasks.TaskAudienceURL != "https://ap-voice.example.run.app" {
		t.Fatalf("TaskAudienceURL = %q, want the SERVICE_URL value", cfg.Tasks.TaskAudienceURL)
	}
}

// 明示された値は既定値に上書きされません。
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

// タスク投入と OAuth は Web 面だけの要件です。Worker 面に要求すると、
// 使わない認証情報へのアクセス権を配ることになります。
func TestValidateEssentialConfig_WebOnlyRequirements(t *testing.T) {
	for _, tt := range []struct {
		name    string
		missing string
		wantIn  string
	}{
		{name: "キュー名", missing: "CLOUD_TASKS_QUEUE_ID", wantIn: "CLOUD_TASKS_QUEUE_ID"},
		{name: "投入先", missing: "WORKER_URL", wantIn: "WORKER_URL"},
		{name: "caller SA", missing: "TASK_CALLER_SERVICE_ACCOUNT_EMAIL", wantIn: "TASK_CALLER_SERVICE_ACCOUNT_EMAIL"},
		{name: "OAuth クライアント", missing: "GOOGLE_CLIENT_ID", wantIn: "GOOGLE_CLIENT_ID"},
		{name: "セッション鍵", missing: "SESSION_ENCRYPT_KEY", wantIn: "SESSION_ENCRYPT_KEY"},
		{name: "許可リスト", missing: "ALLOWED_EMAILS", wantIn: "許可された"},
	} {
		t.Run(tt.name+"が無いと落ちる", func(t *testing.T) {
			cfg := loadFor(t, webEnv(map[string]string{tt.missing: ""}))

			err := cfg.ValidateEssentialConfig()
			if err == nil {
				t.Fatalf("%s の未設定が素通りした", tt.missing)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("エラーに %q が無い: %v", tt.wantIn, err)
			}
		})
	}

	// セッション鍵は AES の要件で長さが決まっています。
	t.Run("セッション鍵の長さが不正なら落ちる", func(t *testing.T) {
		cfg := loadFor(t, webEnv(map[string]string{"SESSION_ENCRYPT_KEY": "みじかい"}))
		if err := cfg.ValidateEssentialConfig(); err == nil {
			t.Fatal("不正な長さの鍵が素通りした")
		}
	})

	// worker 面は上記をどれも要求しません。
	t.Run("worker は Web 面の設定を要求しない", func(t *testing.T) {
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

// 出力先バケットは両ロールで要ります。web は履歴の一覧と出力先の組み立てに、
// worker は synthesize が保存済み台本を読むために使います。
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
				t.Fatal("バケット未設定が素通りした")
			}
			if !strings.Contains(err.Error(), "GCS_VOICE_BUCKET") {
				t.Errorf("エラーに指定方法が無い: %v", err)
			}
		})
	}
}

// 合成の流量はエンジンの大きさで変わるため env で調整できます。
// 未設定なら既定値が入り、コードを触らずに絞れることを固定します。
func TestLoadConfig_VoicevoxThroughput(t *testing.T) {
	t.Run("未設定なら既定値", func(t *testing.T) {
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

	// 実測が覆ったときに再ビルド無しで戻す、という操作が env だけで済むこと。
	// 既定と違う値を置きます。既定と同じ値だと、env が無視されていても通ります。
	t.Run("上書きできる", func(t *testing.T) {
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

// worker では未設定を落とすこと。無制限だと Cloud Tasks が先に打ち切り、
// 失敗の記録も通知も残らないまま再試行もされません。
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
		t.Fatal("未設定が素通りしました")
	}
	if !strings.Contains(err.Error(), "PIPELINE_TIMEOUT") {
		t.Errorf("エラーに変数名がありません: %v", err)
	}
}
