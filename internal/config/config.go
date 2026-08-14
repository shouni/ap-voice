// Package config は、環境変数からアプリケーション設定を読み込み検証します。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/gcp-kit/serverrole"
)

const (
	// DefaultShutdownGrace はサーバー停止時の猶予時間のデフォルト値です。
	DefaultShutdownGrace = 15 * time.Second
	// DefaultHTTPTimeout は外部 HTTP 通信のタイムアウトのデフォルト値です。
	DefaultHTTPTimeout = 60 * time.Second
	// MinInputContentLength は生成に必要な入力テキストの最小長です。
	MinInputContentLength = 10
	// DefaultPipelineTimeout はジョブ 1 件の実行時間の上限のデフォルト値です。
	// 合成はセグメント数に比例して伸びるため長めに取りますが、上限が無いと
	// エンジンが応答しなくなった際に Cloud Run のインスタンスを占有し続けます。
	DefaultPipelineTimeout = 25 * time.Minute
	// DefaultMaxParallelSegments は 1 ジョブ内で同時に投げるセグメント数の既定です。
	DefaultMaxParallelSegments = 8
	// DefaultSegmentRateLimit はセグメントの投入間隔の既定です（秒2件）。
	DefaultSegmentRateLimit = 500 * time.Millisecond
	// DefaultSegmentTimeout はセグメント 1 件あたりの上限の既定です。
	DefaultSegmentTimeout = 120 * time.Second
	// DefaultDispatchDeadline は Cloud Tasks がワーカーの応答を待つ上限です。
	// **Cloud Tasks の HTTP ターゲットは 30 分が上限**で、そこから先へは伸ばせません。
	DefaultDispatchDeadline = 30 * time.Minute
	// defaultLocationID は Cloud Tasks のリージョンの既定値です。
	// ap-infra はフリート全体で asia-northeast1 を使っています。
	defaultLocationID = "asia-northeast1"
)

// ServerConfig は HTTP サーバーの設定です。
type ServerConfig struct {
	ServiceURL string `env:"SERVICE_URL" envDefault:"http://localhost:8080"`
	Port       string `env:"PORT" envDefault:"8080"`
	// Role はこのプロセスが担う役割です。明示が必須で、未設定は起動時エラーになります。
	Role            serverrole.Role `env:"SERVER_ROLE"`
	ShutdownTimeout time.Duration
}

// TasksConfig は Cloud Tasks キューの設定と、受信時の OIDC 検証の設定です。
// Cloud Tasks に閉じた設定であり、GCP 一般の設定でも HTTP サーバーの設定でもないため、
// 兄弟アプリと同じくここに集約します。
type TasksConfig struct {
	QueueID         string `env:"CLOUD_TASKS_QUEUE_ID"`
	WorkerURL       string `env:"WORKER_URL"`
	TaskAudienceURL string `env:"TASK_AUDIENCE_URL"`
	// CallerServiceAccountEmail は、投入するタスクの oidcToken.serviceAccountEmail に
	// 指定する caller SA です。トークンを生成して付与するのは Cloud Tasks であり、
	// このプロセスが署名するわけではありません。投入側＝ Web 面だけの設定です。
	CallerServiceAccountEmail string `env:"TASK_CALLER_SERVICE_ACCOUNT_EMAIL"`
	// DispatchDeadline は、投入するタスクに載せる応答待ちの上限です。
	//
	// 「待つ時間」ではなく **ワーカーの実行時間の実効上限**です。これを超えると
	// ワーカーがまだ処理中でも Cloud Tasks が待受を打ち切ります。Cloud Run の timeout を
	// いくら伸ばしてもこの上限は動きません。
	DispatchDeadline time.Duration `env:"TASK_DISPATCH_DEADLINE"`
	// AllowedServiceAccounts は、worker が受け付ける caller SA の許可リスト（カンマ区切り）です。
	// 受信側が許可すべきは自分自身ではなく投入側の SA で、web と worker で実行 SA を
	// 分けているため、worker には「他人の SA」が並びます。
	// 空だと検証器が fail-closed になるため、worker では必須です。
	AllowedServiceAccounts []string `env:"ALLOWED_TASK_SERVICE_ACCOUNTS"`
}

// GCPConfig は Google Cloud Platform の設定です。
//
// Gemini は Vertex AI 経由でのみ呼びます。API キー経路（GEMINI_API_KEY）は持ちません。
// Cloud Run では実行 SA の roles/aiplatform.user で認証できるため、キーを配ると
// 使われないシークレットへのアクセス権を配ることになるためです。ローカル実行では
// ADC（gcloud auth application-default login）が要ります。
type GCPConfig struct {
	ProjectID string `env:"GCP_PROJECT_ID"`
	// LocationID は **Cloud Tasks のキューが存在するリージョン**です（ap-infra は
	// asia-northeast1 を渡します）。Vertex AI のエンドポイントとは別物で、そちらは
	// adapters.defaultVertexLocationID に固定してあります。混同すると、キューを
	// 見失うか Vertex が存在しないリージョンを指すかのどちらかになります。
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
//
// 流量の3つは**デプロイ先のエンジンの大きさで変わる値**なので env に置きます。
// エンジンが 4 vCPU / 3 GiB のサイドカーだという事実はこのリポジトリではなく
// ap-infra が持っており、そこに合わせて絞るにはコードのビルドを挟みたくありません。
//
//	スループット = min(1/SegmentRateLimit, MaxParallelSegments ÷ 1セグメントの所要時間)
//
// **2026-08-14 の実測（12セグメント / 既定値のまま / 4 vCPU・3 GiB のサイドカー）**
//
//	所要      50.0 秒（12 件）      1 セグメントあたり 20〜25 秒
//	実効      0.24 件/秒            レート制限が許すのは 2.0 件/秒
//	CPU       2.31 / 5 vCPU        メモリ 0.94 / 4 GiB
//
// 効いていたのは右項です。**レート制限には 8 倍の余裕があり、一度も触れていません。**
// 12 件は開始から 5.5 秒で投入し終え、残りは合成待ちでした。
// CPU も飽和しておらず、エンジンは自分の 4 vCPU の 6 割ほどしか使っていません。
// つまり律速はエンジン内部にあり、**並列数を増やしても減らしても結果は読めません。**
// 変えるなら測ってからにしてください（env なので再ビルドは要りません）。
type VoicevoxConfig struct {
	// APIURL が空なら go-voicevox が http://localhost:50021 へ落とします。
	// ローカル実行と Cloud Run のサイドカー構成のどちらもその値でよいため、
	// モデル名と違って未設定を許します。
	APIURL string `env:"VOICEVOX_API_URL"`

	// MaxParallelSegments は 1 ジョブ内で同時に投げるセグメント数です。
	//
	// **上のスループットを実際に縛っているのはこちらです。** 既定の 8 はエンジンの
	// 4 vCPU を超えていますが、実測で CPU は飽和しておらず、実害は出ていません。
	// 代償として効くのはエンジン側のメモリで、同時に抱える合成の数だけバッファが
	// 積まれます。OOM が出たらここを下げます。
	//
	// **ピークは台本の長さでは上がりません。** 同時に飛ぶ数はここで頭打ちなので、
	// 12 行でも 200 行でもメモリの山は同じ高さで、変わるのは所要時間だけです。
	MaxParallelSegments int `env:"VOICEVOX_MAX_PARALLEL_SEGMENTS"`

	// SegmentRateLimit はセグメントの投入間隔です。
	//
	// **VOICEVOX に API のレート制限はありません。** 自前で立てたエンジンで、
	// サイドカー構成では同一インスタンス内にいます。外部仕様への準拠ではなく、
	// エンジンを叩きすぎないための自主的な絞りです。
	//
	// 実測では 8 倍の余裕があり、**調整つまみとしては機能していません**（上を参照）。
	// 触っても現状の速度は変わりません。効き始めるのは、1 セグメントの所要時間が
	// 既定値の 500ms に近づくほど速くなったときです。
	SegmentRateLimit time.Duration `env:"VOICEVOX_SEGMENT_RATE_LIMIT"`

	// SegmentTimeout はセグメント 1 件あたりの上限です。
	// サイドカーは起動時から待ち受けているため、コールドスタート分の余裕は不要です。
	SegmentTimeout time.Duration `env:"VOICEVOX_SEGMENT_TIMEOUT"`
}

// PipelineConfig はジョブ 1 件の実行に関する設定です。
type PipelineConfig struct {
	// Timeout はジョブ 1 件の実行時間の上限です。
	//
	// **三段のうち最も短く取ります。** アプリが自分で先に諦めることで、失敗を記録して
	// Slack へ通知してから終われます。逆順にすると Cloud Tasks が先にリクエストを
	// 打ち切り、プロセスは SIGTERM で落ちて通知の機会を失います。キューは
	// max_attempts = 1 なので再試行も来ません。
	Timeout time.Duration `env:"PIPELINE_TIMEOUT"`
}

// StorageConfig はストレージの設定です。
type StorageConfig struct {
	// GCSBucket は成果物の置き場です。**出力先は利用者に入力させません。**
	// ジョブ ID からパスを導くことで、1 ジョブの成果物が必ず 1 つのプレフィックスに
	// まとまり、履歴の一覧や削除が中身を知らずに行えます。
	GCSBucket string `env:"GCS_VOICE_BUCKET"`
	// MusicBucket は、楽曲紹介モードの入力（ap-comp の recipe.json）を
	// ジョブ ID から解決するために使います。
	// **ap-mv と同じ環境変数・同じ規則です**（gs://<MusicBucket>/music/<jobID>/recipe.json）。
	// 読む側が 2 つに増えたので、片方だけ別名にすると設定を移すときに取り違えます。
	MusicBucket string `env:"AP_MUSIC_BUCKET" envDefault:"ap-music"`
}

// AuthConfig は認証と認可の設定です。Web 面だけが読みます。
type AuthConfig struct {
	GoogleClientID     string   `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string   `env:"GOOGLE_CLIENT_SECRET"`
	SessionSecret      string   `env:"SESSION_SECRET"`
	SessionEncryptKey  string   `env:"SESSION_ENCRYPT_KEY"`
	AllowedEmails      []string `env:"ALLOWED_EMAILS"`
	AllowedDomains     []string `env:"ALLOWED_DOMAINS"`

	// AllowedM2MServiceAccounts は、ブラウザではなく機械が /api を叩くときに
	// 許可するサービスアカウントです（ap-mcp など）。
	//
	// **空でも起動します。** 未設定なら M2M の検証は常に失敗し、すべての
	// リクエストがセッション認証へ落ちます。つまり API を使わない構成では
	// 何も設定しなくてよく、使う構成で書き忘れると「エージェントだけが
	// ログイン画面へ飛ばされる」形で現れます。兄弟アプリと同じ env 名です。
	AllowedM2MServiceAccounts []string `env:"ALLOWED_M2M_SERVICE_ACCOUNTS"`
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

// LoadConfig は環境変数から設定を読み込みます。
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("環境変数の読み込みに失敗しました: %w", err)
	}

	if err := cfg.normalize(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// normalize は読み込んだ値の表記ゆれを整えます。
func (c *Config) normalize() error {
	// 環境変数名はアプリ側の関心事なので、キットのエラーへここで文脈を足します。
	role, err := serverrole.Parse(string(c.Server.Role))
	if err != nil {
		return fmt.Errorf("SERVER_ROLE: %w", err)
	}
	c.Server.Role = role

	c.Server.ServiceURL = strings.TrimSpace(c.Server.ServiceURL)
	c.Server.ShutdownTimeout = DefaultShutdownGrace

	c.Tasks.QueueID = strings.TrimSpace(c.Tasks.QueueID)
	c.Tasks.WorkerURL = strings.TrimSpace(c.Tasks.WorkerURL)
	c.Tasks.TaskAudienceURL = strings.TrimSpace(c.Tasks.TaskAudienceURL)
	if c.Tasks.TaskAudienceURL == "" {
		c.Tasks.TaskAudienceURL = c.Server.ServiceURL
	}
	c.Tasks.CallerServiceAccountEmail = strings.TrimSpace(c.Tasks.CallerServiceAccountEmail)
	if c.Tasks.DispatchDeadline <= 0 {
		c.Tasks.DispatchDeadline = DefaultDispatchDeadline
	}
	if c.Pipeline.Timeout <= 0 {
		c.Pipeline.Timeout = DefaultPipelineTimeout
	}
	c.Tasks.AllowedServiceAccounts = normalizeList(c.Tasks.AllowedServiceAccounts)

	c.Auth.AllowedEmails = normalizeList(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = normalizeList(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = normalizeList(c.Auth.AllowedM2MServiceAccounts)

	c.GCP.ProjectID = strings.TrimSpace(c.GCP.ProjectID)
	c.GCP.LocationID = strings.TrimSpace(c.GCP.LocationID)
	if c.GCP.LocationID == "" {
		c.GCP.LocationID = defaultLocationID
	}

	// env はカンマで分割するだけなので、前後の空白と重複はここで落とします。
	c.AI.GeminiModels = normalizeList(c.AI.GeminiModels)
	c.AI.GeminiModel = firstModel(c.AI.GeminiModels)

	c.Storage.GCSBucket = normalizeGCSBucket(c.Storage.GCSBucket)
	c.Storage.MusicBucket = normalizeGCSBucket(c.Storage.MusicBucket)
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

// ValidateEssentialConfig はアプリケーション実行に不可欠な設定を検証します。
// 検証範囲は SERVER_ROLE に従います。担当しない面の設定まで要求すると、
// 使わない権限や認証情報を配ることになるためです。
func (c *Config) ValidateEssentialConfig() error {
	if len(c.AI.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS が設定されていません（カンマ区切りで複数指定すると、先頭が既定モデルになります）")
	}

	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID が設定されていません（Gemini は Vertex AI 経由で呼びます）")
	}

	// **両ロールで要ります。** web は履歴の一覧と出力先の組み立てに、worker は
	// synthesize が保存済み台本を読むために使います。
	if c.Storage.GCSBucket == "" {
		return fmt.Errorf("GCS_VOICE_BUCKET が設定されていません")
	}

	// 三段のうち上二段の関係はどちらのロールでも検査します。web は投入時に
	// dispatch_deadline を載せ、worker はその内側で PIPELINE_TIMEOUT を使うため、
	// 崩れていると片方だけ直しても噛み合いません。
	if c.Pipeline.Timeout >= c.Tasks.DispatchDeadline {
		return fmt.Errorf(
			"PIPELINE_TIMEOUT (%v) は TASK_DISPATCH_DEADLINE (%v) より短くしてください。"+
				"逆だと Cloud Tasks が先に打ち切り、失敗通知を出せないままジョブが失われます",
			c.Pipeline.Timeout, c.Tasks.DispatchDeadline)
	}

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	if c.Server.Role.ServesWorker() {
		if c.Tasks.TaskAudienceURL == "" {
			return fmt.Errorf("TASK_AUDIENCE_URL が設定されていません。Cloud Tasks の OIDC 検証に必須です")
		}
		// 空だと検証器が fail-closed になり、全タスクが失敗し続けます。
		if len(c.Tasks.AllowedServiceAccounts) == 0 {
			return fmt.Errorf("許可する caller SA が 1 件も指定されていません。ALLOWED_TASK_SERVICE_ACCOUNTS を設定してください")
		}
	}

	return nil
}

// validateWebConfig は Web 面（OAuth ログインとセッション、タスク投入）に必要な設定を検証します。
//
// Worker 面には要求しません。担当しない面の設定まで求めると、使わない認証情報への
// アクセス権を配ることになります。
func (c *Config) validateWebConfig() error {
	// タスクを投入するのは Web 面だけなので、キュー名も Web 面の要件です。
	if c.Tasks.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID が設定されていません")
	}
	if c.Tasks.WorkerURL == "" {
		return fmt.Errorf("WORKER_URL が設定されていません")
	}
	// caller SA はタスクを投入する側＝ Web 面の要件です。worker が受け付ける許可リストは
	// ALLOWED_TASK_SERVICE_ACCOUNTS で別に指定します。
	if c.Tasks.CallerServiceAccountEmail == "" {
		return fmt.Errorf("TASK_CALLER_SERVICE_ACCOUNT_EMAIL が設定されていません")
	}

	if c.Auth.GoogleClientID == "" || c.Auth.GoogleClientSecret == "" || c.Auth.SessionSecret == "" {
		return fmt.Errorf("OAuth 関連の設定（GOOGLE_CLIENT_ID・GOOGLE_CLIENT_SECRET・SESSION_SECRET）が不足しています")
	}
	if len(c.Auth.AllowedEmails) == 0 && len(c.Auth.AllowedDomains) == 0 {
		return fmt.Errorf("許可されたメールアドレスまたはドメインが一つも設定されていません（認可リストが空です）")
	}
	if c.Auth.SessionEncryptKey == "" {
		return fmt.Errorf("SESSION_ENCRYPT_KEY が設定されていません")
	}
	// AES の要件。長さが違うとセッションの暗号化に失敗します。
	if n := len(c.Auth.SessionEncryptKey); n != 16 && n != 24 && n != 32 {
		return fmt.Errorf("SESSION_ENCRYPT_KEY の長さが不正です (%d バイト)。16, 24, 32 のいずれかにしてください", n)
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

// normalizeGCSBucket は、バケット名の表記ゆれを整えます。
//
// **バケット「名」であって URI ではありません。** `gs://ap-music/` のように
// コンソールから貼った形で渡されることがあり、そのまま使うと
// `gs://gs://ap-music//music/...` という URI を組み立ててしまいます。
// ap-mv の同名の関数と同じ規則です — 同じバケットを読む側が 2 つある以上、
// 受け取り方まで揃えておかないと、片方だけ動く設定が生まれます。
func normalizeGCSBucket(bucket string) string {
	bucket = strings.TrimSpace(bucket)
	bucket = strings.TrimPrefix(bucket, "gs://")
	return strings.Trim(bucket, "/")
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
