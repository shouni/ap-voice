package cmd

import (
	"fmt"

	"github.com/shouni/clibase"
	"github.com/spf13/cobra"

	"github.com/shouni/ap-voice/internal/config"
)

// options は CLI が受け取る、実行ごとに変わるパラメータです。
//
// デプロイ先が決める設定（API キー・モデル一覧・エンジンの URL など）は
// config.Config が環境変数から読むため、ここには置きません。
type options struct {
	InputFile  string
	OutputFile string
	Mode       string
	// AIModel は --model / -g の値です。空なら PreRunE が GEMINI_MODELS の
	// 先頭で埋めます。
	AIModel string
}

// opts は、実行のパラメータです。
var opts options

// cfg は、環境変数から読み込んだアプリ設定です。PreRunE が組み立てます。
var cfg *config.Config

// Execute は、アプリケーションのメインエントリポイントです。
func Execute() {
	clibase.Execute(clibase.App{
		Name:     "ap-voice",
		AddFlags: addAppPersistentFlags,
		PreRunE:  initAppPreRunE,
		Commands: []*cobra.Command{
			generateCmd,
		},
	})
}

// initAppPreRunE は、コマンド実行前に設定の読み込みと検証を行います。
func initAppPreRunE(_ *cobra.Command, _ []string) error {
	loaded, err := config.LoadConfig()
	if err != nil {
		return err
	}
	if err := loaded.ValidateEssentialConfig(); err != nil {
		return err
	}
	cfg = loaded

	// フラグが環境変数に勝ちます。未指定なら一覧の先頭を既定として使います。
	if opts.AIModel == "" {
		opts.AIModel = cfg.AI.GeminiModel
	}

	return nil
}

// addAppPersistentFlags は、アプリケーション固有の永続フラグをルートコマンドに追加します。
func addAppPersistentFlags(rootCmd *cobra.Command) {
	rootCmd.PersistentFlags().StringVarP(&opts.InputFile, "input", "i", "", "入力ソースURI。Web URL、GCS (gs://)を指定します。")
	rootCmd.PersistentFlags().StringVarP(&opts.OutputFile, "output", "o", "", "生成されたスクリプトをVOICEVOXエンジンで合成し、指定されたパスに出力します (例: output.wav, gs://my-bucket/audio.wav)。")
	rootCmd.PersistentFlags().StringVarP(&opts.Mode, "mode", "m", "duet", "スクリプト生成モード。'dialogue', 'solo', 'duet' などを指定します。")
	rootCmd.PersistentFlags().StringVarP(&opts.AIModel, "model", "g", "", "使用する Google Gemini モデル名（未指定なら環境変数 GEMINI_MODELS の先頭を使います）")

	if err := rootCmd.MarkPersistentFlagRequired("input"); err != nil {
		panic(fmt.Sprintf("failed to mark 'input' flag as required: %v", err))
	}
}
