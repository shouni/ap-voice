package config

import (
	"strings"
	"time"

	"github.com/shouni/go-utils/envutil"
)

// DefaultHTTPTimeout はHTTPリクエストのデフォルトタイムアウトを定義します。
// DefaultModel はデフォルトの Google Gemini モデル名（例: "gemini-2.5-flash"）を指定します。
// MinInputContentLength は入力されたコンテンツの最小バイト。
const (
	DefaultHTTPTimeout    = 60 * time.Second
	DefaultModel          = "gemini-2.5-flash"
	MinInputContentLength = 10
)

// Config はコマンドラインフラグを保持する構造体です。
type Config struct {
	OutputFile     string
	Mode           string
	VoicevoxOutput string
	ScriptURL      string
	ScriptFile     string
	AIModel        string
	HTTPTimeout    time.Duration

	ProjectID    string
	GeminiAPIKey string
}

// Normalize は設定値の文字列フィールドから前後の空白を一括で削除します。
func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.OutputFile = strings.TrimSpace(c.OutputFile)
	c.VoicevoxOutput = strings.TrimSpace(c.VoicevoxOutput)
	c.ScriptURL = strings.TrimSpace(c.ScriptURL)
	c.ScriptFile = strings.TrimSpace(c.ScriptFile)
	c.AIModel = strings.TrimSpace(c.AIModel)
}

// FillDefaults は、現在の設定で空のフィールドを envCfg の値で補完します。
func (c *Config) FillDefaults(envCfg *Config) {
	if c.ProjectID == "" {
		c.ProjectID = envCfg.ProjectID
	}
	if c.GeminiAPIKey == "" {
		c.GeminiAPIKey = envCfg.GeminiAPIKey
	}
}

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() *Config {
	return &Config{
		ProjectID:    envutil.GetEnv("GCP_PROJECT_ID", ""),
		GeminiAPIKey: envutil.GetEnv("GEMINI_API_KEY", ""),
	}
}
