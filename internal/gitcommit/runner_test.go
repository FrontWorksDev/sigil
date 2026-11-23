package gitcommit

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
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
