package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/shouni/go-gemini-client/gemini"
	"github.com/shouni/go-remote-io/remoteio"
	"github.com/shouni/go-web-exact/v2/ports"

	"prototypus-ai-doc-go/internal/config"
	"prototypus-ai-doc-go/internal/domain"
)

// TemplateData はプロンプトテンプレートに渡すデータ構造です。
type TemplateData struct {
	InputText string
}

// GenerateRunner は generate コマンドの実行に必要な依存とオプションを保持します。
type GenerateRunner struct {
	options       *config.Config
	extractor     ports.Extractor
	promptBuilder domain.PromptBuilder
	aiClient      gemini.ContentGenerator
	reader        remoteio.Reader
}

// NewGenerateRunner は、依存関係を注入して GenerateRunner の新しいインスタンスを生成します。
func NewGenerateRunner(
	options *config.Config,
	extractor ports.Extractor,
	promptBuilder domain.PromptBuilder,
	aiClient gemini.ContentGenerator,
	reader remoteio.Reader,
) *GenerateRunner {
	return &GenerateRunner{
		options:       options,
		extractor:     extractor,
		promptBuilder: promptBuilder,
		aiClient:      aiClient,
		reader:        reader,
	}
}

// Run は、入力ソースからコンテンツを読み込み、AIモデルを使用してナレーションスクリプトを生成する一連の処理を実行します。
func (gr *GenerateRunner) Run(ctx context.Context) (string, error) {
	inputContent, err := gr.readInputContent(ctx)
	if err != nil {
		return "", err
	}
	slog.Info("処理開始", "mode", gr.options.Mode, "model", gr.options.AIModel, "input_size", len(inputContent))
	slog.Info("AIによるスクリプト生成を開始します...")

	data := TemplateData{
		InputText: string(inputContent),
	}
	promptContent, err := gr.promptBuilder.Build(gr.options.Mode, data)
	if err != nil {
		return "", err
	}

	generatedResponse, err := gr.aiClient.GenerateContent(ctx, gr.options.AIModel, promptContent)
	if err != nil {
		return "", fmt.Errorf("スクリプト生成に失敗しました: %w", err)
	}
	slog.Info("AI スクリプト生成完了", "script_length", len(generatedResponse.Text))

	return generatedResponse.Text, nil
}

// --------------------------------------------------------------------------------
// ヘルパー関数 (入力処理)
// --------------------------------------------------------------------------------

func (gr *GenerateRunner) readFromURL(ctx context.Context) ([]byte, error) {
	slog.Info("URLからコンテンツを取得中", "url", gr.options.ScriptURL, "timeout", gr.options.HTTPTimeout.String())

	text, hasBodyFound, err := gr.extractor.FetchAndExtractText(ctx, gr.options.ScriptURL)
	if err != nil {
		return nil, fmt.Errorf("URLからのコンテンツ取得に失敗しました: %w", err)
	}
	if !hasBodyFound {
		slog.Info("記事本文が見つかりませんでした。タイトルのみで処理を続行します。", "url", gr.options.ScriptURL)
	}
	return []byte(text), nil
}

// readInputContent は入力ソースからコンテンツを読み込みます。
func (gr *GenerateRunner) readInputContent(ctx context.Context) ([]byte, error) {
	var inputContent []byte
	var err error

	switch {
	case gr.options.ScriptURL != "":
		inputContent, err = gr.readFromURL(ctx)
	default:
		// URLが指定されていない場合、--script-fileで指定されたパスからコンテンツを読み込む。
		// パスが空文字列または"-"の場合、標準入力がソースとなる。
		path := gr.options.ScriptFile
		rc, openErr := gr.reader.Open(ctx, path)
		if openErr != nil {
			return nil, fmt.Errorf("入力ソースのオープンに失敗しました (%s): %w", path, openErr)
		}

		// 読み取りとクローズを同時に行い、エラーを結合
		readContent, readErr := io.ReadAll(rc)
		closeErr := rc.Close()

		if joinedErr := errors.Join(readErr, closeErr); joinedErr != nil {
			return nil, fmt.Errorf("入力ソース(%s)の処理に失敗しました: %w", path, joinedErr)
		}
		inputContent = readContent
	}

	// 共通のエラーチェック (URL読み込みエラー、または標準入力が空の場合の判定)
	if err != nil {
		// --script-file 指定なし、または明示的な "-" 指定の両方をチェック
		isStdinEmpty := (gr.options.ScriptFile == "" || gr.options.ScriptFile == "-")
		if errors.Is(err, io.EOF) && len(inputContent) == 0 && isStdinEmpty {
			return nil, fmt.Errorf("標準入力が空です。文章を入力してください。")
		}
		return nil, fmt.Errorf("コンテンツの読み込み中にエラーが発生しました: %w", err)
	}

	trimmedContent := strings.TrimSpace(string(inputContent))
	if len(trimmedContent) < config.MinInputContentLength {
		return nil, fmt.Errorf("入力されたコンテンツが短すぎます (最低%dバイト必要です)。", config.MinInputContentLength)
	}

	return []byte(trimmedContent), nil
}
