package pipeline

import (
	"context"

	"ap-voice/internal/domain"
)

type (
	// GenerateRunner は、ナレーションスクリプト生成を実行する責務を持つインターフェースです。
	GenerateRunner interface {
		Run(ctx context.Context, req domain.Request) (string, error)
	}
	// PublishRunner は、生成されたスクリプトの公開処理を実行する責務を持つインターフェースです。
	PublishRunner interface {
		Run(ctx context.Context, outputURI string, content string) (string, error)
	}
)
