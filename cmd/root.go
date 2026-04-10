package cmd

import (
	"github.com/shouni/clibase"
	"github.com/spf13/cobra"

	"prototypus-ai-doc-go/internal/config"
)

// ReviewConfig は、レビュー実行のパラメータです
var opts config.Config

// Execute は、アプリケーションのメインエントリポイントです。
func Execute() {
	clibase.Execute(clibase.App{
		Name:     "prototypus-ai-doc",
		AddFlags: addAppPersistentFlags,
		PreRunE:  initAppPreRunE,
		Commands: []*cobra.Command{
			generateCmd,
		},
	})
}

// initAppPreRunE は、コマンド実行前にログ設定やクライアント初期化を行います。
func initAppPreRunE(cmd *cobra.Command, args []string) error {
	opts.FillDefaults(config.LoadConfig())
	opts.Normalize()

	return nil
}

// addAppPersistentFlags は、アプリケーション固有の永続フラグをルートコマンドに追加します。
func addAppPersistentFlags(rootCmd *cobra.Command) {
	rootCmd.PersistentFlags().StringVarP(&opts.ScriptURL, "script-url", "u", "", "Webページからコンテンツを取得するためのURL。")
	rootCmd.PersistentFlags().StringVarP(&opts.ScriptFile, "script-file", "f", "", "入力スクリプトファイルのパス ('-'を指定すると標準入力から読み込みます。)")
	rootCmd.PersistentFlags().StringVarP(&opts.OutputFile, "output-file", "o", "", "生成されたスクリプトを保存するファイルのパス。省略時は標準出力 (stdout) に出力します。")
	rootCmd.PersistentFlags().StringVarP(&opts.Mode, "mode", "m", "duet", "スクリプト生成モード。'dialogue', 'solo', 'duet' などを指定します。")
	rootCmd.PersistentFlags().StringVarP(&opts.VoicevoxOutput, "voicevox", "v", "", "生成されたスクリプトをVOICEVOXエンジンで合成し、指定されたパスに出力します (例: output.wav, gs://my-bucket/audio.wav)。")
	rootCmd.PersistentFlags().StringVarP(&opts.AIModel, "model", "g", config.DefaultModel, "使用する Google Gemini モデル名 (例: gemini-2.5-flash, gemini-2.5-pro)")
	rootCmd.PersistentFlags().DurationVar(&opts.HTTPTimeout, "http-timeout", config.DefaultHTTPTimeout, "Webリクエストのタイムアウト時間 (例: 15s, 1m)。")
}
