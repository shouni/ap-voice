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

// stubExecutor は worker.Handler に渡す最小の実行器です。
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

// ヘルスチェックは役割に関わらず必要です。Cloud Run の起動確認に使うため、
// ハンドラーが1つも組み立てられていなくても応答できなければなりません。
func TestRouter_HealthAlwaysServed(t *testing.T) {
	for _, h := range []*builder.AppHandlers{nil, {}} {
		if got := do(t, NewRouter(h, ""), http.MethodGet, "/health"); got != http.StatusOK {
			t.Errorf("GET /health = %d, want %d", got, http.StatusOK)
		}
	}
}

// SERVER_ROLE=web のプロセスでは TaskAuth も Worker も nil になるため、
// /tasks/generate はルートごと登録されません。IAM や ingress の設定漏れがあっても、
// 公開側のプロセスに Worker の入口が存在しないことをここで固定します。
func TestRouter_WorkerRouteAbsentWithoutTaskAuth(t *testing.T) {
	r := NewRouter(&builder.AppHandlers{}, "")

	if got := do(t, r, http.MethodPost, "/tasks/generate"); got != http.StatusNotFound {
		t.Fatalf("POST /tasks/generate = %d, want %d", got, http.StatusNotFound)
	}
}

// SERVER_ROLE=worker のプロセスには投入フォームがありません。
// 非公開サービスに認証なしの画面が生えないことをここで固定します。
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

// Worker 面ではルートが登録され、かつ OIDC トークンが無いリクエストは
// 検証ミドルウェアが弾きます。404 でないこと（=登録されていること）と、
// 200 でないこと（=素通りしないこと）の両方を見ます。
func TestRouter_WorkerRouteGuardedByTaskAuth(t *testing.T) {
	exec := &stubExecutor{}
	r := NewRouter(&builder.AppHandlers{
		TaskAuth: oidc.New("https://worker.example.run.app",
			[]string{"caller@example.iam.gserviceaccount.com"}),
		Worker: worker.NewHandler[domain.Request](exec),
	}, "")

	got := do(t, r, http.MethodPost, "/tasks/generate")
	if got == http.StatusNotFound {
		t.Fatal("POST /tasks/generate が登録されていない")
	}
	if got == http.StatusOK {
		t.Fatalf("POST /tasks/generate = %d: 認証なしで通った", got)
	}
	if exec.called {
		t.Fatal("認証を通らずに実行器が呼ばれた")
	}
}

// バージョン付きの vendor と、URL が変わらない自前アセットで Cache-Control を分けること。
func TestStaticCacheControlSeparatesVendorFromOwnAssets(t *testing.T) {
	t.Parallel()

	router := NewRouter(&builder.AppHandlers{}, "")

	tests := []struct {
		target string
		want   string
	}{
		{"/static/vendor/bootstrap-5.3.8/bootstrap.min.css", vendorCacheControl},
		{"/static/css/app.css", ownAssetCacheControl},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.target, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d, want 200", tt.target, rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
		})
	}
}

// CSP が全レスポンスに付き、script-src が緩められていないこと。
func TestResponsesCarryContentSecurityPolicy(t *testing.T) {
	t.Parallel()

	router := NewRouter(&builder.AppHandlers{}, "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	policy := rec.Header().Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("Content-Security-Policy が付いていない")
	}
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP に %q が無い: %s", want, policy)
		}
	}
	// script-src の 'unsafe-inline' は、インラインスクリプトを外した意味を消します
	// （assets の TestTemplatesHaveNoInlineScripts が対になっています）。
	scriptSrc := cspDirective(policy, "script-src")
	if scriptSrc == "" {
		t.Fatalf("script-src が無い: %s", policy)
	}
	if strings.Contains(scriptSrc, "unsafe-inline") || strings.Contains(scriptSrc, "unsafe-eval") {
		t.Errorf("script-src が緩められています: %s", scriptSrc)
	}
	// 音声は署名付き URL へ 302 するため、media-src が送り先を許す必要があります。
	if !strings.Contains(cspDirective(policy, "media-src"), "https://storage.googleapis.com") {
		t.Errorf("media-src が署名付き URL のホストを許していない: %s", policy)
	}
}

// cspDirective は CSP から 1 ディレクティブ分を取り出します。無ければ空文字を返します。
func cspDirective(policy, name string) string {
	for directive := range strings.SplitSeq(policy, ";") {
		directive = strings.TrimSpace(directive)
		if after, ok := strings.CutPrefix(directive, name+" "); ok {
			return after
		}
	}
	return ""
}

// 圧縮が効いていること。画面は日本語 UTF-8（1 文字 3 バイト）でよく縮むのに、
// これまで無圧縮で配信していました。
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
		t.Fatalf("解凍できない: %v", err)
	}
	if !strings.Contains(string(body), "Bootstrap") {
		t.Error("解凍した中身が Bootstrap の CSS でない")
	}
	if len(body) <= rec.Body.Len() {
		t.Errorf("圧縮後 %d バイトが元の %d バイトを下回っていない", rec.Body.Len(), len(body))
	}
}

// CSP 以外の防御ヘッダーも全レスポンスに付くこと。どれも 1 行で入る割に、
// 抜けても画面は正常に見えるため気付けません。
func TestResponsesCarrySecurityHeaders(t *testing.T) {
	t.Parallel()

	router := NewRouter(&builder.AppHandlers{}, "")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	want := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "same-origin",
		"Strict-Transport-Security": hstsMaxAge,
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// autoplay は塞ぎません（メディア再生が壊れます）。
	policy := rec.Header().Get("Permissions-Policy")
	if policy == "" {
		t.Error("Permissions-Policy が付いていない")
	}
	if strings.Contains(policy, "autoplay") {
		t.Errorf("Permissions-Policy が autoplay を塞いでいます: %s", policy)
	}
}
