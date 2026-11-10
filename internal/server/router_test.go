package server

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/builder"
	"github.com/shouni/ap-voice/internal/domain"
)

// stubExecutor is the minimal executor passed to worker.Handler.
type stubExecutor struct{ called bool }

func (s *stubExecutor) Execute(_ context.Context, _ domain.Request) error {
	s.called = true
	return nil
}

func do(t *testing.T, h http.Handler, method, path string) int {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec.Code
}

// The health check is needed regardless of role. Cloud Run uses it to confirm startup,
// so it must answer even when no handler at all has been built.
func TestRouter_HealthAlwaysServed(t *testing.T) {
	for _, h := range []*builder.AppHandlers{nil, {}} {
		if got := do(t, NewRouter(h, ""), http.MethodGet, "/health"); got != http.StatusOK {
			t.Errorf("GET /health = %d, want %d", got, http.StatusOK)
		}
	}
}

// In a SERVER_ROLE=web process both TaskAuth and Worker are nil, so
// /tasks/generate is not registered at all. Even with an IAM or ingress misconfiguration,
// this pins that the public-facing process has no worker entry point.
func TestRouter_WorkerRouteAbsentWithoutTaskAuth(t *testing.T) {
	r := NewRouter(&builder.AppHandlers{}, "")

	if got := do(t, r, http.MethodPost, "/tasks/generate"); got != http.StatusNotFound {
		t.Fatalf("POST /tasks/generate = %d, want %d", got, http.StatusNotFound)
	}
}

// A SERVER_ROLE=worker process has no submission form.
// This pins that no unauthenticated page sprouts on the private service.
func TestRouter_WebRouteAbsentWithoutWebHandler(t *testing.T) {
	r := NewRouter(&builder.AppHandlers{}, "")

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := do(t, r, method, "/"); got != http.StatusNotFound {
			t.Errorf("%s / = %d, want %d", method, got, http.StatusNotFound)
		}
	}
	if got := do(t, r, http.MethodGet, "/auth/login"); got != http.StatusNotFound {
		t.Errorf("GET /auth/login = %d, want %d", got, http.StatusNotFound)
	}
}

// On the worker side the route is registered, and a request without an OIDC token is
// rejected by the verification middleware. It checks both not-404 (= registered) and
// not-200 (= not let through).
func TestRouter_WorkerRouteGuardedByTaskAuth(t *testing.T) {
	exec := &stubExecutor{}
	r := NewRouter(&builder.AppHandlers{
		TaskAuth: mustOIDC(t, "https://worker.example.run.app",
			[]string{"caller@example.iam.gserviceaccount.com"}),
		Worker: worker.NewHandler[domain.Request](exec),
	}, "")

	got := do(t, r, http.MethodPost, "/tasks/generate")
	if got == http.StatusNotFound {
		t.Fatal("POST /tasks/generate is not registered")
	}
	if got == http.StatusOK {
		t.Fatalf("POST /tasks/generate = %d: passed without authentication", got)
	}
	if exec.called {
		t.Fatal("the executor was called without passing authentication")
	}
}

// A CSP is on every response and script-src is not loosened.
func TestResponsesCarryContentSecurityPolicy(t *testing.T) {
	t.Parallel()

	router := NewRouter(&builder.AppHandlers{}, "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("no Content-Security-Policy")
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP lacks %q: %s", want, policy)
		}
	}
	// 'unsafe-inline' in script-src would undo the point of removing inline scripts
	// (paired with TestTemplatesHaveNoInlineScripts in assets).
	scriptSrc := cspDirective(policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("no script-src: %s", policy)
	}
	if strings.Contains(scriptSrc, "unsafe-inline") || strings.Contains(scriptSrc, "unsafe-eval") {
		t.Errorf("script-src is loosened: %s", scriptSrc)
	}
	// Audio 302s to a signed URL, so media-src has to allow the destination.
	if !strings.Contains(cspDirective(policy, "media-src"), "https://storage.googleapis.com") {
		t.Errorf("media-src does not allow the signed URL's host: %s", policy)
	}
}

// cspDirective extracts one directive from a CSP. It returns an empty string if absent.
func cspDirective(policy, name string) string {
	for directive := range strings.SplitSeq(policy, ";") {
		directive = strings.TrimSpace(directive)
		if after, ok := strings.CutPrefix(directive, name+" "); ok {
			return after
		}
	}
	return ""
}

// Compression is in effect. The pages are Japanese UTF-8 (3 bytes per character) and compress well,
// yet they used to be served uncompressed.
func TestCompressibleResponsesAreCompressed(t *testing.T) {
	t.Parallel()

	router := NewRouter(&builder.AppHandlers{}, "")
	req := httptest.NewRequest(http.MethodGet, "/static/vendor/bootstrap-5.3.8/bootstrap.min.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}

	reader, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	defer func() { _ = reader.Close() }()

	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("cannot decompress: %v", err)
	}
	if !strings.Contains(string(body), "Bootstrap") {
		t.Error("the decompressed body is not Bootstrap's CSS")
	}
	if len(body) <= rec.Body.Len() {
		t.Errorf("compressed %d bytes is not below the original %d bytes", rec.Body.Len(), len(body))
	}
}

// Defensive headers other than the CSP are on every response too. Each is one line, yet
// the page looks normal without it, so a missing one goes unnoticed.
func TestResponsesCarrySecurityHeaders(t *testing.T) {
	t.Parallel()

	router := NewRouter(&builder.AppHandlers{}, "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// autoplay is not blocked (it would break media playback).
	policy := rec.Header().Get("Permissions-Policy")
	if policy == "" {
		t.Error("no Permissions-Policy")
	}
	if strings.Contains(policy, "autoplay") {
		t.Errorf("Permissions-Policy blocks autoplay: %s", policy)
	}
}

// mustOIDC builds a verifier configured for tests (New returns an error when settings are missing).
func mustOIDC(t *testing.T, audience string, allowed []string) *oidc.Verifier {
	t.Helper()
	v, err := oidc.New(audience, allowed)
	if err != nil {
		t.Fatalf("oidc.New() error = %v", err)
	}
	return v
}
