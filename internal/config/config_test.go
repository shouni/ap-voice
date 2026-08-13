package config

import (
	"strings"
	"testing"
)

// モデル名はフラグと環境変数のどちらでも指定でき、フラグが勝ちます。
func TestFillDefaults_AIModel(t *testing.T) {
	t.Setenv("GEMINI_MODEL", "model-from-env")

	t.Run("フラグ指定があれば環境変数より優先する", func(t *testing.T) {
		c := &Config{AIModel: "model-from-flag"}
		c.FillDefaults(LoadConfig())

		if c.AIModel != "model-from-flag" {
			t.Fatalf("AIModel = %q, want model-from-flag", c.AIModel)
		}
	})

	t.Run("フラグ未指定なら環境変数で補完する", func(t *testing.T) {
		c := &Config{}
		c.FillDefaults(LoadConfig())

		if c.AIModel != "model-from-env" {
			t.Fatalf("AIModel = %q, want model-from-env", c.AIModel)
		}
	})
}

// VOICEVOX_API_URL は環境変数だけで指定します。README に載っていながら
// どこからも読まれていなかったため、経路が通っていることを固定します。
func TestFillDefaults_VoicevoxAPIURL(t *testing.T) {
	t.Run("環境変数から読み込む", func(t *testing.T) {
		t.Setenv("VOICEVOX_API_URL", "  https://voicevox.example.run.app  ")

		c := &Config{}
		c.FillDefaults(LoadConfig())
		c.Normalize()

		if c.VoicevoxAPIURL != "https://voicevox.example.run.app" {
			t.Fatalf("VoicevoxAPIURL = %q, want https://voicevox.example.run.app", c.VoicevoxAPIURL)
		}
	})

	// 空のまま通すのは go-voicevox 側が localhost:50021 へ落とすためで、
	// ローカル実行とサイドカー構成のどちらでも設定なしで動きます。
	t.Run("未設定なら空のまま", func(t *testing.T) {
		t.Setenv("VOICEVOX_API_URL", "")

		c := &Config{}
		c.FillDefaults(LoadConfig())
		c.Normalize()

		if c.VoicevoxAPIURL != "" {
			t.Fatalf("VoicevoxAPIURL = %q, want empty", c.VoicevoxAPIURL)
		}
	})
}

// 既定値へ黙って落ちると、古いモデルを使い続けたまま気付けません。
func TestValidate_AIModelRequired(t *testing.T) {
	t.Setenv("GEMINI_MODEL", "")

	c := &Config{}
	c.FillDefaults(LoadConfig())
	c.Normalize()

	err := c.Validate()
	if err == nil {
		t.Fatal("モデル名未指定が素通りした")
	}
	if !strings.Contains(err.Error(), "GEMINI_MODEL") {
		t.Errorf("エラーに指定方法が無い: %v", err)
	}
}
