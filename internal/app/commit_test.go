package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FrontWorksDev/sigil/internal/gitcommit"
	"github.com/FrontWorksDev/sigil/internal/project"
)

// ---- sigil commit（issue #13）----

// gitIsolation は、利用者の全体設定を読まないための環境変数。
var gitIsolation = []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}

type cmdCall struct {
	name  string
	args  []string
	stdin string
}

func (c cmdCall) String() string { return c.name + " " + strings.Join(c.args, " ") }

// hookFunc は、コンテナでのフックの実行の偽物。extra は位置引数のうちフックのパスより後ろ。
type hookFunc func(stdin string, extra []string, stdout, stderr io.Writer) error

// cmdFake は Deps.Cmd の偽物。git は本物を（全体設定から切り離して）実行し、
// docker は記録して、フックの名前ごとに決めた応答を返す。
type cmdFake struct {
	calls                 []cmdCall
	hooks                 map[string]hookFunc
	deltaEnvEmptyFeatures bool                                       // delta の Env に DELTA_FEATURES=（空）があったか
	delta                 func(stdin string, stdout io.Writer) error // delta の偽物（nil なら呼ばれたらエラー）
}

func (f *cmdFake) Run(name string, args []string, o gitcommit.RunOpts) error {
	c := cmdCall{name: name, args: append([]string(nil), args...)}
	if name == "git" {
		f.calls = append(f.calls, c)
		o.Env = append(append([]string(nil), gitIsolation...), o.Env...)
		return gitcommit.ExecRunner{}.Run(name, args, o)
	}
	if o.Stdin != nil {
		b, _ := io.ReadAll(o.Stdin)
		c.stdin = string(b)
	}
	f.calls = append(f.calls, c)
	if path.Base(name) == "delta" {
		if f.delta == nil {
			return errors.New("delta の応答が決まっていない")
		}
		for _, kv := range o.Env {
			if kv == "DELTA_FEATURES=" {
				f.deltaEnvEmptyFeatures = true
			}
		}
		return f.delta(c.stdin, o.Stdout)
	}
	// exec -i -w <path> <name> sh -c <script> sh <mode> <hook> <extra...>
	if name != "docker" || len(args) < 11 || args[0] != "exec" {
		return fmt.Errorf("想定外の呼び出し: %s", c)
	}
	mode, hook := args[9], path.Base(args[10])
	if h := f.hooks[hook]; h != nil {
		return h(c.stdin, args[11:], o.Stdout, o.Stderr)
	}
	if mode == "msg" {
		// 何もしないフック: 銘をそのまま返す
		io.WriteString(o.Stdout, c.stdin)
	}
	return nil
}

func (f *cmdFake) find(name, sub string) []cmdCall {
	var out []cmdCall
	for _, c := range f.calls {
		if c.name == name && strings.Contains(c.String(), sub) {
			out = append(out, c)
		}
	}
	return out
}

// ttyReader は、最初の Read の前に before を呼ぶ（画面を出したあと、答える前の出来事を作るため）。
type ttyReader struct {
	r      io.Reader
	before func()
	done   bool
}

func (t *ttyReader) Read(p []byte) (int, error) {
	if !t.done {
		t.done = true
		if t.before != nil {
			t.before()
		}
	}
	return t.r.Read(p)
}

type commitEnv struct {
	*testEnv
	cmd          *cmdFake
	g            gitcommit.Git
	pages        []string
	beforeAnswer func()
}

// newCommitEnv は、newEnv のプロジェクトに本物の git リポジトリを作り、a.txt を 1 回コミットする。
// README の勧めどおり、.sigil-commit を .gitignore に入れた形にする（現実の使い方）。
func newCommitEnv(t *testing.T) *commitEnv {
	t.Helper()
	return newCommitEnvWith(t, true)
}

// newCommitEnvWith は ignore が false なら .gitignore を置かない（入れ忘れた利用者の形）。
func newCommitEnvWith(t *testing.T, ignore bool) *commitEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git がありません")
	}
	e := newEnv(t)
	e.isTTY = true
	c := &commitEnv{testEnv: e, cmd: &cmdFake{hooks: map[string]hookFunc{}}}
	c.g = gitcommit.Git{R: gitcommit.ExecRunner{}, Root: e.proj, Env: gitIsolation}
	e.d.Cmd = c.cmd
	e.d.Pager = func(s string) error { c.pages = append(c.pages, s); return nil }
	e.d.OpenTTY = func() (io.ReadCloser, error) {
		e.ttyOpened++
		if e.ttyErr != nil {
			return nil, e.ttyErr
		}
		return io.NopCloser(&ttyReader{r: strings.NewReader(e.tty), before: c.beforeAnswer}), nil
	}
	c.git("init", "-q", "-b", "main")
	c.git("config", "user.name", "t")
	c.git("config", "user.email", "t@example.com")
	c.git("config", "commit.gpgSign", "false")
	e.write("a.txt", "one\n")
	if ignore {
		e.write(".gitignore", gitcommit.MessageFile+"\n")
	}
	c.commitAll("init")
	return c
}

func (c *commitEnv) git(args ...string) string {
	c.t.Helper()
	out, err := c.g.Out(nil, args...)
	if err != nil {
		c.t.Fatal(err)
	}
	return out
}

func (c *commitEnv) commitAll(msg string) {
	c.t.Helper()
	c.git("add", "-A")
	c.git("commit", "-q", "-m", msg)
}

func (c *commitEnv) head() string { return strings.TrimSpace(c.git("rev-parse", "HEAD")) }

// body は HEAD の銘（commit オブジェクトの本文そのもの）。
func (c *commitEnv) body() string {
	_, b, _ := strings.Cut(c.git("cat-file", "commit", "HEAD"), "\n\n")
	return b
}

func (c *commitEnv) msgFile() string { return filepath.Join(c.proj, gitcommit.MessageFile) }

func (c *commitEnv) noCommitTree() {
	c.t.Helper()
	if n := len(c.cmd.find("git", "commit-tree")); n != 0 {
		c.t.Errorf("commit-tree を %d 回呼んだ", n)
	}
}

// hook は、実行できるフックのファイルを置く（中身はホストで実行されたら印を作るスクリプト）。
func (c *commitEnv) hook(rel string) {
	c.t.Helper()
	p := c.write(rel, "#!/bin/sh\ntouch \"$PWD/HOOK_RAN_ON_HOST\"\n")
	if err := os.Chmod(p, 0o755); err != nil {
		c.t.Fatal(err)
	}
}

func (c *commitEnv) dockerExecs() []cmdCall { return c.cmd.find("docker", "exec ") }

func TestCommitHappyPath(t *testing.T) {
	c := newCommitEnv(t)
	c.write("b.txt", "new\n")
	c.write(gitcommit.MessageFile, "feat: add b\n\nbody line\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	t.Logf("画面:\n%s", c.stderr.String())
	if got := c.body(); got != "feat: add b\n\nbody line\n" {
		t.Errorf("銘 = %q", got)
	}
	if got := c.git("show", "HEAD:b.txt"); got != "new\n" {
		t.Errorf("HEAD:b.txt = %q", got)
	}
	if _, err := os.Stat(c.msgFile()); !os.IsNotExist(err) {
		t.Error(".sigil-commit が残っている")
	}
	if c.ttyOpened != 1 {
		t.Errorf("tty を %d 回開いた", c.ttyOpened)
	}
	se := c.stderr.String()
	for _, want := range []string{"刻印の儀", "b.txt", "刻印しました", "git push -u", "ホストでは hooks を実行していません"} {
		if !strings.Contains(se, want) {
			t.Errorf("標準エラーに %q がない", want)
		}
	}
	if strings.Contains(se, "外界へ解き放つ（push する）") {
		t.Error("upstream がないのに push を聞いた")
	}
	if len(c.dockerExecs()) != 0 || len(c.runner.find("docker")) != 0 {
		t.Error("フックがないのに docker を呼んだ")
	}
	if len(c.pages) != 0 {
		t.Error("ページャーを呼んだ")
	}
}

func TestCommitNo(t *testing.T) {
	c := newCommitEnv(t)
	before := c.head()
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "n\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if c.head() != before {
		t.Error("コミットされた")
	}
	if _, err := os.Stat(c.msgFile()); err != nil {
		t.Error(".sigil-commit が消えた")
	}
	if !strings.Contains(c.stderr.String(), "中止しました") {
		t.Errorf("stderr = %s", c.stderr.String())
	}
	c.noCommitTree()
}

func TestCommitNotTerminal(t *testing.T) {
	c := newCommitEnv(t)
	c.isTTY = false
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	c.noCommitTree()
	if c.ttyOpened != 0 {
		t.Error("tty を開いた")
	}
}

func TestCommitWorksWithoutDockerWhenNoHooks(t *testing.T) {
	c := newCommitEnv(t)
	c.dockerErr = errors.New("not found")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
}

func TestCommitNoMessage(t *testing.T) {
	c := newCommitEnv(t)
	c.write("a.txt", "two\n")
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	se := c.stderr.String()
	if !strings.Contains(se, "sigil: 銘がありません\n") || !strings.Contains(se, ".sigil-commit") {
		t.Errorf("stderr = %s", se)
	}
}

func TestCommitNothing(t *testing.T) {
	c := newCommitEnv(t)
	c.write(gitcommit.MessageFile, "fix: a\n")
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(c.stderr.String(), "sigil: 刻むものがありません\n") {
		t.Errorf("stderr = %s", c.stderr.String())
	}
	if c.ttyOpened != 0 {
		t.Error("tty を開いた")
	}
}

func TestCommitWarnsTrackedMessageFile(t *testing.T) {
	c := newCommitEnv(t)
	c.write(gitcommit.MessageFile, "old\n")
	// .gitignore に入っていても、先に追跡されていた形
	c.git("add", "-f", "--", gitcommit.MessageFile)
	c.commitAll("track message file")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.write("a.txt", "two\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if se := c.stderr.String(); !strings.Contains(se, ".gitignore") || !strings.Contains(se, "削除") {
		t.Errorf("追跡されている注意がない: %s", c.stderr.String())
	}
	if got := c.git("show", "HEAD:"+gitcommit.MessageFile); got != "old\n" {
		t.Errorf("銘のファイルがコミットに入った: %q", got)
	}
}

func TestCommitWorktreeChangedDuringPrompt(t *testing.T) {
	c := newCommitEnv(t)
	c.write("a.txt", "shown\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.beforeAnswer = func() { c.write("a.txt", "tampered\n") }
	c.tty = "y\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if got := c.git("show", "HEAD:a.txt"); got != "shown\n" {
		t.Errorf("HEAD:a.txt = %q", got)
	}
}

func TestCommitDiffThenYes(t *testing.T) {
	c := newCommitEnv(t)
	c.write("a.txt", "one\nx\x1b[31mred\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "d\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if len(c.pages) != 1 {
		t.Fatalf("ページャーの呼び出し = %d", len(c.pages))
	}
	if strings.Contains(c.pages[0], "\x1b") {
		t.Errorf("生の ESC が残っている: %q", c.pages[0])
	}
	if !strings.Contains(c.pages[0], "x^[[31mred") {
		t.Errorf("差分 = %q", c.pages[0])
	}
	if strings.Count(c.stderr.String(), "刻印の儀") != 2 {
		t.Error("差分のあと、画面を出し直していない")
	}
	if got := c.git("show", "HEAD:a.txt"); got != "one\nx\x1b[31mred\n" {
		t.Errorf("HEAD:a.txt = %q", got)
	}
}

// withDelta は、ホストに delta がある状態にする（LookPath が絶対パスを返す）。
func (c *commitEnv) withDelta() {
	lp := c.d.LookPath
	c.d.LookPath = func(name string) (string, error) {
		if name == "delta" {
			return "/opt/test/bin/delta", nil
		}
		return lp(name)
	}
}

func TestCommitDiffUsesDelta(t *testing.T) {
	c := newCommitEnv(t)
	c.fancy()
	c.withDelta()
	c.cmd.delta = func(stdin string, stdout io.Writer) error {
		io.WriteString(stdout, "\x1b[32mDELTA OUTPUT\x1b[0m\n")
		return nil
	}
	c.write("a.txt", "one\nx\x1b]8;;file:///evil.command\x07red\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "d\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	calls := c.cmd.find("/opt/test/bin/delta", "")
	if len(calls) != 1 {
		t.Fatalf("delta の呼び出し = %d", len(calls))
	}
	if got, want := strings.Join(calls[0].args, " "), "--no-gitconfig --paging=never --line-numbers"; got != want {
		t.Errorf("delta の引数 = %q, want %q", got, want)
	}
	if !c.cmd.deltaEnvEmptyFeatures {
		t.Error("delta の Env に DELTA_FEATURES= がない")
	}
	if strings.Contains(calls[0].stdin, "\x1b") || strings.Contains(calls[0].stdin, "\x07") {
		t.Errorf("delta に生の制御文字を渡した: %q", calls[0].stdin)
	}
	if !strings.Contains(calls[0].stdin, "x^[]8;;file:///evil.command^Gred") {
		t.Errorf("delta への入力 = %q", calls[0].stdin)
	}
	if len(c.pages) != 1 || c.pages[0] != "\x1b[32mDELTA OUTPUT\x1b[0m\n" {
		t.Errorf("ページャーへ渡したもの = %q", c.pages)
	}
}

func TestCommitDiffWithoutDeltaUsesBuiltin(t *testing.T) {
	c := newCommitEnv(t)
	c.fancy() // delta はない（LookPath が見つけない）
	c.write("a.txt", "one\nx\x1b[31mred\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "d\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if len(c.pages) != 1 || !strings.Contains(c.pages[0], "x^[[31mred") || !strings.Contains(c.pages[0], "\x1b[38;5;") {
		t.Errorf("組み込みの表示になっていない: %q", c.pages)
	}
}

func TestCommitDiffDeltaFailsFallsBack(t *testing.T) {
	c := newCommitEnv(t)
	c.fancy()
	c.withDelta()
	c.cmd.delta = func(string, io.Writer) error { return errors.New("exit status 2\x1b[31m") }
	c.write("a.txt", "one\nx\x1b[31mred\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "d\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if len(c.pages) != 1 || !strings.Contains(c.pages[0], "x^[[31mred") {
		t.Errorf("組み込みの表示に戻っていない: %q", c.pages)
	}
	se := c.stderr.String()
	if !strings.Contains(se, "sigil: 注意: delta") || !strings.Contains(se, "exit status 2^[[31m") {
		t.Errorf("注意の行がない: %q", se)
	}
}

func TestCommitDiffPlainModesSkipDelta(t *testing.T) {
	for _, mode := range []string{"plain", "mono"} {
		t.Run(mode, func(t *testing.T) {
			c := newCommitEnv(t)
			c.withDelta() // noCmd 相当: delta を呼んだら cmdFake.delta が nil でエラーになる
			switch mode {
			case "plain":
				c.vars["SIGIL_PLAIN"] = "1"
				c.fancy()
			case "mono":
				c.vars["NO_COLOR"] = "1"
				c.fancy()
			}
			c.write("a.txt", "one\nx\n")
			c.write(gitcommit.MessageFile, "fix: a\n")
			c.tty = "d\ny\n"
			if code := c.run("commit"); code != 0 {
				t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
			}
			if n := len(c.cmd.find("/opt/test/bin/delta", "")); n != 0 {
				t.Errorf("delta を %d 回呼んだ", n)
			}
			if len(c.pages) != 1 || strings.Contains(c.pages[0], "\x1b") {
				t.Errorf("組み込みの表示になっていない: %q", c.pages)
			}
		})
	}
}

func TestCommitHooksInContainer(t *testing.T) {
	c := newCommitEnv(t)
	c.hook(".husky/_/pre-commit")
	c.hook(".husky/_/commit-msg")
	c.git("config", "core.hooksPath", ".husky/_")
	c.commitAll("add hooks")
	c.container("running", "4")
	c.cmd.hooks["commit-msg"] = func(stdin string, extra []string, stdout, stderr io.Writer) error {
		if stdin != "fix: a\n" {
			t.Errorf("commit-msg の標準入力 = %q", stdin)
		}
		io.WriteString(stdout, "feat: changed\n")
		return nil
	}
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	p := project.New(c.proj)
	prefix := "docker exec -i -w " + p.ContainerPath + " " + p.ContainerName + " sh -c "
	execs := c.dockerExecs()
	if len(execs) != 2 {
		t.Fatalf("docker exec = %v", execs)
	}
	for i, want := range []string{"pre-commit", "commit-msg"} {
		if !strings.HasPrefix(execs[i].String(), prefix) {
			t.Errorf("呼び出し %d = %s", i, execs[i])
		}
		if got := path.Base(execs[i].args[10]); got != want {
			t.Errorf("フック %d = %s, want %s", i, got, want)
		}
	}
	if got := execs[0].args[11:]; strings.Join(got, " ") != ":(top)" {
		t.Errorf("pre-commit の pathspec = %v", got)
	}
	if got := c.body(); got != "feat: changed\n" {
		t.Errorf("銘 = %q", got)
	}
	if _, err := os.Stat(filepath.Join(c.proj, "HOOK_RAN_ON_HOST")); err == nil {
		t.Error("フックがホストで実行された")
	}
	if !strings.Contains(c.stderr.String(), "✔ commit-msg") {
		t.Errorf("試練の結果がない: %s", c.stderr.String())
	}
}

func TestCommitHookFails(t *testing.T) {
	c := newCommitEnv(t)
	c.hook(".git/hooks/pre-commit")
	c.container("running", "4")
	c.cmd.hooks["pre-commit"] = func(_ string, _ []string, _, stderr io.Writer) error {
		io.WriteString(stderr, "lint\x1b[31m")
		return errors.New("exit status 1")
	}
	before := c.head()
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	se := c.stderr.String()
	if !strings.Contains(se, "lint^[[31m") || strings.Contains(se, "\x1b") {
		t.Errorf("stderr = %q", se)
	}
	if !strings.Contains(se, "試練に敗れました") {
		t.Errorf("stderr = %s", se)
	}
	if c.head() != before {
		t.Error("コミットされた")
	}
	c.noCommitTree()
	if c.ttyOpened != 0 {
		t.Error("tty を開いた")
	}
}

func TestCommitHooksNeedContainer(t *testing.T) {
	c := newCommitEnv(t)
	c.hook(".git/hooks/pre-commit")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(c.stderr.String(), "領域が開いていません") {
		t.Errorf("stderr = %s", c.stderr.String())
	}
	c.noCommitTree()
	if len(c.dockerExecs()) != 0 {
		t.Error("docker exec を呼んだ")
	}
}

func TestCommitHooksNeedDocker(t *testing.T) {
	c := newCommitEnv(t)
	c.dockerErr = errors.New("not found")
	c.hook(".git/hooks/pre-commit")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(c.stderr.String(), "docker が見つかりません") {
		t.Errorf("stderr = %s", c.stderr.String())
	}
	c.noCommitTree()
}

func TestCommitRefusals(t *testing.T) {
	cases := map[string]func(c *commitEnv){
		"include.path": func(c *commitEnv) { c.git("config", "include.path", "../inc.cfg") },
		"MERGE_HEAD":   func(c *commitEnv) { c.write(".git/MERGE_HEAD", c.head()+"\n") },
		"gitlink": func(c *commitEnv) {
			sub := gitcommit.Git{R: gitcommit.ExecRunner{}, Root: filepath.Join(c.proj, "sub"), Env: gitIsolation}
			c.write("sub/x.txt", "x\n")
			for _, a := range [][]string{{"init", "-q"}, {"add", "-A"},
				{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "x"}} {
				if _, err := sub.Out(nil, a...); err != nil {
					c.t.Fatal(err)
				}
			}
		},
		"LFS": func(c *commitEnv) {
			c.write(".gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
			c.write("a.bin", "data\n")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			c := newCommitEnv(t)
			c.write("a.txt", "two\n")
			c.write(gitcommit.MessageFile, "fix: a\n")
			setup(c)
			c.tty = "y\n"
			if code := c.run("commit"); code != 1 {
				t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
			}
			if c.ttyOpened != 0 {
				t.Error("tty を開いた")
			}
			c.noCommitTree()
			if !strings.HasPrefix(c.stderr.String(), "sigil: 刻印できません\n") {
				t.Errorf("stderr = %s", c.stderr.String())
			}
		})
	}
}

// withUpstream は、bare リポジトリを origin にして main を push し、upstream を張る。
func (c *commitEnv) withUpstream() string {
	c.t.Helper()
	bare := filepath.Join(realTemp(c.t), "remote.git")
	if _, err := (gitcommit.Git{R: gitcommit.ExecRunner{}, Root: c.proj, Env: gitIsolation}).Out(nil, "init", "-q", "--bare", bare); err != nil {
		c.t.Fatal(err)
	}
	c.git("remote", "add", "origin", bare)
	c.git("push", "-q", "-u", "origin", "main")
	return bare
}

func bareHead(t *testing.T, bare string) string {
	t.Helper()
	g := gitcommit.Git{R: gitcommit.ExecRunner{}, Root: bare, Env: gitIsolation}
	out, err := g.Out(nil, "rev-parse", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func TestCommitPushFlow(t *testing.T) {
	c := newCommitEnv(t)
	bare := c.withUpstream()
	old := c.head()
	c.hook(".git/hooks/pre-push")
	c.container("running", "4")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	head := c.head()
	if head == old {
		t.Fatal("コミットされていない")
	}
	if got := bareHead(t, bare); got != head {
		t.Errorf("bare の main = %s, want %s", got, head)
	}
	execs := c.dockerExecs()
	if len(execs) != 1 || path.Base(execs[0].args[10]) != "pre-push" {
		t.Fatalf("docker exec = %v", execs)
	}
	if want := "refs/heads/main " + head + " refs/heads/main " + old + "\n"; execs[0].stdin != want {
		t.Errorf("pre-push の標準入力 = %q, want %q", execs[0].stdin, want)
	}
	if got := execs[0].args[11:]; len(got) != 2 || got[0] != "origin" || got[1] != bare {
		t.Errorf("pre-push の引数 = %v", got)
	}
	se := c.stderr.String()
	for _, want := range []string{"push 先の確認", "origin", "1 件"} {
		if !strings.Contains(se, want) {
			t.Errorf("標準エラーに %q がない: %s", want, se)
		}
	}
}

func TestCommitPushHookFails(t *testing.T) {
	c := newCommitEnv(t)
	bare := c.withUpstream()
	old := c.head()
	c.hook(".git/hooks/pre-push")
	c.container("running", "4")
	c.cmd.hooks["pre-push"] = func(_ string, _ []string, _, stderr io.Writer) error {
		io.WriteString(stderr, "tests failed")
		return errors.New("exit status 1")
	}
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\ny\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if got := bareHead(t, bare); got != old {
		t.Error("フックが失敗したのに push した")
	}
	if c.head() == old {
		t.Error("コミットが残っていない")
	}
	if se := c.stderr.String(); !strings.Contains(se, "tests failed") || !strings.Contains(se, "コミットは残っています") {
		t.Errorf("stderr = %s", se)
	}
}

func TestCommitPushDeclined(t *testing.T) {
	c := newCommitEnv(t)
	bare := c.withUpstream()
	old := c.head()
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\nn\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if c.head() == old {
		t.Error("コミットが残っていない")
	}
	if got := bareHead(t, bare); got != old {
		t.Error("断ったのに push した")
	}
}

func TestCommitArgs(t *testing.T) {
	c := newCommitEnv(t)
	c.write("a.txt", "two\n")
	c.write("b.txt", "b\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	if code := c.run("commit", "foo"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(c.stderr.String(), "使い方: sigil commit [-- <path>...]") {
		t.Errorf("stderr = %s", c.stderr.String())
	}
	c.noCommitTree()

	c.tty = "y\n"
	if code := c.run("commit", "--", "a.txt"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if got := c.git("show", "HEAD:a.txt"); got != "two\n" {
		t.Errorf("HEAD:a.txt = %q", got)
	}
	if _, err := c.g.Out(nil, "cat-file", "-e", "HEAD:b.txt"); err == nil {
		t.Error("指定していない b.txt がコミットされた")
	}
}

// Seal が「コミットはできたが、本物の index をそろえられなかった」を返したとき。
// index.lock を置いて、最後の reset だけを失敗させる。
func TestCommitSealedButIndexNotAligned(t *testing.T) {
	c := newCommitEnv(t)
	old := c.head()
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.beforeAnswer = func() { c.write(".git/index.lock", "") }
	c.tty = "y\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if c.head() == old {
		t.Fatal("コミットされていない")
	}
	se := c.stderr.String()
	for _, want := range []string{"刻印しました", "index", "git push -u"} {
		if !strings.Contains(se, want) {
			t.Errorf("標準エラーに %q がない: %s", want, se)
		}
	}
	if strings.Contains(se, "もう一度実行") {
		t.Errorf("やり直しを勧めている: %s", se)
	}
	if _, err := os.Stat(c.msgFile()); !os.IsNotExist(err) {
		t.Error(".sigil-commit が残っている")
	}
}

// 演出あり（魔法陣を描く）でも、フックの実行と刻印が同じように進むこと。
func TestCommitHooksFancy(t *testing.T) {
	for _, fail := range []bool{false, true} {
		c := newCommitEnv(t)
		c.fancy()
		c.hook(".git/hooks/pre-commit")
		c.hook(".git/hooks/commit-msg")
		c.container("running", "4")
		if fail {
			c.cmd.hooks["commit-msg"] = func(_ string, _ []string, _, stderr io.Writer) error {
				io.WriteString(stderr, "bad\x1b[2Jmsg")
				return errors.New("exit status 1")
			}
		}
		c.write("a.txt", "two\n")
		c.write(gitcommit.MessageFile, "fix: a\n")
		c.tty = "y\n"
		code := c.run("commit")
		se := c.stderr.String()
		if fail {
			if code != 1 || !strings.Contains(se, "bad^[[2Jmsg") || strings.Contains(se, "\x1b[2J") {
				t.Errorf("失敗: code = %d, stderr = %q", code, tail(se))
			}
			continue
		}
		if code != 0 {
			t.Fatalf("code = %d, stderr = %s", code, tail(se))
		}
		if got := c.body(); got != "fix: a\n" {
			t.Errorf("銘 = %q", got)
		}
		if !strings.Contains(se, "刻印しました") {
			t.Errorf("stderr = %q", tail(se))
		}
	}
}

// -- の後ろのパスは、git と同じく今いるフォルダから。
func TestCommitPathsRelativeToCwd(t *testing.T) {
	c := newCommitEnv(t)
	c.write("sub/a.txt", "sub\n")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: sub\n")
	c.cwd = filepath.Join(c.proj, "sub")
	c.tty = "y\n"
	if code := c.run("commit", "--", "a.txt"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if got := c.git("show", "HEAD:sub/a.txt"); got != "sub\n" {
		t.Errorf("HEAD:sub/a.txt = %q", got)
	}
	if got := c.git("show", "HEAD:a.txt"); got != "one\n" {
		t.Errorf("最上位の a.txt がコミットされた: %q", got)
	}
	if !strings.Contains(c.stderr.String(), "sub/a.txt") {
		t.Errorf("画面の対象に sub/a.txt がない: %s", c.stderr.String())
	}
}

func TestCommitPathArgsRejected(t *testing.T) {
	for _, arg := range []string{"../../x", ":(top)x", ":!a.txt"} {
		t.Run(arg, func(t *testing.T) {
			c := newCommitEnv(t)
			mustMkdir(t, filepath.Join(c.proj, "sub"))
			c.write("a.txt", "two\n")
			c.write(gitcommit.MessageFile, "fix: a\n")
			c.cwd = filepath.Join(c.proj, "sub")
			c.tty = "y\n"
			if code := c.run("commit", "--", arg); code != 1 {
				t.Fatalf("code = %d", code)
			}
			if !strings.HasPrefix(c.stderr.String(), "sigil: 刻むものを指定できません\n") {
				t.Errorf("stderr = %s", c.stderr.String())
			}
			c.noCommitTree()
			if c.ttyOpened != 0 {
				t.Error("tty を開いた")
			}
		})
	}
}

// push の URL に入った認証情報（全体設定の insteadOf などで入りうる）を、コンテナにも画面にも出さない。
func TestCommitPushURLRedacted(t *testing.T) {
	c := newCommitEnv(t)
	c.git("remote", "add", "origin", "https://user:SECRET@example.invalid/o/r.git")
	c.git("config", "branch.main.remote", "origin")
	c.git("config", "branch.main.merge", "refs/heads/main")
	c.hook(".git/hooks/pre-push")
	c.container("running", "4")
	// pre-push を失敗させ、本物の push には進ませない
	c.cmd.hooks["pre-push"] = func(_ string, _ []string, _, _ io.Writer) error { return errors.New("exit status 1") }
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\ny\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	execs := c.dockerExecs()
	if len(execs) != 1 {
		t.Fatalf("docker exec = %v", execs)
	}
	if strings.Contains(execs[0].String(), "SECRET") || strings.Contains(execs[0].String(), "user") {
		t.Errorf("認証情報がフックの引数に出た: %v", execs[0].args[11:])
	}
	if got := execs[0].args[len(execs[0].args)-1]; got != "https://example.invalid/o/r.git" {
		t.Errorf("URL = %q", got)
	}
	if se := c.stderr.String(); strings.Contains(se, "SECRET") || !strings.Contains(se, "https://example.invalid/o/r.git") {
		t.Errorf("stderr = %s", se)
	}
}

// 確認のあいだに別の場所でコミットされたら、刻印しない。説明は 1 つにまとめる。
func TestCommitBranchMovedDuringPrompt(t *testing.T) {
	c := newCommitEnv(t)
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.beforeAnswer = func() { c.git("commit", "-q", "--allow-empty", "-m", "elsewhere") }
	c.tty = "y\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	se := c.stderr.String()
	if !strings.Contains(se, "sigil: 刻印は果たされませんでした\n") || strings.Contains(se, "））") {
		t.Errorf("stderr = %s", se)
	}
	if strings.Count(se, "コミットしていません")+strings.Count(se, "刻印されたようです") != 1 {
		t.Errorf("説明が重なっている: %s", se)
	}
	if _, err := os.Stat(c.msgFile()); err != nil {
		t.Error(".sigil-commit が消えた")
	}
}

// HookExecArgs が拒否したときも、フックの場所は Escape して出す。
func TestHookRunnerOutsideRootEscaped(t *testing.T) {
	root := realTemp(t)
	h := hookRunner{p: project.New(root), r: gitcommit.Repo{Root: root},
		hooks: gitcommit.Hooks{Dir: "/elsewhere/\x1b[31mhooks"}}
	_, err := h.run(io.Discard, gitcommit.PreCommit, nil, "", "コミットしていません")
	var le *loreError
	if !errors.As(err, &le) {
		t.Fatalf("lore ではない: %v", err)
	}
	if strings.Contains(err.Error(), "\x1b") || !strings.Contains(err.Error(), "^[[31m") {
		t.Errorf("err = %q", err)
	}
}

// HEAD にサブモジュールがあると、git add がその中で git を動かし、コンテナが書ける入れ子の設定の
// filter をホストで実行してしまう。tty を開く前に拒否し、filter は一度も動かない。
func TestCommitRefusesExistingSubmodule(t *testing.T) {
	c := newCommitEnv(t)
	sub := gitcommit.Git{R: gitcommit.ExecRunner{}, Root: filepath.Join(c.proj, "sub"), Env: gitIsolation}
	c.write("sub/f.txt", "s\n")
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "s"}} {
		if _, err := sub.Out(nil, a...); err != nil {
			t.Fatal(err)
		}
	}
	c.git("add", "--", "sub")
	c.git("commit", "-q", "-m", "add submodule")

	// コンテナがしうる細工: 入れ子の設定に filter を置き、サブモジュールの中を変える
	marker := filepath.Join(realTemp(t), "PWNED")
	if _, err := sub.Out(nil, "config", "filter.evil.clean", "sh -c 'touch "+marker+"; cat'"); err != nil {
		t.Fatal(err)
	}
	c.write("sub/.gitattributes", "* filter=evil\n")
	// 大きさを変えない書き換え。git は中身を確かめるために、ファイルを filter に通す。
	c.write("sub/f.txt", "x\n")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if c.ttyOpened != 0 {
		t.Error("tty を開いた")
	}
	c.noCommitTree()
	if n := len(c.cmd.find("git", " add ")); n != 0 {
		t.Errorf("git add を %d 回呼んだ", n)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("入れ子の設定の filter がホストで実行された")
	}
	if se := c.stderr.String(); !strings.Contains(se, "サブモジュール（gitlink）") || !strings.Contains(se, "sub") {
		t.Errorf("stderr = %s", se)
	}

	// 対照: Inspect を飛ばして TakeSnapshot（SafeConfig 付きの add）に進むと filter が動く。
	// 細工が有効で、拒否がなければ実行されていたことの証明。
	r := gitcommit.Repo{Root: c.proj, GitDir: filepath.Join(c.proj, ".git"), Branch: "refs/heads/main", Head: c.head()}
	s, _ := gitcommit.TakeSnapshot(c.g, r, nil)
	s.Cleanup()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("対照で filter が動いていません: %v", err)
	}
}

// -- の後ろのパスは glob ではなく、そのままの名前。画面にも magic を付けずに出す。
func TestCommitPathLiteral(t *testing.T) {
	c := newCommitEnv(t)
	c.write("a[b].txt", "lit\n")
	c.write("ab.txt", "glob\n")
	c.write(gitcommit.MessageFile, "fix: lit\n")
	c.tty = "y\n"
	if code := c.run("commit", "--", "a[b].txt"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if got := c.git("show", "HEAD:a[b].txt"); got != "lit\n" {
		t.Errorf("HEAD:a[b].txt = %q", got)
	}
	if _, err := c.g.Out(nil, "cat-file", "-e", "HEAD:ab.txt"); err == nil {
		t.Error("指定していない ab.txt がコミットされた")
	}
	se := c.stderr.String()
	if !strings.Contains(se, "a[b].txt") || strings.Contains(se, ":(top") {
		t.Errorf("stderr = %s", se)
	}
}

// pre-commit のフックにも、ホストと同じ top / literal の pathspec を渡す。
func TestCommitHookPathspecLiteral(t *testing.T) {
	c := newCommitEnv(t)
	c.hook(".git/hooks/pre-commit")
	c.container("running", "4")
	c.write("a[b].txt", "lit\n")
	c.write(gitcommit.MessageFile, "fix: lit\n")
	c.tty = "y\n"
	if code := c.run("commit", "--", "a[b].txt"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	execs := c.dockerExecs()
	if len(execs) != 1 {
		t.Fatalf("docker exec = %v", execs)
	}
	if got := strings.Join(execs[0].args[11:], " "); got != ":(top,literal)a[b].txt" {
		t.Errorf("pre-commit の pathspec = %q", got)
	}
}

// push するのは刻印したコミット。pre-push のあいだに HEAD が動いても、そのコミットは送らない。
func TestCommitPushesSealedCommit(t *testing.T) {
	c := newCommitEnv(t)
	bare := c.withUpstream()
	c.hook(".git/hooks/pre-push")
	c.container("running", "4")
	var sealed string
	c.cmd.hooks["pre-push"] = func(stdin string, _ []string, _, _ io.Writer) error {
		sealed = c.head()
		// 利用者がホストで別のコミットを足した
		c.git("commit", "-q", "--allow-empty", "-m", "later")
		return nil
	}
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if sealed == "" || sealed == c.head() {
		t.Fatalf("sealed = %q, head = %q", sealed, c.head())
	}
	if got := bareHead(t, bare); got != sealed {
		t.Errorf("bare の main = %s, want %s（刻印したコミット）", got, sealed)
	}
	push := c.cmd.find("git", " push ")
	if len(push) != 1 || !strings.Contains(push[0].String(), "--recurse-submodules=no") ||
		!strings.Contains(push[0].String(), sealed+":refs/heads/main") {
		t.Errorf("push = %v", push)
	}
}

// .sigil-commit を .gitignore に入れていても（勧めている形）、フックありで最後まで刻印でき、
// 銘のファイルは消え、コミットに入らない。入れ忘れていても同じ。
func TestCommitMessageFileIgnoredOrNot(t *testing.T) {
	for _, ignore := range []bool{true, false} {
		t.Run(fmt.Sprintf("ignore=%v", ignore), func(t *testing.T) {
			c := newCommitEnvWith(t, ignore)
			c.hook(".git/hooks/pre-commit")
			c.container("running", "4")
			c.write("b.txt", "b\n")
			c.write(gitcommit.MessageFile, "feat: b\n")
			c.tty = "y\n"
			if code := c.run("commit"); code != 0 {
				t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
			}
			if _, err := os.Stat(c.msgFile()); !os.IsNotExist(err) {
				t.Error(".sigil-commit が残っている")
			}
			if got := c.git("ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(got, gitcommit.MessageFile) {
				t.Errorf("銘のファイルがコミットに入った: %q", got)
			}
			if got := c.body(); got != "feat: b\n" {
				t.Errorf("銘 = %q", got)
			}
			// フックには除外の指定を渡さない（.gitignore 済みのファイルを名指しすると git add が失敗するため）
			if got := strings.Join(c.dockerExecs()[0].args[11:], " "); got != ":(top)" {
				t.Errorf("pre-commit の pathspec = %q", got)
			}
		})
	}
}

// 銘のファイルそのものを -- で指定したら、わかる言葉で断る。
func TestCommitArgsRejectsMessageFile(t *testing.T) {
	for _, arg := range []string{".sigil-commit", "./.sigil-commit", "sub/../.sigil-commit", ".SIGIL-COMMIT"} {
		t.Run(arg, func(t *testing.T) {
			c := newCommitEnv(t)
			c.write("a.txt", "two\n")
			c.write(gitcommit.MessageFile, "fix: a\n")
			c.tty = "y\n"
			if code := c.run("commit", "--", arg); code != 1 {
				t.Fatalf("code = %d", code)
			}
			se := c.stderr.String()
			if !strings.HasPrefix(se, "sigil: 刻むものを指定できません\n") || !strings.Contains(se, "銘のファイル") {
				t.Errorf("stderr = %s", se)
			}
			c.noCommitTree()
		})
	}
}

// commit の説明は次の行に回し、ほかの行と説明の桁をそろえる。
func TestUsageCommitAligned(t *testing.T) {
	col := strings.Index(usage, "プロジェクトのコンテナに入る")
	start := strings.LastIndex(usage[:col], "\n") + 1
	want := "  sigil commit [-- <path>...]\n" + strings.Repeat(" ", len(usage[start:col])) + "コンテナで用意した銘で"
	if !strings.Contains(usage, want) {
		t.Errorf("usage の commit の行がそろっていない:\n%s", usage)
	}
}

func TestCommitDiffDeltaUnsafeOutputFallsBack(t *testing.T) {
	c := newCommitEnv(t)
	c.fancy()
	c.withDelta()
	c.cmd.delta = func(_ string, stdout io.Writer) error {
		io.WriteString(stdout, "\x1b]8;;file:///x\x07link\x1b]8;;\x07\n")
		return nil
	}
	c.write("a.txt", "one\nx\x1b[31mred\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "d\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if len(c.pages) != 1 || strings.Contains(c.pages[0], "]8;;") || !strings.Contains(c.pages[0], "x^[[31mred") {
		t.Errorf("組み込みの表示に戻っていない: %q", c.pages)
	}
	if !strings.Contains(c.stderr.String(), "sigil: 注意: delta の出力に想定外の制御シーケンス") {
		t.Errorf("注意の行がない: %q", c.stderr.String())
	}
}

func TestCommitDiffDeltaSGRAndELAccepted(t *testing.T) {
	c := newCommitEnv(t)
	c.fancy()
	c.withDelta()
	want := "\x1b[0K\x1b[1;32m+x\x1b[0m\x1b[K\n"
	c.cmd.delta = func(_ string, stdout io.Writer) error {
		io.WriteString(stdout, want)
		return nil
	}
	c.write("a.txt", "one\nx\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "d\ny\n"
	if code := c.run("commit"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if len(c.pages) != 1 || c.pages[0] != want {
		t.Errorf("ページャーへ渡したもの = %q", c.pages)
	}
	if strings.Contains(c.stderr.String(), "想定外") {
		t.Errorf("不要な注意: %q", c.stderr.String())
	}
}

func TestDeltaOutputSafe(t *testing.T) {
	for s, want := range map[string]bool{
		"plain\n\t":           true,
		"\x1b[0K":             true,
		"\x1b[38;5;1mx\x1b[m": true,
		"日本語":                 true,
		"\x1b]8;;u\x07":       false,
		"\x1b":                false,
		"\x1b[":               false,
		"\x1b[2J":             false,
		"a\x07":               false,
		"\u009b31m":           false,
		"\xff":                false,
	} {
		if got := deltaOutputSafe(s); got != want {
			t.Errorf("deltaOutputSafe(%q) = %v, want %v", s, got, want)
		}
	}
}

// 古い layout のコンテナでは、フックを実行しない。sigil と同じく作り直しを求める。
// 古いコンテナには、共有の sigil-claude などがマウントされたままで、そこからほかのプロジェクトに届くため。
func TestCommitHooksRefuseOldLayout(t *testing.T) {
	c := newCommitEnv(t)
	c.hook(".husky/_/pre-commit")
	c.git("config", "core.hooksPath", ".husky/_")
	c.commitAll("add hooks")
	c.container("running", "3")
	c.write("a.txt", "two\n")
	c.write(gitcommit.MessageFile, "fix: a\n")
	c.tty = "y\n"
	if code := c.run("commit"); code != 1 {
		t.Fatalf("code = %d, stderr = %s", code, c.stderr.String())
	}
	if len(c.dockerExecs()) != 0 {
		t.Errorf("古いコンテナでフックを実行した: %v", c.dockerExecs())
	}
	if !strings.Contains(c.stderr.String(), "古い sigil で作られています") {
		t.Errorf("stderr = %s", c.stderr.String())
	}
}
