package gitcommit

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeExec(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestFindHooks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".husky", "_")
	writeExec(t, filepath.Join(dir, PreCommit), "#!/bin/sh\n", 0o755)
	writeExec(t, filepath.Join(dir, CommitMsg), "#!/bin/sh\n", 0o644)
	if err := os.MkdirAll(filepath.Join(dir, PrePush), 0o755); err != nil { // ディレクトリは対象外
		t.Fatal(err)
	}
	h, err := FindHooks(Repo{Root: root, HooksDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Present[PreCommit] || h.Present[CommitMsg] || h.Present[PrePush] || h.Present[PrepareCommitMsg] {
		t.Errorf("Present=%v", h.Present)
	}
	if h.Dir != dir {
		t.Errorf("Dir=%q", h.Dir)
	}
	if !h.Any(PreCommit, CommitMsg) || h.Any(CommitMsg, PrePush) || h.Any() {
		t.Error("Any の結果が違う")
	}
}

func TestFindHooksOutside(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	if _, err := FindHooks(Repo{Root: root, HooksDir: out}); err != nil {
		t.Fatalf("フックがなければ拒否しない: %v", err)
	}
	writeExec(t, filepath.Join(out, PreCommit), "#!/bin/sh\n", 0o755)
	_, err := FindHooks(Repo{Root: root, HooksDir: out})
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("Refusal ではない: %v", err)
	}
	if !strings.Contains(ref.Msg, out) || !strings.Contains(ref.Msg, "手動でコミット") {
		t.Errorf("Msg=%q", ref.Msg)
	}

	// フックの場所に制御文字があっても、画面を操作させない
	odd := filepath.Join(t.TempDir(), "h\x1b[2J\nx")
	writeExec(t, filepath.Join(odd, PreCommit), "#!/bin/sh\n", 0o755)
	_, err = FindHooks(Repo{Root: root, HooksDir: odd})
	if !errors.As(err, &ref) {
		t.Fatalf("Refusal ではない: %v", err)
	}
	if strings.ContainsAny(ref.Msg, "\x1b\n") || !strings.Contains(ref.Msg, "h^[[2J^Jx") {
		t.Errorf("Msg=%q", ref.Msg)
	}
}

func TestHookExecArgs(t *testing.T) {
	extra := []string{".", ":(exclude,top).sigil-commit"}
	got, err := HookExecArgs("sigil-p-1", "/workspace/p", "/h/p", "/h/p/.husky/_", PreCommit, extra)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "-i", "-w", "/workspace/p", "sigil-p-1", "sh", "-c", hookScript, "sh",
		"pre-commit", "/workspace/p/.husky/_/pre-commit", ".", ":(exclude,top).sigil-commit"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got=%q\nwant=%q", got, want)
	}
	// mode の "pre-commit" は case の分岐名として必要なので、フックのパスと他の名前で確かめる
	for _, s := range []string{"/h/p", ".husky", "/workspace", "sigil-p-1", PrepareCommitMsg, CommitMsg} {
		if strings.Contains(hookScript, s) {
			t.Errorf("hookScript に %q が入っている", s)
		}
	}
	for name, mode := range map[string]string{PrepareCommitMsg: "msg", CommitMsg: "msg", PrePush: "pre-push"} {
		a, err := HookExecArgs("c", "/w", "/h/p", "/h/p/.git/hooks", name, nil)
		if err != nil {
			t.Fatal(err)
		}
		if a[9] != mode || a[10] != "/w/.git/hooks/"+name {
			t.Errorf("%s: %q", name, a[9:])
		}
	}
	if _, err := HookExecArgs("c", "/w", "/h/p", "/elsewhere", PreCommit, nil); err == nil {
		t.Error("hostRoot の外は誤り")
	}
}

type hookRecorder struct {
	name string
	args []string
	o    RunOpts
}

func (f *hookRecorder) Run(name string, args []string, o RunOpts) error {
	f.name, f.args, f.o = name, args, o
	return nil
}

func TestRunHook(t *testing.T) {
	f := &hookRecorder{}
	var so, se bytes.Buffer
	in := strings.NewReader("x")
	if err := RunHook(f, []string{"exec"}, in, &so, &se); err != nil {
		t.Fatal(err)
	}
	if f.name != "docker" || !reflect.DeepEqual(f.args, []string{"exec"}) || f.o.Stdin != in || f.o.Stdout != &so || f.o.Stderr != &se {
		t.Errorf("%+v", f)
	}
}

// runScript は hookScript をホストの sh で直接実行する。
func runScript(t *testing.T, root string, stdin string, args ...string) (string, string, error) {
	t.Helper()
	full := append([]string{"-c", hookScript, "sh"}, args...)
	cmd := exec.Command("sh", full...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	cmd.Stdin = strings.NewReader(stdin)
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	return o.String(), e.String(), err
}

func readOnly(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root では読み取り専用にならない")
	}
	t.Cleanup(func() {
		_ = exec.Command("chmod", "-R", "u+w", dir).Run()
	})
	if err := exec.Command("chmod", "-R", "a-w", dir).Run(); err != nil {
		t.Fatal(err)
	}
}

func TestHookScriptReadOnlyGit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh がありません")
	}
	for _, withHead := range []bool{true, false} {
		name := "initial"
		if withHead {
			name = "with-commit"
		}
		t.Run(name, func(t *testing.T) {
			root, g := newRepo(t)
			// 勧めどおり、銘のファイルは .gitignore に入っている
			write(t, root, ".gitignore", MessageFile+"\n")
			if withHead {
				write(t, root, "old.txt", "old\n")
				commitAll(t, g, "first")
			}
			write(t, root, "new.txt", "n\n")
			write(t, root, MessageFile, "msg\n")
			outFile := filepath.Join(t.TempDir(), "out")
			hook := filepath.Join(t.TempDir(), "hook")
			writeExec(t, hook, "#!/bin/sh\ngit add new.txt && git diff --cached --name-only > \""+outFile+"\"\n", 0o755)
			readOnly(t, filepath.Join(root, ".git"))

			before := indexHash(t, root)
			args := append([]string{PreCommit, hook}, AddPathspec(nil)...)
			_, se, err := runScript(t, root, "", args...)
			if err != nil {
				t.Fatalf("%v: %s", err, se)
			}
			if after := indexHash(t, root); after != before {
				t.Error("本物の index が変わった")
			}
			b, _ := os.ReadFile(outFile)
			if !strings.Contains(string(b), "new.txt") || strings.Contains(string(b), MessageFile) {
				t.Errorf("OUT=%q", b)
			}
		})
	}
}

// 銘のファイルが追跡されていて、.gitignore に入っていないときも、フックの一時 index には入らない。
func TestHookScriptMessageFileTracked(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, MessageFile, "old\n")
	commitAll(t, g, "first")
	write(t, root, MessageFile, "new\n")
	write(t, root, "a.txt", "a\n")
	outFile := filepath.Join(t.TempDir(), "out")
	hook := filepath.Join(t.TempDir(), "hook")
	writeExec(t, hook, "#!/bin/sh\ngit diff --cached --name-only > \""+outFile+"\"\n", 0o755)
	readOnly(t, filepath.Join(root, ".git"))
	args := append([]string{PreCommit, hook}, AddPathspec(nil)...)
	if _, se, err := runScript(t, root, "", args...); err != nil {
		t.Fatalf("%v: %s", err, se)
	}
	if b, _ := os.ReadFile(outFile); strings.TrimSpace(string(b)) != "a.txt" {
		t.Errorf("OUT=%q", b)
	}
}

func TestHookScriptMsg(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh がありません")
	}
	for _, withHead := range []bool{true, false} {
		root, g := newRepo(t)
		if withHead {
			write(t, root, "a", "a")
			commitAll(t, g, "first")
		}
		hook := filepath.Join(t.TempDir(), "hook")
		// 標準出力に出したものはメッセージに混ざってはならない
		writeExec(t, hook, "#!/bin/sh\necho NOISE\nprintf X >> \"$1\"\n", 0o755)
		readOnly(t, filepath.Join(root, ".git"))
		before := indexHash(t, root)
		so, se, err := runScript(t, root, "msg", "msg", hook)
		if err != nil {
			t.Fatalf("%v: %s", err, se)
		}
		if indexHash(t, root) != before {
			t.Error("本物の index が変わった")
		}
		if so != "msgX" {
			t.Errorf("withHead=%v stdout=%q", withHead, so)
		}
		if !strings.Contains(se, "NOISE") {
			t.Errorf("フックの出力は標準エラーへ: %q", se)
		}
	}
}

func indexHash(t *testing.T, root string) [32]byte {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, ".git", "index")) // 無ければ空
	return sha256.Sum256(b)
}

func TestHookScriptUnknownMode(t *testing.T) {
	root, _ := newRepo(t)
	_, se, err := runScript(t, root, "", "bogus", "/bin/true")
	if err == nil || !strings.Contains(se, "unknown mode") {
		t.Errorf("err=%v stderr=%q", err, se)
	}
}

func TestHookScriptCDPATH(t *testing.T) {
	root, _ := newRepo(t)
	write(t, root, "a", "a")
	hook := filepath.Join(t.TempDir(), "hook")
	writeExec(t, hook, "#!/bin/sh\ntrue\n", 0o755)
	t.Setenv("CDPATH", t.TempDir())
	if _, se, err := runScript(t, root, "", PreCommit, hook, "."); err != nil {
		t.Fatalf("%v: %s", err, se)
	}
}
