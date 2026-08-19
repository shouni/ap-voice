package config

import "fmt"

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
	if c.Tasks.DispatchDeadline <= 0 {
		return fmt.Errorf("TASK_DISPATCH_DEADLINE が設定されていません（三段のタイムアウトはデプロイ設定が決めます。例: 30m）")
	}

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
		// 無制限は許しません。Cloud Tasks が先に打ち切ると、失敗の記録も通知も残らないまま
		// 再試行もされず、ジョブが running のまま残ります。渡されるのは worker 面だけなので、
		// 必須なのも worker 面だけです。
		if c.Pipeline.Timeout <= 0 {
			return fmt.Errorf("PIPELINE_TIMEOUT が設定されていません（worker では無制限にできません。TASK_DISPATCH_DEADLINE より短い値を設定してください）")
		}
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
