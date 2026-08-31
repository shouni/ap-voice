// Package assets は、プロンプト・話者一覧・HTML テンプレートを埋め込みリソースとして提供します。
package assets

import "embed"

// promptDir はプロンプトの置き場です。ファイル名がそのままモード名になります
// （prompts/tech_duet.md → mode="tech_duet"）。接頭辞はジャンルで、一覧が
// ジャンルごとに固まって並ぶためのものです（入力の型は front matter が持ちます）。
const promptDir = "prompts"

// PromptFiles は埋め込まれたプロンプトファイル群です。
//
//go:embed prompts/*.md
var PromptFiles embed.FS

// SpeakersJSON は VOICEVOX エンジンの /speakers 応答をそのまま保存したものです。
//
// どの話者を使うかはアプリの方針なので、go-voicevox ではなくここが持ちます。
// ライブラリは応答の構造を解釈するだけで、一覧を同梱しません。
//
// 更新は取得し直して置き換えるだけです。手で書き写さないので、エンジンが増やしたスタイルを
// 取りこぼすことがありません。整形して保存するのは、エンジン更新時の差分を読めるようにするためです。
//
//	curl -s "$VOICEVOX_API_URL/speakers" |
//	  python3 -c 'import json,sys; json.dump(json.load(sys.stdin), sys.stdout, ensure_ascii=False, indent=2)' \
//	  > assets/speakers.json
//
// ここから読むのは語彙（誰がどのスタイルを持つか）だけです。スタイル ID は使いません—
// エンジンのビルドで変わるため、go-voicevox が起動時に実物へ問い合わせます。
//
//go:embed speakers.json
var SpeakersJSON []byte

// Templates は Web 面の HTML テンプレートです。
//
//go:embed templates/*.html templates/partials/*.html
var Templates embed.FS

// StaticFiles は Web 面の静的ファイルです。ディレクトリごと埋め込むので、
// ファイルを足せば配信は自動で効きます。
//
//go:embed static
var StaticFiles embed.FS
