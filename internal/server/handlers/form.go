package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/ap-voice/assets"
	"github.com/shouni/ap-voice/internal/domain"
)

// 投入フォームのタブです。入力の型ごとに分かれています。
//
// 1 枚のフォームに全部載せていた頃は、素のテキストを入れたまま楽曲紹介モードを
// 選べてしまい、選んだ時点では何も起こらず、生成に入ってからデコードで落ちていました。
// 入力の型が違うものは、同じ画面に並べても選び分けられません。
const (
	// tabInput は入力ソース（Web URL / gs:// の文書）から台本を書かせるタブです。
	tabInput = "input"
	// tabRecipe は楽曲レシピから宣伝ナレーションを書かせるタブです。
	tabRecipe = "recipe"
	// tabScript は完成済みの台本 JSON をそのまま合成するタブです。Gemini を通しません。
	tabScript = "script"
)

// formView はフォーム画面に渡す値です。
type formView struct {
	baseView
	// TextModes と RecipeModes は、front matter の input で分けたモードです。
	// タブごとに別の <select> を描きます。1 つの select を JavaScript で
	// 組み替えると、選択肢の出し分けの根拠が画面側に移ってしまいます。
	TextModes   []assets.Mode
	RecipeModes []assets.Mode
	Models      []string
	Message     string
	Error       string
	Form        domain.Request
	// ActiveTab は再描画時に開くタブです。投入したタブを開き直します。
	// 常に先頭を開くと、結果の文言だけが見えて入力内容が隠れたタブに残ります。
	ActiveTab string
	// MusicJobID と ScriptJSON は、入力ソース以外のタブの入力の残りです。
	// domain.Request には収まらない（URI に変換済み／台本そのもの）ため別に持ちます。
	MusicJobID string
	ScriptJSON string
}

// Home は投入フォームを表示します。
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, h.formView(r, domain.Request{Command: domain.CommandGenerate}, tabInput))
}

// formView は、タブごとのモード一覧を詰めた描画用の値を作ります。
func (h *Handler) formView(r *http.Request, form domain.Request, activeTab string) formView {
	return formView{
		baseView:    h.base(r),
		TextModes:   assets.FilterModes(h.modes, assets.InputText),
		RecipeModes: assets.FilterModes(h.modes, assets.InputRecipe),
		Models:      h.models,
		Form:        form,
		ActiveTab:   activeTab,
	}
}

// createJobForm は、フォームの内容を検証し、Worker 面へ実行を引き渡します
// （POST /jobs をフォームから呼んだとき）。
//
// ここでは合成を待ちません。分単位かかるため、リクエストの中で完了させられないためです。
// 結果は Slack 通知と出力先で受け取ります。
func (h *Handler) createJobForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{}, tabInput, "フォームの解析に失敗しました")
		return
	}

	switch r.FormValue("source") {
	case tabRecipe:
		h.enqueueFromRecipe(w, r)
	case tabScript:
		h.enqueueFromScript(w, r)
	default:
		h.enqueueFromInput(w, r)
	}
}

// enqueueFromInput は「入力ソース」タブの投入です。Web URL / gs:// の文書を
// Gemini に読ませて台本を書かせます。
func (h *Handler) enqueueFromInput(w http.ResponseWriter, r *http.Request) {
	command, ok := h.generateCommand(w, r, tabInput)
	if !ok {
		return
	}
	h.enqueueGenerate(w, r, command, strings.TrimSpace(r.FormValue("input_uri")), tabInput, "")
}

// enqueueFromRecipe は「楽曲レシピ」タブの投入です。
//
// 入力ソースの代わりに楽曲生成サービスのジョブ ID を受け取ります。
// gs:// のパスを貼らせるより短く、打ち間違えれば ID の形で弾けます。
func (h *Handler) enqueueFromRecipe(w http.ResponseWriter, r *http.Request) {
	command, ok := h.generateCommand(w, r, tabRecipe)
	if !ok {
		return
	}

	musicJobID := strings.TrimSpace(r.FormValue("music_job_id"))
	inputURI, err := h.recipeInputURI(musicJobID)
	if err != nil {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{Mode: r.FormValue("mode")}, tabRecipe, err.Error())
		return
	}
	h.enqueueGenerate(w, r, command, inputURI, tabRecipe, musicJobID)
}

// recipeInputURI は、楽曲生成サービスのジョブ ID から楽曲レシピの場所を組み立てます。
//
// 動画生成サービスと同じ規則です（gs://<musicBucket>/music/<jobID>/recipe.json）。
// 下書き（drafts/）は見ません — 宣伝ナレーションは公開した曲に付けるものだからです。
func (h *Handler) recipeInputURI(musicJobID string) (string, error) {
	if musicJobID == "" {
		return "", fmt.Errorf("楽曲のジョブIDを入力してください")
	}
	// Validate はパスとして安全かだけを見ます。形式は見ません—
	// "not-a-job-id" も通ってしまい、存在しない場所を指すジョブが投入されます。
	// SortKey は時刻を取り出せない ID で空を返すので、形の確認に使えます。
	if err := jobid.Validate(musicJobID); err != nil {
		return "", fmt.Errorf("楽曲のジョブIDに使えない文字が含まれています: %w", err)
	}
	if jobid.SortKey(musicJobID) == "" {
		return "", fmt.Errorf("楽曲のジョブIDの形式が正しくありません（例: music-20260814-031712-5c812debb05f）")
	}
	if strings.TrimSpace(h.musicBucket) == "" {
		return "", fmt.Errorf("AP_MUSIC_BUCKET が設定されていないため、ジョブIDから楽曲レシピを解決できません")
	}
	return fmt.Sprintf("gs://%s/music/%s/recipe.json", strings.TrimSpace(h.musicBucket), musicJobID), nil
}

// generateCommand は、生成系タブのボタンが送った command を確かめます。
//
// この画面から選べる command は限られます。台本 JSON タブ以外に synthesize を
// 渡す口は無く、受け付ける値をここで明示しておくと、画面から来るものと
// API から来るものの境界がはっきりします。
func (h *Handler) generateCommand(w http.ResponseWriter, r *http.Request, tab string) (domain.Command, bool) {
	command := domain.Command(r.FormValue("command"))
	if command != domain.CommandGenerate && command != domain.CommandGenerateAndSynthesize {
		h.renderError(w, r, http.StatusBadRequest, domain.Request{}, tab, "この画面から実行できない処理です")
		return "", false
	}
	return command, true
}

// enqueueGenerate は、生成系タブの共通の投入経路です。
func (h *Handler) enqueueGenerate(w http.ResponseWriter, r *http.Request, command domain.Command, inputURI, tab, musicJobID string) {
	// 出力先はフォームから受け取りません。ジョブ ID から導くことで、1 ジョブの
	// 成果物が必ず 1 つのプレフィックスにまとまり、あとから一覧・削除できます。
	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		h.renderError(w, r, http.StatusInternalServerError, domain.Request{}, tab, "ジョブIDの発行に失敗しました")
		return
	}

	req := domain.Request{
		Command:   command,
		JobID:     jobID,
		InputURI:  inputURI,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
		Mode:      r.FormValue("mode"),
		AIModel:   r.FormValue("ai_model"),
	}

	// worker 側でも Execute の冒頭で検証しますが、投入前に弾けば
	// 「タスクにはなったが必ず失敗する」状態を作らずに済みます（submit の中です）。
	status, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderError(w, r, status, req, tab, err.Error())
		return
	}
	acceptedAt(w, jobID)

	// 投入した内容をそのまま残します。同じソースからモードを変えて
	// もう1本作るのが普通の使い方で、空に戻すと URL を貼り直すことになります。
	// ジョブ ID と出力先は毎回発行し直すため、残っていても次の投入には影響しません。
	view := h.formView(r, req, tab)
	view.MusicJobID = musicJobID
	view.Message = acceptedMessage(command, req.JobID)
	h.render(w, http.StatusAccepted, view)
}

// enqueueFromScript は「台本 JSON」タブの投入です。
//
// Gemini を通しません。貼られた台本をそのまま合成します。ジョブ ID を発行して
// その場所へ保存するので、既存ジョブの差し替えと同じ経路が新規作成になります
// （保存先はジョブ ID から決まり、ジョブが既にあるかどうかは問いません）。
func (h *Handler) enqueueFromScript(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.FormValue("script_json"))
	if raw == "" {
		h.renderScriptError(w, r, http.StatusBadRequest, raw, "台本のJSONを貼り付けてください")
		return
	}

	var script domain.Script
	if err := json.Unmarshal([]byte(raw), &script); err != nil {
		h.renderScriptError(w, r, http.StatusBadRequest, raw, "JSONの解釈に失敗しました: "+err.Error())
		return
	}

	// 話者とスタイルは API と同じ検証を通します（validateScript）。
	cleaned, err := h.validateScript(script)
	if err != nil {
		h.renderScriptError(w, r, http.StatusBadRequest, raw, err.Error())
		return
	}

	jobID, err := jobid.New(jobIDPrefix)
	if err != nil {
		h.renderScriptError(w, r, http.StatusInternalServerError, raw, "ジョブIDの発行に失敗しました")
		return
	}

	if err := h.repo.SaveScript(r.Context(), jobID, cleaned); err != nil {
		h.renderScriptError(w, r, http.StatusBadGateway, raw, "台本の保存に失敗しました")
		return
	}

	req := domain.Request{
		Command:   domain.CommandSynthesize,
		JobID:     jobID,
		OutputURI: h.layout.AudioURI(h.bucket, jobID),
	}
	status, err := h.submit(r.Context(), req)
	if err != nil {
		h.renderScriptError(w, r, status, raw, err.Error())
		return
	}
	acceptedAt(w, jobID)

	// 貼った JSON は残しません。投入した台本は履歴の詳細で編集できるため、
	// 同じものを貼ったまま二重に投入できる状態のほうが危険です。
	view := h.formView(r, domain.Request{Command: domain.CommandGenerate}, tabScript)
	view.Message = fmt.Sprintf("音声の作成を受け付けました（%s）。完了すると通知が届きます。", jobID)
	h.render(w, http.StatusAccepted, view)
}

// acceptedMessage は、受け付けた処理に応じた案内文を返します。
// どちらを押したかが分かる文面にします。まとめて作った場合は音声まで待つため、
// 「履歴に並びます」だけでは、次に何を待てばよいのか分かりません。
func acceptedMessage(command domain.Command, jobID string) string {
	if command == domain.CommandGenerateAndSynthesize {
		return fmt.Sprintf("台本と音声の作成を受け付けました（%s）。完了すると通知が届きます。", jobID)
	}
	return fmt.Sprintf("台本の作成を受け付けました（%s）。完了すると履歴に並びます。", jobID)
}

// renderError は、生成系タブの失敗を、そのタブを開いた状態で描き直します。
func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, status int, form domain.Request, tab, msg string) {
	view := h.formView(r, form, tab)
	view.MusicJobID = strings.TrimSpace(r.FormValue("music_job_id"))
	view.Error = msg
	h.render(w, status, view)
}

// renderScriptError は、台本 JSON タブの失敗を、貼った JSON を残したまま描き直します。
// ここでは残します。直して出し直すのが普通で、消えると貼り直しになります。
func (h *Handler) renderScriptError(w http.ResponseWriter, r *http.Request, status int, raw, msg string) {
	view := h.formView(r, domain.Request{Command: domain.CommandGenerate}, tabScript)
	view.ScriptJSON = raw
	view.Error = msg
	h.render(w, status, view)
}

func (h *Handler) render(w http.ResponseWriter, status int, view formView) {
	h.renderTemplate(w, status, "home.html", &view)
}
