// Package config は、環境変数からアプリケーション設定を読み込み検証します。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

const (
	// DefaultHTTPTimeout は外部 HTTP 通信のタイムアウトのデフォルト値です。
	DefaultHTTPTimeout = 60 * time.Second
	// MinInputContentLength は生成に必要な入力テキストの最小長です。
	MinInputContentLength = 10
	// defaultLocationID は Vertex AI のロケーションのデフォルト値です。
	// フリートの他アプリは asia-northeast1 を渡しますが、ap-voice の Gemini 呼び出しは
	// 従来 "global" で動いているため、既定値はそちらに合わせます。
	defaultLocationID = "global"
)

// GCPConfig は Google Cloud Platform の設定です。
//
// Gemini は Vertex AI 経由でのみ呼びます。API キー経路（GEMINI_API_KEY）は持ちません。
// Cloud Run では実行 SA の roles/aiplatform.user で認証できるため、キーを配ると
// 使われないシークレットへのアクセス権を配ることになるためです。ローカル実行では
// ADC（gcloud auth application-default login）が要ります。
type GCPConfig struct {
	ProjectID string `env:"GCP_PROJECT_ID"`
	// 既定値は envDefault ではなく normalize で埋めます。envDefault は変数が
	// 未設定のときしか効かず、空文字を渡された場合に素通りするためです。
	LocationID string `env:"GCP_LOCATION_ID"`
}

// AIConfig は AI モデルの設定です。
type AIConfig struct {
	// モデル一覧はカンマ区切りで、先頭が既定モデルです。単数形は LoadConfig が
	// 一覧の先頭から埋めるので、環境変数からは読みません。
	//
	// 既定値は持ちません。モデル ID が古くなるのは Google のリリース周期であって
	// このリポジトリの都合ではないため、既定値を置くと誰も気付かないまま古いモデルを
	// 使い続けることになります。空なら ValidateEssentialConfig が起動時に落とします。
	GeminiModels []string `env:"GEMINI_MODELS"`
	GeminiModel  string   `env:"-"`
}

// VoicevoxConfig は音声合成エンジンの設定です。
type VoicevoxConfig struct {
	// APIURL が空なら go-voicevox が http://localhost:50021 へ落とします。
	// ローカル実行と Cloud Run のサイドカー構成のどちらもその値でよいため、
	// モデル名と違って未設定を許します。
	APIURL string `env:"VOICEVOX_API_URL"`
}

// NotificationConfig は通知の設定です。
type NotificationConfig struct {
	SlackWebhookURL string `env:"SLACK_WEBHOOK_URL"`
}

// HTTPConfig は外部 HTTP 通信の設定です。
type HTTPConfig struct {
	// 既定値は LocationID と同じ理由で normalize が埋めます。
	Timeout time.Duration `env:"HTTP_TIMEOUT"`
}

// Config はアプリ設定です。
//
// 保持するのはデプロイ先が決める値だけで、入力元・出力先・生成モードといった
// 実行ごとに変わる値は domain.Request が持ちます。両者を 1 つの構造体へ混ぜると、
// 実行ごとの値が環境変数から来るように見えてしまいます。
type Config struct {
	GCP          GCPConfig
	AI           AIConfig
	Voicevox     VoicevoxConfig
	Notification NotificationConfig
	HTTP         HTTPConfig
}

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("環境変数の読み込みに失敗しました: %w", err)
	}

	cfg.normalize()

	return &cfg, nil
}

// normalize は読み込んだ値の表記ゆれを整えます。
func (c *Config) normalize() {
	c.GCP.ProjectID = strings.TrimSpace(c.GCP.ProjectID)
	c.GCP.LocationID = strings.TrimSpace(c.GCP.LocationID)
	if c.GCP.LocationID == "" {
		c.GCP.LocationID = defaultLocationID
	}

	// env はカンマで分割するだけなので、前後の空白と重複はここで落とします。
	c.AI.GeminiModels = normalizeList(c.AI.GeminiModels)
	c.AI.GeminiModel = firstModel(c.AI.GeminiModels)

	c.Voicevox.APIURL = strings.TrimSpace(c.Voicevox.APIURL)
	c.Notification.SlackWebhookURL = strings.TrimSpace(c.Notification.SlackWebhookURL)

	if c.HTTP.Timeout <= 0 {
		c.HTTP.Timeout = DefaultHTTPTimeout
	}
}

// ValidateEssentialConfig はアプリケーション実行に不可欠な設定を検証します。
func (c *Config) ValidateEssentialConfig() error {
	if len(c.AI.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS が設定されていません（カンマ区切りで複数指定すると、先頭が既定モデルになります）")
	}

	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID が設定されていません（Gemini は Vertex AI 経由で呼びます）")
	}

	return nil
}

// firstModel は一覧の先頭（＝既定として使うモデル）を返します。
// 一覧が空になるのは設定漏れのときだけで、その値は使われる前に起動時検証で弾かれます。
func firstModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

// normalizeList は env が分割しただけのカンマ区切り値を整えます。
// 前後の空白を落とし、空要素と重複を捨て、順序は保ちます。
// 既定値で埋めることはせず、空なら空のまま返します。
func normalizeList(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}
