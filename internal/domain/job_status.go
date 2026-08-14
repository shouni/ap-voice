package domain

import "github.com/shouni/go-job-kit/jobstatus"

// JobStatus は、ジョブの進行状況に ap-voice の成果物の在り処を足した型です。
//
// go-job-kit の Status は**成果物の場所を持ちません**。単一 URI・出力ディレクトリ・
// 複数 URI とサービスごとに形が違うため、埋め込んだ型を利用側が定義する決まりです。
// Go は埋め込みを JSON でフラットに展開するので、保存済みの status.json は
// そのまま読み書きできます。
//
// **これが無いと「できた」としか言えません。** 状態だけ見ても音声の在り処が
// 分からず、投入した側は成果物へ辿り着けません。
type JobStatus struct {
	jobstatus.Status
	// AudioURI は音声の保存先（gs://）です。合成が完了したジョブにだけ入ります。
	//
	// **署名付き URL はここに置きません。** 1 時間で切れるうえ発行に計算が要るので、
	// 30 秒ごとのポーリングで返すには向きません。再生できるリンクが要るときは
	// 都度発行する別の口を使います。
	AudioURI string `json:"audio_uri,omitempty"`
	// ScriptURI は台本の保存先（gs://）です。generate の時点から入ります。
	ScriptURI string `json:"script_uri,omitempty"`
}
