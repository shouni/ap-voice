// Package config は、環境変数からアプリケーション設定を読み込み検証します。
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-serve-kit/serverrole"
	"github.com/shouni/go-utils/strlist"
)

const (
	// DefaultShutdownGrace はサーバー停止時の猶予時間のデフォルト値です。
	DefaultShutdownGrace = 15 * time.Second
	// DefaultHTTPTimeout は外部 HTTP 通信のタイムアウトのデフォルト値です。
	// 縛るのは HTTP 1 往復で、セグメント 1 件ではありません（HTTPConfig 参照）。
	DefaultHTTPTimeout = 60 * time.Second
	// MinInputContentLength は生成に必要な入力テキストの最小長です。
	MinInputContentLength = 10
	// DefaultMaxParallelSegments は 1 ジョブ内で同時に投げるセグメント数の既定です。
	// エンジンの vCPU 数に合わせています（下の VoicevoxConfig の実測を参照）。
	DefaultMaxParallelSegments = 4
	// DefaultSegmentRateLimit はセグメントの投入間隔の既定です。
	DefaultSegmentRateLimit = 100 * time.Millisecond
	// DefaultSegmentTimeout はセグメント 1 件あたりの上限の既定です。
	DefaultSegmentTimeout = 120 * time.Second
	// defaultLocationID は Cloud Tasks のリージョンの既定値です。
	// フリート全体で asia-northeast1 を使っています。
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
	// 「待つ時間」ではなくワーカーの実行時間の実効上限です。これを超えると
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
	// LocationID は Cloud Tasks のキューが存在するリージョンです（デプロイ設定が
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
// 流量の3つはデプロイ先のエンジンの大きさで変わる値なので env に置きます。
// エンジンが 4 vCPU / 3 GiB のサイドカーだという事実はこのリポジトリではなく
// デプロイ設定が持っており、そこに合わせて絞るにはコードのビルドを挟みたくありません。
//
//	スループット = min(1/SegmentRateLimit, MaxParallelSegments ÷ 1セグメントの所要時間)
//
// 2026-08-14 の実測（12セグメント / 既定値のまま / 4 vCPU・3 GiB のサイドカー）
//
//	所要      50.0 秒（12 件）      1 セグメントあたり 20〜25 秒
//	実効      0.24 件/秒            レート制限が許すのは 2.0 件/秒
//	CPU       2.31 / 5 vCPU        メモリ 0.94 / 4 GiB
//
// 効いていたのは右項です。
//
// 2026-08-24 に Cloud Monitoring と Cloud Logging で並列数まで踏み込みました。
// 進捗ログが 1 セグメントごとに文字数と所要時間を出しているので、実効並列度ごとに
// 1 文字あたりの合成時間を出せます（バッチの実効並列は min(セグメント数, 8)）。
//
//	実効並列   標本   ms/文字(中央値)   スループット
//	約 4        7      233              17.2 文字/秒
//	8         108      430              18.6 文字/秒
//
// スループットは横ばいで、1 件あたりの所要だけが倍になっています。430 が
// 233 の約 2 倍で、並列比 8/4 と一致するのが飽和の署名です。CPU 使用率の
// p99 が 3.7 / 5 vCPU（うちエンジンの割り当ては 4）であることとも合います。
// 合成は CPU バウンドなので、4 vCPU は同時 4 本で使い切るという道理どおりです。
// 既定を 8 から 4 に落としたのはこの実測によります。
//
// 弱いところも書いておきます。低並列側は 7 標本しかありません（進捗ログが
// 5 件ごと＋最終のみで、4 セグメントのバッチからは 1 件しか採れない）。加えて
// 小さいバッチはコールドスタート直後の startup_cpu_boost に当たりえて、それだと
// 低並列を実際より良く見せます。この偏りは 4 への変更と逆向きに効くので、
// 所要時間が悪化したら env で 8 に戻して測り直してください。
//
// 2026-08-24 に Cloud Logging の 90 日ぶん（29 バッチ）で裏を取りました。
// go-voicevox がバッチ終了時に出す segment_duration_avg/min/max を、開始ログとの
// 差分（実測の所要時間）と突き合わせたものです。
//
//	バッチの規模    4 〜 60 セグメント
//	1 セグメント    平均 2.0 〜 35.3 秒（最小は 1.0 秒）
//	所要時間        ほぼ 波数 × セグメント平均 に乗る
//
// 上の 1 件だけを見て「レート制限には 8 倍の余裕がある」と書いていましたが、
// それは長い台本にしか当てはまりませんでした。旧既定の 500ms が律速に回るのは
// 1 セグメントが 並列数 × 間隔 = 4 秒 を切ったときで、29 バッチ中 13 バッチに
// 4 秒未満のセグメントがあり、4 バッチは平均が 4 秒未満（2.0 〜 3.2 秒）でした。
// その 4 バッチ（いずれも 4 セグメント）は実測 4.5 〜 6.4 秒で、合成そのものは
// 2.0 〜 3.2 秒です。短い台本ほど待ちの割合が大きいという、いちばん目につく
// ところに効いていました。
type VoicevoxConfig struct {
	// APIURL が空なら go-voicevox が http://localhost:50021 へ落とします。
	// ローカル実行と Cloud Run のサイドカー構成のどちらもその値でよいため、
	// モデル名と違って未設定を許します。
	APIURL string `env:"VOICEVOX_API_URL"`

	// MaxParallelSegments は 1 ジョブ内で同時に投げるセグメント数です。
	//
	// 上のスループットを実際に縛っているのはこちらです。既定はエンジンの
	// vCPU 数と同じ 4 です。合成は CPU バウンドなので、4 vCPU なら同時 4 本で
	// 使い切ります。それ以上はスループットを増やさず、1 件あたりの待ちを
	// 伸ばすだけであることが実測に出ています（下記）。
	//
	// この値はアプリの都合であって、インフラの設定ではありません。エンジンの
	// 大きさ（4 vCPU / 3 GiB）を決めるのはデプロイ設定ですが、それをどう食わせるかは
	// 合成の戦略なのでここが持ちます。デプロイ設定はこの env を置いておらず、
	// env が残っているのは戻すためです — 実測が覆ったら、再ビルドを挟まずに
	// VOICEVOX_MAX_PARALLEL_SEGMENTS で上書きできます。
	//
	// ピークは台本の長さでは上がりません。同時に飛ぶ数はここで頭打ちなので、
	// 12 行でも 200 行でもメモリの山は同じ高さで、変わるのは所要時間だけです。
	MaxParallelSegments int `env:"VOICEVOX_MAX_PARALLEL_SEGMENTS"`

	// SegmentRateLimit はセグメントの投入間隔です。
	//
	// VOICEVOX に API のレート制限はありません。自前で立てたエンジンで、
	// サイドカー構成では同一インスタンス内にいます。外部仕様への準拠ではなく、
	// エンジンを叩きすぎないための自主的な絞りです。
	//
	// スループットのつまみではありません。同時に飛ぶ数を縛るのは
	// MaxParallelSegments のほうで、この間隔が効き始めるのは
	// 「1 セグメントの所要時間 < 並列数 × 間隔」になったとき、つまり実測の
	// 20〜25 秒が 1 秒を切るほど速くなったときだけです。
	//
	// それでも払う代償はあります。バーストが 1 なので、毎バッチの先頭には
	// (並列数 - 1) × 間隔 の待ちがそのまま乗ります。既定の 8 並列なら、旧既定の
	// 500ms で 3.5 秒、100ms で 0.7 秒です。効かない絞りに毎ジョブ数秒を払う形
	// だったので、意図（起動時にエンジンを一斉に叩かない）を保てる最小限として
	// 100ms にしています。
	SegmentRateLimit time.Duration `env:"VOICEVOX_SEGMENT_RATE_LIMIT"`

	// SegmentTimeout はセグメント 1 件あたりの上限です。
	// サイドカーは起動時から待ち受けているため、コールドスタート分の余裕は不要です。
	//
	// 「1 件」はクエリ（audio_query）と合成（synthesis）の 2 往復ぶんで、それぞれ
	// リトライが 1 回まで入ります。1 往復ぶんの上限は HTTP_TIMEOUT です—
	// 小さいほうが先に効くので、こちらだけ延ばしても往復は延びません（HTTPConfig 参照）。
	SegmentTimeout time.Duration `env:"VOICEVOX_SEGMENT_TIMEOUT"`
}

// PipelineConfig はジョブ 1 件の実行に関する設定です。
type PipelineConfig struct {
	// Timeout はジョブ 1 件の実行時間の上限です。
	//
	// 三段のうち最も短く取ります。アプリが自分で先に諦めることで、失敗を記録して
	// Slack へ通知してから終われます。逆順にすると Cloud Tasks が先にリクエストを
	// 打ち切り、プロセスは SIGTERM で落ちて通知の機会を失います。voice-queue は
	// max_attempts = 2 ですが、この不等式が守られていないと 2 回目が 1 回目と
	// 重なりえます。再配信が直列に届くことに再実行ガードが依存しています。
	Timeout time.Duration `env:"PIPELINE_TIMEOUT"`
}

// StorageConfig はストレージの設定です。
type StorageConfig struct {
	// GCSBucket は成果物の置き場です。出力先は利用者に入力させません。
	// ジョブ ID からパスを導くことで、1 ジョブの成果物が必ず 1 つのプレフィックスに
	// まとまり、履歴の一覧や削除が中身を知らずに行えます。
	GCSBucket string `env:"GCS_VOICE_BUCKET"`
	// MusicBucket は、楽曲紹介モードの入力（music.Recipe 形式の recipe.json）を
	// ジョブ ID から解決するために使います。
	// 動画生成サービスと同じ環境変数・同じ規則です（gs://<MusicBucket>/music/<jobID>/recipe.json）。
	// 読む側が 2 つに増えたので、片方だけ別名にすると設定を移すときに取り違えます。
	MusicBucket string `env:"AP_MUSIC_BUCKET" envDefault:"ap-music"`
	// FirestoreDatabase はジョブ状態を置く Firestore データベースです。
	//
	// 名前付きデータベースを使うのは、(default) の枠を占めないためです。
	// コレクション名は設定にしません（サービスの身元であってデプロイごとに
	// 変わる値ではないため。repository の statusCollection を参照）。
	FirestoreDatabase string `env:"FIRESTORE_DATABASE" envDefault:"job-status"`
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
	// 許可するサービスアカウントです（MCP サーバーなど）。
	//
	// 空でも起動します。未設定なら M2M の検証は常に失敗し、すべての
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
//
// 合成の上限は 2 段になっています。**この値が縛るのは HTTP 1 往復**
// （http.Client.Timeout）で、セグメント 1 件はクエリと合成の 2 往復、しかも
// go-http-kit が 1 回ずつリトライするため（builder の WithMaxRetries(1)）、
// セグメント全体を縛るのは VOICEVOX_SEGMENT_TIMEOUT のほうです。
//
// 60 秒と 120 秒は食い違いではなく、この 2 段です。**往復の上限をセグメントの
// 上限まで上げてはいけません** — 最初の 1 往復が budget を使い切れるようになり、
// 上限に達した往復のやり直しが入らなくなります。半分にしておけば、時間切れの
// 往復をもう一度試してもセグメントの budget に収まります。
//
// 段を跨いだ勘違いに注意してください。1 往復を 60 秒より延ばしたいときに
// VOICEVOX_SEGMENT_TIMEOUT を上げても効きません（小さいほうが先に効きます）。
// 逆にこの値を上げると通知にも効きます— Slack へ投げるのは同じクライアント
// （リトライだけ外したもの）なので、Webhook が応答しないときの待ちが
// そのまま伸びます。
//
// なお実測のセグメントは最大 35.3 秒で、どちらの上限にも遠いところにあります。
// 所要を決めているのは並列数のほうです（VoicevoxConfig を参照）。
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
	c.Tasks.AllowedServiceAccounts = strlist.Normalize(c.Tasks.AllowedServiceAccounts)

	c.Auth.AllowedEmails = strlist.Normalize(c.Auth.AllowedEmails)
	c.Auth.AllowedDomains = strlist.Normalize(c.Auth.AllowedDomains)
	c.Auth.AllowedM2MServiceAccounts = strlist.Normalize(c.Auth.AllowedM2MServiceAccounts)

	c.GCP.ProjectID = strings.TrimSpace(c.GCP.ProjectID)
	c.GCP.LocationID = strings.TrimSpace(c.GCP.LocationID)
	if c.GCP.LocationID == "" {
		c.GCP.LocationID = defaultLocationID
	}

	// env はカンマで分割するだけなので、前後の空白と重複はここで落とします。
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

// firstModel は一覧の先頭（＝既定として使うモデル）を返します。
// 一覧が空になるのは設定漏れのときだけで、その値は使われる前に起動時検証で弾かれます。
func firstModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return models[0]
}
