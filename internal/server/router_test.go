package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shouni/gcp-kit/auth"
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
		TaskAuth: auth.NewTaskVerifier("https://worker.example.run.app",
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
