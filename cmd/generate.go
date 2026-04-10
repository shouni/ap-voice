package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"prototypus-ai-doc-go/internal/builder"
)

// generateCmd はナレーションスクリプト生成のメインコマンドです。
var generateCmd = &cobra.Command{
	Use:   "generate",
	Short: "AIにナレーションスクリプトを生成させます。",
	Long: `AIに渡す元となる文章を指定し、ナレーションスクリプトを生成します。
Webページやファイル、標準入力から文章を読み込むことができます。`,
	RunE: generateCommand,
}

// generateCommand は、AIによるナレーションスクリプトを生成し、指定されたURIのクラウドストレージにWAVをアップロード
func generateCommand(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// 制約チェック
	if cmd.Flags().Changed("voicevox") && cmd.Flags().Changed("output-file") {
		return fmt.Errorf("--voicevoxオプションと--output-fileオプションは同時に指定できません")
	}

	appCtx, err := builder.BuildContainer(ctx, &opts)
	if err != nil {
		// コンテナの構築エラーをラップして返す
		return fmt.Errorf("コンテナの構築に失敗しました: %w", err)
	}
	defer func() {
		if closeErr := appCtx.Close(); closeErr != nil {
			slog.ErrorContext(ctx, "コンテナのクローズに失敗しました", "error", closeErr)
		}
	}()

	err = appCtx.Pipeline.Execute(ctx)
	if err != nil {
		return err
	}

	return nil
}
