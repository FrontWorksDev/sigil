package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigil/internal/gitcommit"
	"sigil/internal/project"
)

// noCmd は、sigil commit 以外のテストの Deps.Cmd。呼ばれたら失敗させ、git や docker を本当に動かさない。
type noCmd struct{}

func (noCmd) Run(name string, args []string, _ gitcommit.RunOpts) error {
	return errors.New("Deps.Cmd は使わないはず: " + name + " " + strings.Join(args, " "))
}

type call struct {
	name string
	args []string
	env  []string // RunEnv で渡された追加の環境（Run のときは nil）
}

func (c call) String() string { return c.name + " " + strings.Join(c.args, " ") }

type handler struct {
	prefix string
	stdout string
	stderr string // 標準エラーに書く内容（docker build の進み具合など）
	err    error
	fn     func() (string, error) // あれば stdout / err の代わりに呼ぶ（呼ばれた時点の状態で応答を変えるため）
}

// fakeRunner は呼び出しを記録し、"docker ps -a" のような
// argv の先頭部分ごとに応答を返す。後から登録したものを優先する。
type fakeRunner struct {
	calls    []call
	handlers []handler
}

func (f *fakeRunner) on(prefix, stdout string, err error) {
	f.handlers = append(f.handlers, handler{prefix: prefix, stdout: stdout, err: err})
}

func (f *fakeRunner) onStderr(prefix, stderr string, err error) {
	f.handlers = append(f.handlers, handler{prefix: prefix, stderr: stderr, err: err})
}

func (f *fakeRunner) onFunc(prefix string, fn func() (string, error)) {
	f.handlers = append(f.handlers, handler{prefix: prefix, fn: fn})
}

func (f *fakeRunner) Run(name string, args []string, stdout, stderr io.Writer) error {
	return f.RunEnv(name, args, nil, stdout, stderr)
}

// RunEnv は Run と同じ応答を返し、追加の環境も記録する（docker.EnvRunner）。
func (f *fakeRunner) RunEnv(name string, args, extraEnv []string, stdout, stderr io.Writer) error {
	c := call{name: name, args: append([]string(nil), args...), env: append([]string(nil), extraEnv...)}
	f.calls = append(f.calls, c)
	s := c.String()
	for i := len(f.handlers) - 1; i >= 0; i-- {
		h := f.handlers[i]
		if strings.HasPrefix(s, h.prefix) {
			out, err := h.stdout, h.err
			if h.fn != nil {
				out, err = h.fn()
			}
			if stdout != nil {
				io.WriteString(stdout, out)
			}
			if stderr != nil && h.stderr != "" {
				io.WriteString(stderr, h.stderr)
			}
			return err
		}
	}
	return nil
}

func (f *fakeRunner) find(prefix string) []call {
	var out []call
	for _, c := range f.calls {
		if strings.HasPrefix(c.String(), prefix) {
			out = append(out, c)
		}
	}
	return out
}

// securityResp は、偽の security が返す応答。
type securityResp struct {
	stdout, stderr string
	err            error
}

// fakeSecurityCmd は Deps.Security の偽物。呼び出しを記録し、引数ごとに登録した応答を返す。
// 本物の /usr/bin/security は決して動かさない。未登録の呼び出しはエラーにして、
// 思いがけず security に届いたテストが黙って通らないようにする。
type fakeSecurityCmd struct {
	calls [][]string
	resps map[string]securityResp
}

// on は、引数（空白でつないだもの）に対する応答を登録する。
func (f *fakeSecurityCmd) on(args []string, stdout, stderr string, err error) {
	if f.resps == nil {
		f.resps = map[string]securityResp{}
	}
	f.resps[strings.Join(args, " ")] = securityResp{stdout, stderr, err}
}

func (f *fakeSecurityCmd) run(args []string) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	r, ok := f.resps[strings.Join(args, " ")]
	if !ok {
		return nil, nil, errors.New("偽の security に未登録の呼び出し: " + strings.Join(args, " "))
	}
	return []byte(r.stdout), []byte(r.stderr), r.err
}

type execCall struct {
	argv0 string
	argv  []string
	env   []string
}

// testEnv は、ホームとプロジェクトを一時フォルダに作った、偽物だらけの環境。
type testEnv struct {
	t      *testing.T
	d      Deps
	runner *fakeRunner
	sec    *fakeSecurityCmd
	execs  []execCall
	vars   map[string]string
	stdout bytes.Buffer
	stderr bytes.Buffer

	home, proj, cwd string

	tty       string // OpenTTY から読ませる内容
	ttyErr    error
	ttyOpened int
	opPath    string // 空なら op は PATH にない
	dockerErr error  // LookPath("docker") のエラー
	isTTY     bool

	// 演出（internal/ui）
	stderrTTY    bool
	stdoutTTY    bool
	cols, rows   int
	stderrAtExec []string // Exec が呼ばれた時点の標準エラー（端末を戻してから置き換えたかを見る）

	order []string // 承認と Exec の呼び出し順
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	base := realTemp(t)
	e := &testEnv{
		t:      t,
		runner: &fakeRunner{},
		sec:    &fakeSecurityCmd{},
		home:   filepath.Join(base, "home"),
		proj:   filepath.Join(base, "home", "src", "proj"),
	}
	e.cwd = e.proj
	mustMkdir(t, filepath.Join(e.proj, ".git"))
	e.vars = map[string]string{
		"HOME":            e.home,
		"XDG_CONFIG_HOME": filepath.Join(e.home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(e.home, ".local", "state"),
	}
	// 既定: git はプロジェクトの最上位を返し、コンテナはなく、イメージはある
	e.runner.on("git -C", e.proj+"\n", nil)
	e.runner.on("docker ps -a --filter name=", "", nil)
	e.runner.on("docker image inspect", "", nil)

	e.d = Deps{
		Runner:   e.runner,
		Security: e.sec.run,
		Cmd:      noCmd{},
		Pager:    func(string) error { return errors.New("Deps.Pager は使わないはず") },
		Exec: func(argv0 string, argv, env []string) error {
			e.order = append(e.order, "exec")
			e.stderrAtExec = append(e.stderrAtExec, e.stderr.String())
			e.execs = append(e.execs, execCall{argv0, argv, env})
			// 本物なら起動し直した側が消す一時フォルダを、テストの終わりに消す
			for _, kv := range env {
				if dir, ok := strings.CutPrefix(kv, "SIGIL_OP_TMP="); ok {
					t.Cleanup(func() { os.RemoveAll(dir) })
				}
			}
			return nil
		},
		LookPath: func(name string) (string, error) {
			switch {
			case name == "docker" && e.dockerErr == nil:
				return "/usr/local/bin/docker", nil
			case name == "op" && e.opPath != "":
				return e.opPath, nil
			}
			return "", errors.New("not found")
		},
		LookupEnv: func(k string) (string, bool) { v, ok := e.vars[k]; return v, ok },
		Environ: func() []string {
			var out []string
			for k, v := range e.vars {
				out = append(out, k+"="+v)
			}
			return out
		},
		Getwd:       func() (string, error) { return e.cwd, nil },
		UserHomeDir: func() (string, error) { return e.home, nil },
		OpenTTY: func() (io.ReadCloser, error) {
			e.ttyOpened++
			e.order = append(e.order, "tty")
			if e.ttyErr != nil {
				return nil, e.ttyErr
			}
			return io.NopCloser(strings.NewReader(e.tty)), nil
		},
		IsTerminal:       func() bool { return e.isTTY },
		StderrIsTerminal: func() bool { return e.stderrTTY },
		StdoutIsTerminal: func() bool { return e.stdoutTTY },
		TermSize:         func() (int, int) { return e.cols, e.rows },
		StdoutTermSize:   func() (int, int) { return e.cols, e.rows },
		// 演出の待ちは、待たずにすぐ進める
		After: func(time.Duration) <-chan time.Time {
			c := make(chan time.Time, 1)
			c <- time.Time{}
			return c
		},
		Now:               func() time.Time { return time.Date(2026, 11, 25, 1, 2, 3, 0, time.UTC) },
		Executable:        func() (string, error) { return "/abs/dev", nil },
		Getuid:            func() int { return 501 },
		Getgid:            func() int { return 20 },
		WriteBuildContext: func(dir string) error { return os.WriteFile(filepath.Join(dir, "Dockerfile"), nil, 0o644) },
		Stdout:            &e.stdout,
		Stderr:            &e.stderr,
	}
	return e
}

func (e *testEnv) run(args ...string) int {
	return Run(args, e.d)
}

// container は、既存のコンテナの状態（"running" / "exited"）と layout を偽の docker に設定する。
func (e *testEnv) container(state, layout string) {
	e.runner.on("docker ps -a --filter name=", e.name()+"\t"+state+"\t"+layout+"\n", nil)
	// 停止中のコンテナを起動する前に読むラベル（ro はなし）
	e.runner.on("docker container inspect", `{"sigil.layout":"`+layout+`"}`+"\n", nil)
}

func (e *testEnv) name() string { return project.New(e.proj).ContainerName }
func (e *testEnv) key() string  { return project.New(e.proj).Key }

func (e *testEnv) write(rel, content string) string {
	p := filepath.Join(e.proj, rel)
	if filepath.IsAbs(rel) {
		p = rel
	}
	mustMkdir(e.t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
