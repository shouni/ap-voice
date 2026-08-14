package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/ap-voice/internal/domain"

	"github.com/shouni/go-remote-io/remoteio"
)

// PublishStep は、スクリプトの公開処理を実行する具象構造体です。
type PublishStep struct {
	voice  domain.Voice
	signer remoteio.URLSigner
}

// NewPublishStep は PublishStep の新しいインスタンスを作成します。
func NewPublishStep(voice domain.Voice, signer remoteio.URLSigner) *PublishStep {
	return &PublishStep{
		voice:  voice,
		signer: signer,
	}
}

// PublishScript は台本だけを保存します。generate はここで終わります。
//
// 音声まで作らないのは、台本を確認・修正してから合成へ進めるようにするためです。
// 合成は分単位かかるので、直す前提の台本で先に走らせても捨てることになります。
func (r *PublishStep) PublishScript(ctx context.Context, outputURI string, lines []domain.ScriptLine) (string, error) {
	if outputURI == "" {
		return "", errors.New("出力先パス(outputURI)が指定されていません")
	}

	slog.InfoContext(ctx, "台本の保存を開始します。", "output_path", outputURI)
	if err := r.voice.UploadScript(ctx, outputURI, lines); err != nil {
		return "", fmt.Errorf("台本の保存に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "台本の保存が完了しました。", "output_path", outputURI)

	return r.publicURLOrEmpty(ctx, outputURI), nil
}

// Run は音声を合成して保存します。台本も隣に書き直します。
func (r *PublishStep) Run(ctx context.Context, outputURI string, lines []domain.ScriptLine) (string, error) {
	if outputURI == "" {
		return "", errors.New("出力先パス(outputURI)が指定されていません")
	}

	slog.InfoContext(ctx, "音声合成を開始します。", "output_path", outputURI)
	if err := r.voice.UploadWav(ctx, outputURI, lines); err != nil {
		return "", fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "音声合成が完了しました。", "output_path", outputURI)

	// 台本も書き直します。API から修正済みの台本を渡された場合、保存されている
	// 台本と実際に喋った内容がずれてしまうためです。
	if err := r.voice.UploadScript(ctx, outputURI, lines); err != nil {
		return "", fmt.Errorf("台本の保存に失敗しました (%s): %w", outputURI, err)
	}

	return r.publicURLOrEmpty(ctx, outputURI), nil
}

// publicURLOrEmpty は署名付き URL を返します。作れなくても処理は続けます。
// 成果物は保存できているので、URL が無いことを失敗にする理由がありません。
func (r *PublishStep) publicURLOrEmpty(ctx context.Context, outputURI string) string {
	publicURL, err := r.buildPublicURL(ctx, outputURI)
	if err != nil {
		slog.WarnContext(ctx, "公開URLの生成に失敗したため、URLなしで続行します。", "output_path", outputURI, "error", err)
		return ""
	}
	return publicURL
}

func (r *PublishStep) buildPublicURL(ctx context.Context, outputURI string) (string, error) {
	if r.signer == nil {
		return "", nil
	}
	return r.signer.GenerateSignedURL(ctx, outputURI, "GET", time.Hour)
}
