package domain

import "github.com/shouni/go-job-firestore/jobfirestore"

// JobStatus は、ジョブの進行状況に ap-voice の成果物の在り処を足した型です。
//
// go-job-firestore の Status は**成果物の場所を持ちません**。単一 URI・出力ディレクトリ・
// 複数 URI とサービスごとに形が違うため、埋め込んだ型を利用側が定義する決まりです。
// 埋め込みは Firestore でも JSON でもフラットに展開されるので、ドキュメントと
// レスポンス JSON は同じ形になります。
//
// **firestore タグを省かないでください。** 省くと保存されるフィールド名が Go の
// 識別子（AudioURI）になり、json タグで組み立てた既存のレスポンスと食い違います。
//
// **これが無いと「できた」としか言えません。** 状態だけ見ても音声の在り処が
// 分からず、投入した側は成果物へ辿り着けません。
type JobStatus struct {
	jobfirestore.Status
	// AudioURI は音声の保存先（gs://）です。合成が完了したジョブにだけ入ります。
	//
	// **署名付き URL はここに置きません。** 1 時間で切れるうえ発行に計算が要るので、
	// 30 秒ごとのポーリングで返すには向きません。再生できるリンクが要るときは
	// 都度発行する別の口を使います。
	AudioURI string `json:"audio_uri,omitempty" firestore:"audio_uri,omitempty"`
	// ScriptURI は台本の保存先（gs://）です。generate の時点から入ります。
	ScriptURI string `json:"script_uri,omitempty" firestore:"script_uri,omitempty"`
	// Mode は台本を作ったときの形式（tech_solo など）です。
	//
	// **状態に残さないと、後から振り返る手段がありません。** 出来上がった台本から
	// 分かるのは話者の組み合わせまでで、ずんだもん 1 人の台本が tech_solo だったか
	// tech_howto だったかは区別できません。長さや口調の目安を実測で見直すには、
	// どのモードが何を作ったかが要ります。
	Mode string `json:"mode,omitempty" firestore:"mode,omitempty"`
}

// CarryFrom は、前回の記録から**今回の組み立てでは分からない値**を引き継ぎます。
//
// ワーカーは状態が変わるたびにタスクから状態を組み立て直すため、これが無いと
// running を書いた時点で前回の在り処が消えます。**モードも同じです** —
// synthesize は台本がすでにある前提のコマンドなのでモードを持たず、
// 引き継がないと generate → synthesize と二段で進めたジョブだけがモードを失います
// （画面から作った場合は必ずこの経路です）。
//
// 埋まっている値は上書きしません。今回の組み立てで分かったことのほうが新しく、
// 合成し直しで在り処が変わったのに古い値へ戻されては困ります。
func (s *JobStatus) CarryFrom(prev *JobStatus) {
	if prev == nil {
		return
	}
	if s.AudioURI == "" {
		s.AudioURI = prev.AudioURI
	}
	if s.ScriptURI == "" {
		s.ScriptURI = prev.ScriptURI
	}
	if s.Mode == "" {
		s.Mode = prev.Mode
	}
}
