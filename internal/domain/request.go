package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Command は、1件のリクエストにどこまでやらせるかを表します。
//
// 台本生成と音声合成を別の入口にしているのは、台本が「成果物であると同時に入力でもある」
// ためです。読みや話者を直して合成だけやり直したいことは普通に起こり、1つの入口しか
// 無いと、そのたびに Gemini の生成からやり直すことになります。費用も待ち時間も無駄に
// なるうえ、出力が前回と変わってしまって「直したかった1行」以外まで別物になります。
type Command string

const (
	// CommandGenerate は、入力ソースから台本を生成して保存します。**音声は作りません。**
	// 台本を確認・修正してから合成へ進めるようにするためです。
	CommandGenerate Command = "generate"
	// CommandSynthesize は、台本から音声を作ります。台本は Script で直接渡すか、
	// JobID で保存済みのものを指します。Gemini は呼ばれません。
	CommandSynthesize Command = "synthesize"
	// CommandGenerateAndSynthesize は、台本を作ってそのまま音声まで作ります。
	//
	// **確認を省く選択です。** 台本を直せるようになった今、確認を挟むかどうかは
	// 利用者が決められます。短いモード（ニュース、楽曲紹介）のように直す前提が
	// 薄いものでは、履歴を開いてもう一度押す手間の方が大きくなります。
	//
	// 分岐は増えません。resolveScript は「synthesize 以外」を生成扱いにし、
	// publish は「generate 以外」を音声まで作る扱いにするため、この値は
	// 両方の望ましい側へ落ちます。
	CommandGenerateAndSynthesize Command = "generate_and_synthesize"
)

// ErrUnknownCommand は、未知の Command が指定されたことを表します。
var ErrUnknownCommand = errors.New("domain: unknown command")

// Request はパイプライン実行に必要な入力パラメータを保持するモデルです。
// Cloud Tasks のペイロード（JSON）としてそのまま流れるため、タグが投入側との契約になります。
type Request struct {
	// Command は実行する処理です。省略できません。
	//
	// generate は台本まで、synthesize は音声までを担当します。分けているのは、
	// 台本が成果物であると同時に入力でもあるためです。読みや話者を直してから
	// 合成できるようにすると、直したい 1 行のために生成をやり直さずに済みます。
	//
	// 空を generate とみなさないのは、Script を渡したのに command を書き忘れた場合に、
	// 渡した台本が黙って捨てられ、Gemini の生成が走ってしまうためです。
	// 課金と出力の両方が変わる取り違えを、既定値で吸収する価値はありません。
	Command Command `json:"command"`

	// JobID は 1 回の実行を識別します。成果物の置き場もこの ID から決まります。
	// 発行するのは投入側（Web 面）で、Worker 面はログと通知で使うだけです。
	JobID string `json:"job_id,omitempty"`

	// InputURI は generate の入力ソース（Web URL / gs://）です。
	InputURI string `json:"input_uri,omitempty"`
	// OutputURI は WAV の出力先です。台本は同名の .json として隣に置かれます。
	OutputURI string `json:"output_uri"`
	// Mode は generate の台本形式（solo / dialogue / duet）です。
	Mode string `json:"mode,omitempty"`
	// AIModel は generate に使う Gemini モデル名です。空なら GEMINI_MODELS の先頭を使います。
	AIModel string `json:"ai_model,omitempty"`

	// Script は synthesize の入力となる台本です。
	// PublishStep が書き出した audio.json の lines をそのまま貼り戻せる形にしてあります。
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
	case CommandGenerate, CommandGenerateAndSynthesize:
		if strings.TrimSpace(r.InputURI) == "" {
			return errors.New("入力ソース(input_uri)が指定されていません")
		}
	case CommandSynthesize:
		// 台本は直接渡すか、保存済みのものを JobID で指すかのどちらかです。
		if len(r.Script) == 0 && strings.TrimSpace(r.JobID) == "" {
			return errors.New("台本が特定できません。script を直接渡すか、保存済み台本の job_id を指定してください")
		}
	case "":
		return fmt.Errorf("%w: command が指定されていません（%q / %q / %q）",
			ErrUnknownCommand, CommandGenerate, CommandSynthesize, CommandGenerateAndSynthesize)
	default:
		return fmt.Errorf("%w: %q（%q / %q / %q）",
			ErrUnknownCommand, r.Command, CommandGenerate, CommandSynthesize, CommandGenerateAndSynthesize)
	}

	return nil
}
