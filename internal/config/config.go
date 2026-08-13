// Package config は、環境変数からアプリケーション設定を読み込み検証します。
package config

import (
	"errors"
	"strings"
	"time"

	"github.com/shouni/go-utils/envutil"
)

// DefaultHTTPTimeout はHTTPリクエストのデフォルトタイムアウトを定義します。
// MinInputContentLength は生成に必要な入力テキストの最小長です。
const (
	DefaultHTTPTimeout    = 60 * time.Second
	MinInputContentLength = 10
)

// Config はコマンドラインフラグを保持する構造体です。
type Config struct {
	InputFile       string
	OutputFile      string
	SlackWebhookURL string

	Mode string

	// AIModel は使用する Gemini モデル名です。既定値は持たず、--model か
	// GEMINI_MODEL で必ず指定させます。モデル ID が古くなるのは Google の
	// リリース周期であってこのリポジトリの都合ではないため、既定値を置くと
	// 誰も気付かないまま古いモデルを使い続けることになります。
	AIModel string

	// VoicevoxAPIURL は音声合成に使う VOICEVOX エンジンの URL です。
	// 空なら go-voicevox が http://localhost:50021 へ落とし、警告を出します。
	// モデル名と違って既定値を置いてよいのは、50021 が VOICEVOX の標準ポートで
	// 外部の都合で古くなる値ではないためです。フラグは持たせず環境変数だけにして
	// あります。実行ごとに変える値ではなく、デプロイ先が決める値だからです。
	VoicevoxAPIURL string

	HTTPTimeout  time.Duration
	ProjectID    string
	GeminiAPIKey string
}

// Normalize は設定値の文字列フィールドから前後の空白を一括で削除します。
func (c *Config) Normalize() {
	if c == nil {
		return
	}
	c.InputFile = strings.TrimSpace(c.InputFile)
	c.OutputFile = strings.TrimSpace(c.OutputFile)
	c.AIModel = strings.TrimSpace(c.AIModel)
	c.SlackWebhookURL = strings.TrimSpace(c.SlackWebhookURL)
	c.VoicevoxAPIURL = strings.TrimSpace(c.VoicevoxAPIURL)
}

// FillDefaults は、現在の設定で空のフィールドを envCfg の値で補完します。
func (c *Config) FillDefaults(envCfg *Config) {
	if c.AIModel == "" {
		c.AIModel = envCfg.AIModel
	}
	if c.ProjectID == "" {
		c.ProjectID = envCfg.ProjectID
	}
	if c.GeminiAPIKey == "" {
		c.GeminiAPIKey = envCfg.GeminiAPIKey
	}
	if c.SlackWebhookURL == "" {
		c.SlackWebhookURL = envCfg.SlackWebhookURL
	}
	if c.VoicevoxAPIURL == "" {
		c.VoicevoxAPIURL = envCfg.VoicevoxAPIURL
	}
}

// Validate は実行に最低限必要な設定が揃っているかを確認します。
// フラグと環境変数の両方を見たあと（FillDefaults の後）に呼びます。
func (c *Config) Validate() error {
	if c.AIModel == "" {
		return errors.New("モデル名が指定されていません。--model / -g フラグか GEMINI_MODEL 環境変数で指定してください")
	}
	return nil
}

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() *Config {
	return &Config{
		AIModel:         envutil.GetEnv("GEMINI_MODEL", ""),
		ProjectID:       envutil.GetEnv("GCP_PROJECT_ID", ""),
		GeminiAPIKey:    envutil.GetEnv("GEMINI_API_KEY", ""),
		SlackWebhookURL: envutil.GetEnv("SLACK_WEBHOOK_URL", ""),
		VoicevoxAPIURL:  envutil.GetEnv("VOICEVOX_API_URL", ""),
	}
}
