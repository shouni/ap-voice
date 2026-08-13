package config

import (
	"strings"
	"testing"
	"time"
)

// loadFor は環境変数を差し替えたうえで LoadConfig を呼びます。
// 差し替えたキーは、渡されなかったものも含めて必ず空にします。
// 実行環境に値が残っていると、テストが手元でだけ通る（あるいは落ちる）ためです。
func loadFor(t *testing.T, envs map[string]string) *Config {
	t.Helper()

	for _, key := range []string{
		"GCP_PROJECT_ID", "GCP_LOCATION_ID", "GEMINI_MODELS",
		"VOICEVOX_API_URL", "SLACK_WEBHOOK_URL", "HTTP_TIMEOUT",
	} {
		t.Setenv(key, envs[key])
	}

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
		cfg := loadFor(t, map[string]string{"GEMINI_MODELS": "model-a", "GCP_PROJECT_ID": "proj"})
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
