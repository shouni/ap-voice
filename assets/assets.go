// Package assets は、プロンプト・話者一覧・HTML テンプレートを埋め込みリソースとして提供します。
package assets

import (
	"embed"

	"github.com/shouni/go-prompt-kit/resource"
)

// promptDir はテンプレートの置き場です。ファイル名がそのままモード名になります
// （prompts/duet.md → mode="duet"）。ディレクトリで区切っている以上、ファイル名側の
// 接頭辞は重複なので付けません。
const promptDir = "prompts"

// PromptFiles は埋め込まれたプロンプトファイル群です。
//
//go:embed prompts/*.md
var PromptFiles embed.FS

// SpeakersJSON は VOICEVOX エンジンの /speakers 応答をそのまま保存したものです。
//
// **どの話者を使うかはアプリの方針**なので、go-voicevox ではなくここが持ちます。
// ライブラリは応答の構造を解釈するだけで、一覧を同梱しません。
//
// 更新は取得し直して置き換えるだけです。手で書き写さないので、エンジンが増やしたスタイルを
// 取りこぼすことがありません。整形して保存するのは、エンジン更新時の差分を読めるようにするためです。
//
//	curl -s "$VOICEVOX_API_URL/speakers" |
//	  python3 -c 'import json,sys; json.dump(json.load(sys.stdin), sys.stdout, ensure_ascii=False, indent=2)' \
//	  > assets/speakers.json
//
// ここから読むのは語彙（誰がどのスタイルを持つか）だけです。**スタイル ID は使いません** —
// エンジンのビルドで変わるため、go-voicevox が起動時に実物へ問い合わせます。
//
//go:embed speakers.json
var SpeakersJSON []byte

// Templates は Web 面の HTML テンプレートです。
//
//go:embed templates/*.html
var Templates embed.FS

// StaticFiles は Web 面の静的ファイル（CSS）です。
//
//go:embed static
var StaticFiles embed.FS

// LoadPrompts は埋め込まれたプロンプトファイルを読み込みます。
func LoadPrompts() (map[string]string, error) {
	return resource.Load(PromptFiles, promptDir, "")
}
