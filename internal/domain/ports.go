package domain

import (
	"context"
)

// Pipeline は、処理を行うインターフェースです。
type Pipeline interface {
	// Execute は、すべての依存関係を構築し実行します。
	Execute(ctx context.Context) error
}

// GenerateRunner は、ナレーションスクリプト生成を実行する責務を持つインターフェースです。
type GenerateRunner interface {
	Run(ctx context.Context) (string, error)
}

// PublishRunner は、生成されたスクリプトの公開処理を実行する責務を持つインターフェースです。
type PublishRunner interface {
	Run(ctx context.Context, scriptContent string) error
}

// PromptBuilder は、プロンプト文字列を生成する責務を定義します。
type PromptBuilder interface {
	Build(mode string, data any) (string, error)
}
