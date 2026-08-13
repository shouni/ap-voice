package assets

import (
	_ "embed"
)

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
//	  > assets/speakers/speakers.json
//
// ここから読むのは語彙（誰がどのスタイルを持つか）だけです。**スタイル ID は使いません** —
// エンジンのビルドで変わるため、go-voicevox が起動時に実物へ問い合わせます。
//
//go:embed speakers/speakers.json
var SpeakersJSON []byte
