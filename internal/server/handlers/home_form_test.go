package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shouni/go-voicevox/speaker"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
)

// capturingQueue は、投入されたリクエストを覚えておく TaskQueue です。
type capturingQueue struct {
	got   domain.Request
	calls int
}

func (q *capturingQueue) Enqueue(_ context.Context, req domain.Request) error {
	q.got = req
	q.calls++
	return nil
}

// formHandler は、投入フォームを描画・処理できる Handler を返します。
// テンプレートは実物を読みます — タブの出し分けは描画結果でしか確かめられません。
func formHandler(t *testing.T, queue domain.TaskQueue, repo ScriptRepository) *Handler {
	t.Helper()

	registry, err := speaker.NewRegistry(assets.SpeakersJSON)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	tmpl := parseTemplates(t)
	modes, err := assets.LoadModes()
	if err != nil {
		t.Fatalf("LoadModes() error = %v", err)
	}

	return &Handler{
		queue:       queue,
		templates:   tmpl,
		modes:       modes,
		models:      []string{"gemini-test"},
		bucket:      "test",
		musicBucket: "ap-music",
		layout:      domain.NewStorageLayout(),
		repo:        repo,
		speakers:    registry,
	}
}

// postForm は投入フォームへ POST します。
func postForm(t *testing.T, h *Handler, values url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("POST", "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Enqueue(rec, req)
	return rec
}

// TestHomeSeparatesModesByInputKind は、タブごとに出す生成モードが
// 入力の型で分かれていることを検証します。
//
// 素のテキストのタブに楽曲紹介を出すと、選べるのに必ず失敗します。
// レシピを渡す前提のモードは、生成に入ってからデコードで落ちるため、
// 選んだ時点では何も起こらず、原因が分かるのは worker のログだけです。
func TestHomeSeparatesModesByInputKind(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, nil)

	rec := httptest.NewRecorder()
	h.Home(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	inputTab := sectionOf(t, body, `id="mode_input"`, "</select>")
	recipeTab := sectionOf(t, body, `id="mode_recipe"`, "</select>")

	if strings.Contains(inputTab, `value="music_promo"`) {
		t.Error("入力ソースのタブに music_promo が出ています")
	}
	if !strings.Contains(inputTab, `value="comedy_manzai"`) {
		t.Error("入力ソースのタブに素のテキストのモードが出ていません")
	}
	if !strings.Contains(recipeTab, `value="music_promo"`) {
		t.Error("楽曲レシピのタブに music_promo が出ていません")
	}
	if strings.Contains(recipeTab, `value="comedy_manzai"`) {
		t.Error("楽曲レシピのタブに素のテキストのモードが出ています")
	}

	// 台本 JSON のタブにはモードの選択そのものがありません。
	if !strings.Contains(body, `name="script_json"`) {
		t.Error("台本JSONのタブがありません")
	}
}

// sectionOf は、from から to までを切り出します。タブごとの選択肢を
// 見分けるために使います。
func sectionOf(t *testing.T, body, from, to string) string {
	t.Helper()

	i := strings.Index(body, from)
	if i < 0 {
		t.Fatalf("%q が見つかりません", from)
	}
	rest := body[i:]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("%q の後に %q がありません", from, to)
	}
	return rest[:j]
}

// openingTagOf は、marker を含む要素の開始タグを返します。
// class は id より前に書かれているため、marker から前方へ遡ります。
func openingTagOf(t *testing.T, body, marker string) string {
	t.Helper()

	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("%q が見つかりません", marker)
	}
	start := strings.LastIndex(body[:i], "<")
	if start < 0 {
		t.Fatalf("%q の開始タグが見つかりません", marker)
	}
	end := strings.Index(body[start:], ">")
	if end < 0 {
		t.Fatalf("%q の開始タグが閉じていません", marker)
	}
	return body[start : start+end]
}

// TestEnqueueFromRecipeBuildsURIFromJobID は、楽曲のジョブ ID から
// レシピの場所が組み立てられることを検証します。
//
// 動画生成サービスと同じ規則です。片方だけ規則が変わると、同じ ID を渡しているのに
// 一方だけ動く状態になり、どちらが正しいのか判断できなくなります。
func TestEnqueueFromRecipeBuildsURIFromJobID(t *testing.T) {
	t.Parallel()

	queue := &capturingQueue{}
	h := formHandler(t, queue, nil)

	rec := postForm(t, h, url.Values{
		"source":       {"recipe"},
		"command":      {"generate"},
		"music_job_id": {"music-20260814-031712-5c812debb05f"},
		"mode":         {"music_promo"},
		"ai_model":     {"gemini-test"},
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	const want = "gs://ap-music/music/music-20260814-031712-5c812debb05f/recipe.json"
	if queue.got.InputURI != want {
		t.Errorf("InputURI = %q, want %q", queue.got.InputURI, want)
	}
	if queue.got.Mode != "music_promo" {
		t.Errorf("Mode = %q", queue.got.Mode)
	}
}

// TestEnqueueFromRecipeRejectsBadJobID は、形式の違うジョブ ID を
// 投入前に弾くことを検証します。
//
// ID からパスを組み立てるので、形式が違えば存在しない場所を指します。
// 投入してしまうと、失敗が分かるのは worker が読みに行ったあとです。
func TestEnqueueFromRecipeRejectsBadJobID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		jobID   string
		wantErr string
	}{
		{name: "空", jobID: "", wantErr: "楽曲のジョブIDを入力してください"},
		{name: "形式違い", jobID: "not-a-job-id", wantErr: "形式が正しくありません"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			queue := &capturingQueue{}
			h := formHandler(t, queue, nil)

			rec := postForm(t, h, url.Values{
				"source": {"recipe"}, "command": {"generate"},
				"music_job_id": {tt.jobID}, "mode": {"music_promo"},
			})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("本文に %q がありません", tt.wantErr)
			}
			if queue.calls != 0 {
				t.Errorf("弾いたのに投入しています: %d 回", queue.calls)
			}
		})
	}
}

// TestEnqueueFromScriptCreatesJobWithoutGemini は、貼られた台本が
// 新しいジョブとして保存され、生成を挟まず合成へ回ることを検証します。
//
// 保存が投入より先です。台本はタスクに載せないため、届いた worker が
// 読みに行く先に既に無いと、その場で失敗します。
func TestEnqueueFromScriptCreatesJobWithoutGemini(t *testing.T) {
	t.Parallel()

	repo := &savingRepo{}
	queue := &capturingQueue{}
	h := formHandler(t, queue, repo)

	rec := postForm(t, h, url.Values{
		"source": {"script"},
		"script_json": {`{"title":"漫才","lines":[
			{"speaker":"ずんだもん","style":"ノーマル","text":"どうもなのだ"},
			{"speaker":"四国めたん","style":"ノーマル","text":"どうも。"}]}`},
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if repo.calls != 1 {
		t.Fatalf("保存が %d 回です", repo.calls)
	}
	if len(repo.saved.Lines) != 2 || repo.saved.Title != "漫才" {
		t.Errorf("保存された台本が違います: %+v", repo.saved)
	}
	if queue.got.Command != domain.CommandSynthesize {
		t.Errorf("Command = %q, want synthesize", queue.got.Command)
	}
	// 生成の入力は持ちません。Gemini を通さない経路なので、
	// 入力ソースやモードが混ざっていると生成が走ってしまいます。
	if queue.got.InputURI != "" || queue.got.Mode != "" {
		t.Errorf("生成の入力が混ざっています: InputURI=%q Mode=%q", queue.got.InputURI, queue.got.Mode)
	}
	if queue.got.JobID == "" {
		t.Error("ジョブIDが発行されていません")
	}
}

// TestEnqueueFromScriptRejectsBadScript は、壊れた JSON と実在しない
// 話者・スタイルを投入前に弾くことを検証します。
func TestEnqueueFromScriptRejectsBadScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{name: "空", json: "", wantErr: "貼り付けてください"},
		{name: "壊れたJSON", json: `{"title":`, wantErr: "JSONの解釈に失敗しました"},
		{
			name:    "実在しない話者",
			json:    `{"title":"t","lines":[{"speaker":"居ない人","style":"ノーマル","text":"本文"}]}`,
			wantErr: "一覧にありません",
		},
		{
			// 春日部つむぎの talk スタイルは「ノーマル」だけです。
			name:    "話者が持たないスタイル",
			json:    `{"title":"t","lines":[{"speaker":"春日部つむぎ","style":"ヒソヒソ","text":"本文"}]}`,
			wantErr: "というスタイルはありません",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := &savingRepo{}
			queue := &capturingQueue{}
			h := formHandler(t, queue, repo)

			rec := postForm(t, h, url.Values{"source": {"script"}, "script_json": {tt.json}})

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Errorf("本文に %q がありません", tt.wantErr)
			}
			if repo.calls != 0 || queue.calls != 0 {
				t.Errorf("弾いたのに保存/投入しています: save=%d enqueue=%d", repo.calls, queue.calls)
			}
		})
	}
}

// TestEnqueueFromScriptKeepsTheJSONOnError は、失敗したときに貼った JSON が
// 残ることを検証します。直して出し直すのが普通です。消えると貼り直しになります。
func TestEnqueueFromScriptKeepsTheJSONOnError(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, &savingRepo{})

	const bad = `{"title":"t","lines":[{"speaker":"居ない人","style":"ノーマル","text":"覚えていてほしい本文"}]}`
	rec := postForm(t, h, url.Values{"source": {"script"}, "script_json": {bad}})

	if !strings.Contains(rec.Body.String(), "覚えていてほしい本文") {
		t.Error("貼った JSON が消えています")
	}
}

// TestEnqueueReopensTheTabThatFailed は、失敗した画面が
// そのタブを開いた状態で描き直されることを検証します。
//
// 常に先頭のタブが開くと、エラーの文言だけが見えて、入力内容は
// 閉じたタブの中に残ります。何を直せばよいのか分かりません。
func TestEnqueueReopensTheTabThatFailed(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, &savingRepo{})

	rec := postForm(t, h, url.Values{
		"source": {"recipe"}, "command": {"generate"},
		"music_job_id": {"こわれたID"}, "mode": {"music_promo"},
	})

	pane := openingTagOf(t, rec.Body.String(), `id="tab-recipe"`)
	if !strings.Contains(pane, "show active") {
		t.Errorf("楽曲レシピのタブが開いていません: %q", pane)
	}
}

// TestEnqueueSaysWhichButtonWasPressed は、まとめて作ったときに
// 音声まで待つと分かる文面になることを検証します。
//
// どちらのボタンでも「台本の作成を受け付けました」と出ていた時期があり、
// 音声まで走っていることが画面から読み取れませんでした。
func TestEnqueueSaysWhichButtonWasPressed(t *testing.T) {
	t.Parallel()

	h := formHandler(t, &capturingQueue{}, nil)

	rec := postForm(t, h, url.Values{
		"source": {"input"}, "command": {"generate_and_synthesize"},
		"input_uri": {"gs://in/x.md"}, "mode": {"tech_solo"},
	})

	if !strings.Contains(rec.Body.String(), "台本と音声の作成を受け付けました") {
		t.Error("まとめて作ったことが文面に出ていません")
	}
}

// TestSubmitValidatesBeforeEnqueuing は、投入の作法が 1 箇所にまとまったことを
// 検証します。
//
// 検証 → 記録 → 投入の 3 つは 5 箇所へ写されており、1 箇所（詳細画面の保存経路）
// では検証が抜けていました。抜けても画面は普通に動き、必ず失敗するタスクが
// 積まれるだけなので、気付く機会がありません。
func TestSubmitValidatesBeforeEnqueuing(t *testing.T) {
	t.Parallel()

	t.Run("検証に落ちたら投入しない", func(t *testing.T) {
		t.Parallel()

		queue := &capturingQueue{}
		h := &Handler{queue: queue}

		// command が空のリクエストです。Validate が弾きます。
		status, err := h.submit(context.Background(), domain.Request{OutputURI: "gs://b/o.wav"})

		if err == nil {
			t.Fatal("検証に落ちるはずのリクエストが通りました")
		}
		if status != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
		}
		if queue.calls != 0 {
			t.Errorf("検証に落ちたのに %d 回投入しています", queue.calls)
		}
	})

	t.Run("通ったら投入して 202 を返す", func(t *testing.T) {
		t.Parallel()

		queue := &capturingQueue{}
		h := &Handler{queue: queue}
		req := domain.Request{
			Command:   domain.CommandSynthesize,
			JobID:     "voice-20260814-020913-b1b8b2f9e8d7",
			OutputURI: "gs://b/o.wav",
		}

		status, err := h.submit(context.Background(), req)

		if err != nil {
			t.Fatalf("submit() error = %v", err)
		}
		if status != http.StatusAccepted {
			t.Errorf("status = %d, want %d", status, http.StatusAccepted)
		}
		if queue.calls != 1 || queue.got.JobID != req.JobID {
			t.Errorf("投入されていません: calls=%d got=%+v", queue.calls, queue.got)
		}
	})
}
