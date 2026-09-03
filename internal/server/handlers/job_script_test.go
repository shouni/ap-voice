package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/ap-voice/internal/domain"
)

// 同じルートが、相手の求める表現で答えること。
//
// MCP サーバーの baseClient は全リクエストに Accept: application/json を付けるため、
// ルートを 1 本にしても機械側は JSON を受け取り続けます。
func TestScriptServesBothAudiences(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	h := &Handler{repo: &storedScriptRepo{}}

	t.Run("機械には素の JSON を返す", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/"+jobID+"/script", "application/json", jobID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("Content-Disposition = %q, want empty（機械はファイルとして受け取らない）", got)
		}
	})

	// 画面から開いたときに本文が表示されるだけだと、確認はできても手元に残せません。
	t.Run("人にはファイルとして返す", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/"+jobID+"/script", browserAccept, jobID))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get("Content-Disposition"); got == "" {
			t.Error("Content-Disposition が無い（ダウンロードにならない）")
		}
	})

	// 同じ URL が Accept で中身を変えるため、キャッシュへ伝える必要があります。
	t.Run("Vary: Accept を立てること", func(t *testing.T) {
		t.Parallel()

		rec := httptest.NewRecorder()
		h.Script(rec, requestWithJobID(http.MethodGet, "/jobs/"+jobID+"/script", "application/json", jobID))

		if got := rec.Header().Get("Vary"); got != "Accept" {
			t.Errorf("Vary = %q, want %q", got, "Accept")
		}
	})
}

// putScript は PUT /api/jobs/{jobID}/script を呼びます。
func putScript(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	req := httptest.NewRequest("PUT", "/jobs/"+jobID+"/script", strings.NewReader(body))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.ScriptUpdate(rec, req)
	return rec
}

// TestAPIUpdateScriptSharesValidationWithTheForm は、API が画面と同じ検証を
// 通ることを検証します。
//
// 片方だけ緩いと、そちらから実在しない組み合わせが入ります。合成時に
// 合成時に既定へ黙って落ちるため、保存できてしまうと「指定したのに
// 違う声で喋る」形でしか現れません。
func TestScriptUpdateSharesValidationWithTheForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "実在しない話者",
			body:       `{"title":"t","lines":[{"speaker":"存在しない","style":"ノーマル","text":"本文"}]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "一覧にありません",
		},
		{
			// 春日部つむぎの talk スタイルは「ノーマル」だけです。
			name:       "話者が持たないスタイル",
			body:       `{"title":"t","lines":[{"speaker":"春日部つむぎ","style":"ヒソヒソ","text":"本文"}]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "というスタイルはありません",
		},
		{
			name:       "空の台本",
			body:       `{"title":"t","lines":[]}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "台本が空です",
		},
		{
			name:       "壊れたJSON",
			body:       `{"title":`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "JSONの解釈に失敗しました",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &savingRepo{}
			rec := putScript(t, apiHandler(t, repo), tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("body = %s, want に %q を含む", rec.Body.String(), tt.wantErr)
			}
			// 弾いたものは保存しません。
			if repo.calls != 0 {
				t.Errorf("検証に失敗したのに %d 回保存しています", repo.calls)
			}
		})
	}
}

// TestAPIUpdateScriptSavesAndReturnsTheCleanedScript は、通った台本が
// 整えられた形で保存され、そのまま返ることを検証します。
func TestScriptUpdateSavesAndReturnsTheCleanedScript(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	rec := putScript(t, apiHandler(t, repo),
		`{"title":"  題名  ","lines":[
			{"speaker":"ずんだもん","style":"あまあま","text":"  前後の空白は落ちるのだ  "},
			{"speaker":"四国めたん","style":"ノーマル","text":"   "},
			{"speaker":"四国めたん","style":"セクシー","text":"三行目"}
		]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got domain.Script
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答のデコードに失敗しました: %v", err)
	}

	// 本文が空の行は落ちます。行を消す手段でもあります。
	if len(got.Lines) != 2 {
		t.Fatalf("行数 = %d, want 2", len(got.Lines))
	}
	if got.Title != "題名" {
		t.Errorf("Title = %q, want 題名", got.Title)
	}
	if got.Lines[0].Text != "前後の空白は落ちるのだ" {
		t.Errorf("本文の前後の空白が残っています: %q", got.Lines[0].Text)
	}
	// 返したものと保存したものが同じであること。
	if repo.calls != 1 {
		t.Fatalf("保存回数 = %d, want 1", repo.calls)
	}
	if len(repo.saved.Lines) != len(got.Lines) || repo.saved.Title != got.Title {
		t.Errorf("保存 %+v と応答 %+v が食い違います", repo.saved, got)
	}
}

// TestAPIUpdateScriptRejectsBadJobID は、不正なジョブ ID を保存前に弾くことを検証します。
func TestScriptUpdateRejectsBadJobID(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	h := apiHandler(t, repo)

	req := httptest.NewRequest("PUT", "/jobs/x/script", strings.NewReader(`{"lines":[]}`))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", "../../evil")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.ScriptUpdate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if repo.calls != 0 {
		t.Error("不正なジョブIDで保存しています")
	}
}

// TestSynthesizeDispatchesOnBody は、POST /jobs/{jobID}/synthesize が本文の形で
// 経路を分けることを検証します。
//
// JSON（本文なし）は保存済みの台本をそのまま合成し、フォームは編集中の台本を
// 保存してから合成します。1 本の URL に寄せたので、この分岐が崩れると機械の
// 呼び出しがフォームの解析へ落ちるか、画面の保存が黙って飛ばされます。
func TestSynthesizeDispatchesOnBody(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	withJobID := func(req *http.Request) *http.Request {
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("jobID", jobID)
		return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))
	}

	t.Run("JSON は保存済みの台本を合成する", func(t *testing.T) {
		t.Parallel()

		var order []string
		repo := &savingStateRepo{}
		h := apiHandler(t, repo)
		h.queue = recordingQueue{order: &order}

		req := withJobID(httptest.NewRequest("POST", "/jobs/"+jobID+"/synthesize", nil))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.Synthesize(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		if repo.calls != 0 {
			t.Errorf("JSON なのに台本が保存されています（%d 回）", repo.calls)
		}
		if loc := rec.Header().Get("Location"); loc != "/jobs/"+jobID {
			t.Errorf("Location = %q, want /jobs/%s", loc, jobID)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["command"] != string(domain.CommandSynthesize) {
			t.Errorf("command = %q, want synthesize", body["command"])
		}
	})

	t.Run("フォームは台本を保存してから合成する", func(t *testing.T) {
		t.Parallel()

		var order []string
		repo := &savingStateRepo{}
		h := detailHandler(t, repo)
		h.queue = recordingQueue{order: &order}

		form := url.Values{}
		form.Set("title", "t")
		form.Add("speaker", "ずんだもん")
		form.Add("style", "ノーマル")
		form.Add("text", "こんにちは")
		req := withJobID(httptest.NewRequest("POST", "/jobs/"+jobID+"/synthesize", strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.Synthesize(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
		}
		if repo.calls != 1 {
			t.Errorf("台本の保存回数 = %d, want 1", repo.calls)
		}
		if len(order) != 1 || order[0] != "enqueue" {
			t.Errorf("投入 = %v, want [enqueue]", order)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html（画面を描き直す）", ct)
		}
	})
}

// getScript は GET /history/{jobID}/script を呼びます。
func getScript(t *testing.T, h *Handler, jobID string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/history/"+jobID+"/script", nil)
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("jobID", jobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, ctx))

	rec := httptest.NewRecorder()
	h.Script(rec, req)
	return rec
}

// TestScriptDownloadsTheStoredScript は、保存済みの台本がそのまま JSON として
// 落ちてくることを検証します。
//
// ファイル名を付ける Content-Disposition が要ります。無いとブラウザは
// JSON を画面に表示するだけで、ダウンロードにならない経路があります。
func TestScriptDownloadsTheStoredScript(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	want := domain.Script{
		Title: "テスト台本",
		Lines: []domain.ScriptLine{
			{Speaker: "ずんだもん", Style: "ノーマル", Text: "こんにちはなのだ"},
		},
	}
	h := apiHandler(t, &storedScriptRepo{script: want})

	rec := getScript(t, h, jobID)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Content-Disposition"), `attachment; filename="`+jobID+`.json"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}

	var got domain.Script
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("台本が JSON として読めません: %v (body=%q)", err, rec.Body.String())
	}
	if got.Title != want.Title || len(got.Lines) != len(want.Lines) || got.Lines[0] != want.Lines[0] {
		t.Errorf("script = %+v, want %+v", got, want)
	}
}

// TestScriptRejectsBadJobID は、ジョブ ID の検証を通ることを確認します。
// ID はそのままファイル名になるため、素通りさせられません。
func TestScriptRejectsBadJobID(t *testing.T) {
	t.Parallel()

	h := apiHandler(t, &storedScriptRepo{})

	rec := getScript(t, h, "../../etc/passwd")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
