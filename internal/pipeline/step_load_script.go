package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/ap-voice/internal/domain"
)

// LoadScriptStep は、保存済みの台本を JobID で読み出します（synthesize）。
//
// 台本はペイロードに載りません（理由は domain.Request.JobID）。持ち込みの台本も
// 投入側が先に保存するので、読み出しは 1 経路です — ペイロードの台本を優先する分岐が
// かつてありましたが、投入側がどれも台本を載せなくなったあとは、テストだけが
// 通る道になっていました。
type LoadScriptStep struct {
	scripts domain.ScriptStore
}

// NewLoadScriptStep は LoadScriptStep を生成します。
func NewLoadScriptStep(scripts domain.ScriptStore) *LoadScriptStep {
	return &LoadScriptStep{scripts: scripts}
}

// Name は工程名です。
func (*LoadScriptStep) Name() string { return "load_script" }

// Execute は、台本を読み出して sc.Script に載せます。
func (s *LoadScriptStep) Execute(ctx context.Context, sc *Context) error {
	jobID := sc.Request.JobID
	script, err := s.scripts.Load(ctx, jobID)
	if err != nil {
		err = fmt.Errorf("保存済み台本の読み込みに失敗しました (%s): %w", jobID, err)
		// 台本が無いのと読めないのは別物です。synthesize の投入は台本を保存した
		// あとに行うので、無いまま届いたものは待っても現れません。読めないほう
		// （GCS の一時障害）は再配信で直り得るので、そのまま返します。
		if errors.Is(err, domain.ErrScriptNotFound) {
			return worker.Permanent(err)
		}
		return err
	}
	if len(script.Lines) == 0 {
		// 空の台本が後から埋まることはありません。
		return worker.Permanent(fmt.Errorf("保存済み台本が空です (%s)", jobID))
	}
	sc.Script = script
	return nil
}
