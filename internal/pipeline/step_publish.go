package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shouni/ap-voice/internal/domain"
)

// Signer は PublishStep が必要とする署名機能だけを表します。
// remoteio.Store がそのまま満たします。
type Signer interface {
	SignURL(ctx context.Context, name, method string, expires time.Duration) (string, error)
}

// PublishStep は、スクリプトの公開処理を実行する具象構造体です。
type PublishStep struct {
	voice  domain.Voice
	signer Signer
}

// NewPublishStep は PublishStep の新しいインスタンスを作成します。
func NewPublishStep(voice domain.Voice, signer Signer) *PublishStep {
	return &PublishStep{
		voice:  voice,
		signer: signer,
	}
}

// PublishScript は台本だけを保存します。generate はここで終わります。
//
// 音声まで作らないのは、台本を確認・修正してから合成へ進めるようにするためです。
// 合成は分単位かかるので、直す前提の台本で先に走らせても捨てることになります。
func (r *PublishStep) PublishScript(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if outputURI == "" {
		return "", errors.New("出力先パス(outputURI)が指定されていません")
	}

	slog.InfoContext(ctx, "台本の保存を開始します。", "output_path", outputURI)
	if err := r.voice.UploadScript(ctx, outputURI, script); err != nil {
		return "", fmt.Errorf("台本の保存に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "台本の保存が完了しました。", "output_path", outputURI)

	// **署名付き URL は返しません。** 署名は対象の存在を確かめないため、まだ作っていない
	// 音声の URL を署名でき、通知に載せると 404 のリンクを配ることになります。
	// この段階で見るべきものは台本で、それは詳細画面が表示します。
	return "", nil
}

// Run は台本を保存してから音声を合成します。
//
// **順序が逆だと、合成の時間切れで台本ごと失われます。** 台本と音声をまとめて作る
// 経路では、生成した台本はまだどこにも保存されていません。音声を先に作ると、
// 合成が上限に達した時点で Gemini の生成結果が消え、やり直すしかなくなります。
// 先に保存しておけば、詳細画面から合成だけをやり直せます。
//
// 台本を毎回書き直すのは、修正済みの台本を渡された場合に、保存されている台本と
// 実際に喋った内容がずれないようにするためです。
func (r *PublishStep) Run(ctx context.Context, outputURI string, script domain.Script) (string, error) {
	if outputURI == "" {
		return "", errors.New("出力先パス(outputURI)が指定されていません")
	}

	if err := r.voice.UploadScript(ctx, outputURI, script); err != nil {
		return "", fmt.Errorf("台本の保存に失敗しました (%s): %w", outputURI, err)
	}

	slog.InfoContext(ctx, "音声合成を開始します。", "output_path", outputURI)
	if err := r.voice.UploadWav(ctx, outputURI, script.Lines); err != nil {
		return "", fmt.Errorf("音声合成パイプラインの実行に失敗しました (%s): %w", outputURI, err)
	}
	slog.InfoContext(ctx, "音声合成が完了しました。", "output_path", outputURI)

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
	return r.signer.SignURL(ctx, outputURI, "GET", time.Hour)
}
