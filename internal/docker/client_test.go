package docker

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestExecRunnerRunEnvLaterWins は、本物のプロセスで、extraEnv が sigil 自身の環境の同じ名前に勝つことを確かめる。
// キーチェーンの値がホストのシェルの同じ名前に負けないための前提なので、偽物の Runner ではなく実際に実行する
// （docker の代わりに env を実行する。docker は要らない）。
func TestExecRunnerRunEnvLaterWins(t *testing.T) {
	t.Setenv("SIGIL_TEST_RUNENV", "host")
	var out bytes.Buffer
	if err := (ExecRunner{}).RunEnv("/usr/bin/env", nil, []string{"SIGIL_TEST_RUNENV=keychain"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "SIGIL_TEST_RUNENV=") {
			got = append(got, line)
		}
	}
	if !reflect.DeepEqual(got, []string{"SIGIL_TEST_RUNENV=keychain"}) {
		t.Errorf("子プロセスの環境 = %v", got)
	}
}

type call struct {
	name string
	args []string
}

// fakeRunner は呼ばれた argv を記録し、順に用意した応答を返す。
type fakeRunner struct {
	calls     []call
	responses []response
}

type response struct {
	stdout string
	err    error
}

func (f *fakeRunner) Run(name string, args []string, stdout, stderr io.Writer) error {
	f.calls = append(f.calls, call{name, append([]string(nil), args...)})
	if len(f.responses) == 0 {
		return nil
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	if stdout != nil {
		io.WriteString(stdout, r.stdout)
	}
	return r.err
}

func TestInspectFailureIsNotTreatedAsExisting(t *testing.T) {
	// 回帰テスト: docker が失敗しても空行を出力することがある（シェル版で inspect が実際にそうだった）。
	// 出力ではなく終了コードで判定し、失敗は「存在する」とも「ない」ともせず、エラーにする。
	// （「ない」とすると、Docker Desktop が止まっているときに sigil stop が嘘の「ありません」を出す）
	f := &fakeRunner{responses: []response{{stdout: "\n", err: errors.New("exit status 1")}}}
	st, _, err := Client{R: f}.Inspect("sigil-x-1")
	if err == nil {
		t.Fatalf("エラーにならない（状態 %v）", st)
	}
	if !strings.Contains(err.Error(), "Docker Desktop") {
		t.Errorf("案内がない: %v", err)
	}
}

func TestInspectStates(t *testing.T) {
	cases := []struct {
		out        string
		wantState  State
		wantLayout string
	}{
		{"", Missing, ""},
		{"\n", Missing, ""},
		{"sigil-x-1\trunning\t2\n", Running, "2"},
		{"sigil-x-1\texited\t2\n", Stopped, "2"},
		{"sigil-x-1\tcreated\t2\n", Stopped, "2"},
		{"sigil-x-1\trunning\t\n", Running, ""},
		// name フィルタは部分一致なので、名前が完全に一致する行だけを見る
		{"sigil-x-1-old\trunning\t2\n", Missing, ""},
		{"sigil-x-1-old\trunning\t2\nsigil-x-1\texited\t\n", Stopped, ""},
	}
	for _, tc := range cases {
		f := &fakeRunner{responses: []response{{stdout: tc.out}}}
		st, layout, err := Client{R: f}.Inspect("sigil-x-1")
		if err != nil {
			t.Fatal(err)
		}
		if st != tc.wantState || layout != tc.wantLayout {
			t.Errorf("%q → %v %q", tc.out, st, layout)
		}
	}
}

func TestInspectArgs(t *testing.T) {
	f := &fakeRunner{}
	Client{R: f}.Inspect("c")
	want := []string{"ps", "-a", "--filter", "name=c", "--format", "{{.Names}}\t{{.State}}\t{{.Label \"sigil.layout\"}}"}
	if f.calls[0].name != "docker" || !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("argv = %v", f.calls[0])
	}
}

func TestImageExists(t *testing.T) {
	f := &fakeRunner{responses: []response{{}, {err: errors.New("no such image")}}}
	c := Client{R: f}
	if !c.ImageExists("img") {
		t.Error("ある")
	}
	if c.ImageExists("img") {
		t.Error("ない")
	}
	if !reflect.DeepEqual(f.calls[0].args, []string{"image", "inspect", "img"}) {
		t.Errorf("argv = %v", f.calls[0].args)
	}
}

func TestRemove(t *testing.T) {
	f := &fakeRunner{responses: []response{{}, {err: errors.New("daemon down")}}}
	c := Client{R: f}
	if err := c.Remove("c"); err != nil {
		t.Errorf("Remove = %v", err)
	}
	if err := c.Remove("c"); err == nil {
		t.Error("失敗を返すはず")
	}
	if !reflect.DeepEqual(f.calls[0].args, []string{"rm", "-f", "c"}) {
		t.Errorf("argv = %v", f.calls[0].args)
	}
}

func TestCreateAndStart(t *testing.T) {
	f := &fakeRunner{}
	c := Client{R: f}
	s := sampleSpec()
	if err := c.Create(s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls[0].args, RunArgs(s)) {
		t.Errorf("run argv = %v", f.calls[0].args)
	}
	if err := c.Start("c"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.calls[1].args, []string{"start", "c"}) {
		t.Errorf("start argv = %v", f.calls[1].args)
	}
}

func TestSetupWakaTimeSkipsWithoutKey(t *testing.T) {
	f := &fakeRunner{responses: []response{{err: errors.New("exit status 1")}}}
	if err := (Client{R: f}).SetupWakaTime("c"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Errorf("キーがないのに書き込もうとした: %v", f.calls)
	}
}

func TestSetupWakaTimeWritesFromContainerEnv(t *testing.T) {
	f := &fakeRunner{}
	if err := (Client{R: f}).SetupWakaTime("c"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"exec", "c", "sh", "-c", `[ -n "${WAKATIME_API_KEY:-}" ]`},
		{"exec", "c", "sh", "-c", `umask 077; printf "[settings]\napi_key = %s\n" "$WAKATIME_API_KEY" > "$HOME/.wakatime.cfg"`},
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %v", f.calls)
	}
	for i := range want {
		if !reflect.DeepEqual(f.calls[i].args, want[i]) {
			t.Errorf("call %d = %v", i, f.calls[i].args)
		}
		// キーの値はコンテナ内の環境変数から読む。ホストの argv に値は載らない
		for _, a := range f.calls[i].args {
			if strings.Contains(a, "WAKATIME_API_KEY=") {
				t.Errorf("argv に値がある: %s", a)
			}
		}
	}
}

// fakeEnvRunner は RunEnv も持つ Runner。RunEnv に渡された環境を記録する。
type fakeEnvRunner struct {
	fakeRunner
	envCalls []call
	envs     [][]string
}

func (f *fakeEnvRunner) RunEnv(name string, args, extraEnv []string, stdout, stderr io.Writer) error {
	f.envCalls = append(f.envCalls, call{name, append([]string(nil), args...)})
	f.envs = append(f.envs, append([]string(nil), extraEnv...))
	return nil
}

func TestCreatePassesSecretEnvToEnvRunner(t *testing.T) {
	f := &fakeEnvRunner{}
	s := sampleSpec()
	s.Envs = []string{"A"}
	s.SecretEnv = []string{"A=v"}
	if err := (Client{R: f}).Create(s); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || len(f.envCalls) != 1 {
		t.Fatalf("Run = %v, RunEnv = %v", f.calls, f.envCalls)
	}
	if !reflect.DeepEqual(f.envs[0], []string{"A=v"}) {
		t.Errorf("extraEnv = %v", f.envs[0])
	}
	args := f.envCalls[0].args
	hasE := false
	for i, a := range args {
		// 値は環境で渡す。argv（ps で見える）には載せない
		if a == "v" || strings.Contains(a, "A=v") {
			t.Errorf("argv に値がある: %v", args)
		}
		if a == "-e" && i+1 < len(args) && args[i+1] == "A" {
			hasE = true
		}
	}
	if !hasE {
		t.Errorf("-e A がない: %v", args)
	}
}

func TestCreateSecretEnvNeedsEnvRunner(t *testing.T) {
	f := &fakeRunner{}
	s := sampleSpec()
	s.Envs = []string{"A"}
	s.SecretEnv = []string{"A=v"}
	err := (Client{R: f}).Create(s)
	if err == nil {
		t.Fatal("エラーにならない")
	}
	if strings.Contains(err.Error(), "A=v") {
		t.Errorf("エラーに値がある: %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("docker を呼んだ: %v", f.calls)
	}
}
