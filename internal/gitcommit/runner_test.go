package gitcommit

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	name   string
	args   []string
	stderr string
	err    error
}

func (f *fakeRunner) Run(name string, args []string, o RunOpts) error {
	f.name = name
	f.args = args
	if o.Stderr != nil {
		io.WriteString(o.Stderr, f.stderr)
	}
	return f.err
}

func TestGitAddsSafeConfig(t *testing.T) {
	f := &fakeRunner{}
	if _, err := (Git{R: f, Root: "/r"}).Out(nil, "status"); err != nil {
		t.Fatal(err)
	}
	want := []string{"-C", "/r", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"-c", "submodule.recurse=false", "-c", "diff.ignoreSubmodules=all", "status"}
	if f.name != "git" || !reflect.DeepEqual(f.args, want) {
		t.Errorf("got %s %v, want git %v", f.name, f.args, want)
	}
}

func TestGitErrorEscapesStderr(t *testing.T) {
	f := &fakeRunner{stderr: "bad\x1b[31m\n", err: errors.New("exit status 1")}
	_, err := (Git{R: f, Root: "/r"}).Out(nil, "status")
	if err == nil {
		t.Fatal("エラーになるはず")
	}
	if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "^[") {
		t.Errorf("エスケープされていない: %q", err.Error())
	}
}

func TestGitWith(t *testing.T) {
	g := Git{Env: []string{"A=1"}}
	h := g.With("B=2")
	if len(g.Env) != 1 || !reflect.DeepEqual(h.Env, []string{"A=1", "B=2"}) {
		t.Errorf("g=%v h=%v", g.Env, h.Env)
	}
}

func TestExecRunnerEnvAndNilStdin(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh がない")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var out bytes.Buffer
		if err := (ExecRunner{}).Run("sh", []string{"-c", `printf %s "$SIGIL_T"`}, RunOpts{Env: []string{"SIGIL_T=x"}, Stdout: &out}); err != nil || out.String() != "x" {
			t.Errorf("Env: out=%q err=%v", out.String(), err)
		}
		out.Reset()
		// Stdin が nil なら /dev/null につながり、cat はすぐ EOF になる
		if err := (ExecRunner{}).Run("sh", []string{"-c", "cat; echo done"}, RunOpts{Stdout: &out}); err != nil || out.String() != "done\n" {
			t.Errorf("Stdin: out=%q err=%v", out.String(), err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("タイムアウト")
	}
}
