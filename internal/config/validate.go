package config

import "fmt"

// ValidateEssentialConfig validates the settings essential to running the application.
// What is validated follows SERVER_ROLE. Requiring settings for a side this process does not serve
// would mean handing out permissions and credentials it never uses.
func (c *Config) ValidateEssentialConfig() error {
	if len(c.AI.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS is not set (with several, comma-separated, the first is the default model)")
	}

	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID is not set (Gemini is called through Vertex AI)")
	}

	// Both roles need it: web to list the history and build output locations, the worker
	// for synthesize to read stored scripts.
	if c.Storage.GCSBucket == "" {
		return fmt.Errorf("GCS_VOICE_BUCKET is not set")
	}

	// Of the three tiers, the relation between the upper two is checked for either role. web stamps
	// dispatch_deadline at enqueue time and the worker uses PIPELINE_TIMEOUT inside it, so
	// if they are out of order, fixing only one side does not make them fit.
	if c.Tasks.DispatchDeadline <= 0 {
		return fmt.Errorf("TASK_DISPATCH_DEADLINE is not set (the deployment decides the three timeout tiers, e.g. 30m)")
	}

	if c.Pipeline.Timeout >= c.Tasks.DispatchDeadline {
		return fmt.Errorf(
			"PIPELINE_TIMEOUT (%v) must be shorter than TASK_DISPATCH_DEADLINE (%v); "+
				"otherwise Cloud Tasks cuts the request first and the job is lost without a failure notification",
			c.Pipeline.Timeout, c.Tasks.DispatchDeadline)
	}

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	if c.Server.Role.ServesWorker() {
		// Unlimited is not allowed. If Cloud Tasks cuts the request first, nothing is recorded or notified,
		// it is not retried, and the job stays running. Only the worker side receives it, so
		// only the worker side requires it.
		if c.Pipeline.Timeout <= 0 {
			return fmt.Errorf("PIPELINE_TIMEOUT is not set (it cannot be unlimited on the worker; set a value shorter than TASK_DISPATCH_DEADLINE)")
		}
		if c.Tasks.TaskAudienceURL == "" {
			return fmt.Errorf("TASK_AUDIENCE_URL is not set; it is required for Cloud Tasks OIDC verification")
		}
		// Empty, the verifier fails closed and every task keeps failing.
		if len(c.Tasks.AllowedServiceAccounts) == 0 {
			return fmt.Errorf("no allowed caller SA is configured; set ALLOWED_TASK_SERVICE_ACCOUNTS")
		}
	}

	return nil
}

// validateWebConfig validates the settings the web side needs (OAuth login and sessions, task enqueueing).
//
// They are not required of the worker side. Requiring settings for a side it does not serve would
// hand out access to credentials it never uses.
func (c *Config) validateWebConfig() error {
	// Only the web side enqueues tasks, so the queue name is a web-side requirement too.
	if c.Tasks.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID is not set")
	}
	if c.Tasks.WorkerURL == "" {
		return fmt.Errorf("WORKER_URL is not set")
	}
	// The caller SA is a requirement of the enqueuing side, the web side. The allowlist the worker
	// accepts is set separately with ALLOWED_TASK_SERVICE_ACCOUNTS.
	if c.Tasks.CallerServiceAccountEmail == "" {
		return fmt.Errorf("TASK_CALLER_SERVICE_ACCOUNT_EMAIL is not set")
	}

	if c.Auth.GoogleClientID == "" || c.Auth.GoogleClientSecret == "" {
		return fmt.Errorf("OAuth settings (GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET) are missing")
	}
	if len(c.Auth.AllowedEmails) == 0 && len(c.Auth.AllowedDomains) == 0 {
		return fmt.Errorf("no allowed email address or domain is configured (the authorization list is empty)")
	}

	return nil
}
