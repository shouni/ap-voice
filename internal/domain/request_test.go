package domain

import (
	"errors"
	"strings"
	"testing"
)

// TestRequestValidate は、コマンドごとに揃っているべき入力を検証します。
//
// 揃っていない入力は何度実行しても同じように失敗するため、外部を 1 つも
// 叩く前に弾きます。この判断は Web フォーム・API・Worker の 3 つが同じ関数で
// 行うので、ここが緩むと 3 つとも緩みます。
func TestRequestValidate(t *testing.T) {
	t.Parallel()

	const jobID = "voice-20260814-020913-b1b8b2f9e8d7"
	const output = "gs://bucket/voice/" + jobID + "/audio.wav"

	tests := []struct {
		name string
		req  Request
		// wantErr が空なら通ること、そうでなければその文言を含むこと。
		wantErr string
		// unknown は ErrUnknownCommand で包まれているべきかです。
		unknown bool
	}{
		{
			name: "generate は入力ソースと出力先が要る",
			req:  Request{Command: CommandGenerate, JobID: jobID, InputURI: "https://example.com", OutputURI: output},
		},
		{
			name:    "generate に入力ソースが無い",
			req:     Request{Command: CommandGenerate, JobID: jobID, OutputURI: output},
			wantErr: "入力ソース",
		},
		{
			name: "generate_and_synthesize も入力ソースが要る",
			req:  Request{Command: CommandGenerateAndSynthesize, JobID: jobID, InputURI: "https://example.com", OutputURI: output},
		},
		{
			name:    "generate_and_synthesize に入力ソースが無い",
			req:     Request{Command: CommandGenerateAndSynthesize, JobID: jobID, OutputURI: output},
			wantErr: "入力ソース",
		},
		{
			// 台本はペイロードに載りません。在り処はジョブ ID だけです。
			name: "synthesize はジョブIDで台本を指す",
			req:  Request{Command: CommandSynthesize, JobID: jobID, OutputURI: output},
		},
		{
			name:    "synthesize に台本の在り処が無い",
			req:     Request{Command: CommandSynthesize, OutputURI: output},
			wantErr: "台本が特定できません",
		},
		{
			// 空白だけの値は「指定した」とみなしません。フォームから来ます。
			name:    "空白だけのジョブID",
			req:     Request{Command: CommandSynthesize, JobID: "   ", OutputURI: output},
			wantErr: "台本が特定できません",
		},
		{
			name:    "出力先が無い",
			req:     Request{Command: CommandGenerate, JobID: jobID, InputURI: "https://example.com"},
			wantErr: "出力先",
		},
		{
			// 空を generate とみなしません。持ち込んだ台本が黙って捨てられ、
			// 課金と出力の両方が変わる取り違えになります。
			name:    "command が空",
			req:     Request{JobID: jobID, InputURI: "https://example.com", OutputURI: output},
			wantErr: "command が指定されていません",
			unknown: true,
		},
		{
			name:    "未知の command",
			req:     Request{Command: "compose", JobID: jobID, OutputURI: output},
			wantErr: `"compose"`,
			unknown: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.req.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want %q を含むエラー", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want に %q を含む", err, tt.wantErr)
			}
			// 未知のコマンドは型で見分けられる必要があります。文言で判別すると、
			// 文言を直した時点で呼び出し側の分岐が黙って外れます。
			if got := errors.Is(err, ErrUnknownCommand); got != tt.unknown {
				t.Errorf("errors.Is(err, ErrUnknownCommand) = %v, want %v", got, tt.unknown)
			}
		})
	}
}

// TestRequestValidateNamesEveryCommand は、コマンドの取り違えを直せる案内が
// 出ることを検証します。
//
// 受け付ける値が 3 つあり、そのうちどれなのかはエラー文にしか出ません。
// 増えたコマンドを案内に足し忘れると、呼び出し側は存在しない値だと思い込みます。
func TestRequestValidateNamesEveryCommand(t *testing.T) {
	t.Parallel()

	err := Request{OutputURI: "gs://bucket/o.wav"}.Validate()
	if err == nil {
		t.Fatal("command が空なのにエラーになりません")
	}
	for _, command := range []Command{CommandGenerate, CommandSynthesize, CommandGenerateAndSynthesize} {
		if !strings.Contains(err.Error(), string(command)) {
			t.Errorf("案内に %q がありません: %v", command, err)
		}
	}
}
