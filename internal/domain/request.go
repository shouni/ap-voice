package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Command は、1件のリクエストにどこまでやらせるかを表します。
//
// 台本生成と音声合成を別の入口にしているのは、台本が「成果物であると同時に入力でもある」
// ためです。PublishRunner は WAV の隣へ台本を .json で書き出しており、読みや話者を直して
// 合成だけやり直したいことは普通に起こります。1つの入口しか無いと、そのたびに Gemini の
// 生成からやり直すことになり、費用も待ち時間も無駄になるうえ、出力が前回と変わってしまって
// 「直したかった1行」以外まで別物になります。
type Command string

const (
	// CommandGenerate は、入力ソースから台本を生成し、そのまま音声まで作ります。
	CommandGenerate Command = "generate"
	// CommandSynthesize は、渡された台本から音声だけを作ります。
	// 台本を生成し直さないため、Gemini は呼ばれません。
	CommandSynthesize Command = "synthesize"
)

// ErrUnknownCommand は、未知の Command が指定されたことを表します。
var ErrUnknownCommand = errors.New("未知の command です")

// Request はパイプライン実行に必要な入力パラメータを保持するモデルです。
// Cloud Tasks のペイロード（JSON）としてそのまま流れるため、タグが投入側との契約になります。
type Request struct {
	// Command は実行する処理です。省略できません。
	//
	// 空を generate とみなさないのは、Script を渡したのに command を書き忘れた場合に、
	// 渡した台本が黙って捨てられ、Gemini の生成が走ってしまうためです。
	// 課金と出力の両方が変わる取り違えを、既定値で吸収する価値はありません。
	Command Command `json:"command"`

	// InputURI は generate の入力ソース（Web URL / gs://）です。
	InputURI string `json:"input_uri,omitempty"`
	// OutputURI は WAV の出力先です。台本は同名の .json として隣に置かれます。
	OutputURI string `json:"output_uri"`
	// Mode は generate の台本形式（solo / dialogue / duet）です。
	Mode string `json:"mode,omitempty"`
	// AIModel は generate に使う Gemini モデル名です。空なら GEMINI_MODELS の先頭を使います。
	AIModel string `json:"ai_model,omitempty"`

	// Script は synthesize の入力となる台本です。
	// PublishRunner が書き出した .json をそのまま貼り戻せる形にしてあります。
	Script []ScriptLine `json:"script,omitempty"`
}

// Validate は、Command と、その Command に必要なフィールドが揃っているかを確かめます。
//
// 揃っていない入力は何度実行しても同じように失敗するため、パイプラインへ渡す前に弾きます。
func (r Request) Validate() error {
	if strings.TrimSpace(r.OutputURI) == "" {
		return errors.New("出力先(output_uri)が指定されていません")
	}

	switch r.Command {
	case CommandGenerate:
		if strings.TrimSpace(r.InputURI) == "" {
			return errors.New("入力ソース(input_uri)が指定されていません")
		}
	case CommandSynthesize:
		if len(r.Script) == 0 {
			return errors.New("台本(script)が空です。synthesize は台本を生成しないため、呼び出し側が渡す必要があります")
		}
	case "":
		return fmt.Errorf("%w: command が指定されていません（%q または %q）",
			ErrUnknownCommand, CommandGenerate, CommandSynthesize)
	default:
		return fmt.Errorf("%w: %q（%q または %q）",
			ErrUnknownCommand, r.Command, CommandGenerate, CommandSynthesize)
	}

	return nil
}
