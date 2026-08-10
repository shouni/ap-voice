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
