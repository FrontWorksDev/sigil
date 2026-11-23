package gitcommit

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sealSetup は、初回コミット済みのリポジトリで Inspect と TakeSnapshot まで進める。
func sealSetup(t *testing.T, pathspec []string, edit func(root string)) (string, Git, Repo, Snapshot) {
	t.Helper()
	root, g := newRepo(t)
	write(t, root, "a.txt", "one\n")
	commitAll(t, g, "init")
	if edit != nil {
		edit(root)
	}
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, pathspec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Cleanup)
	return root, g, r, s
}

func gitOut(t *testing.T, g Git, args ...string) string {
	t.Helper()
	out, err := g.Out(nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSealCommitsSnapshotNotWorktree(t *testing.T) {
	root, g, r, s := sealSetup(t, nil, func(root string) { write(t, root, "a.txt", "two\n") })
	write(t, root, "a.txt", "tampered\n")
	c, err := Seal(g, r, s, "msg", nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOut(t, g, "rev-parse", c+"^{tree}")); got != s.Tree {
		t.Errorf("tree = %s, want %s", got, s.Tree)
	}
	if got := gitOut(t, g, "show", "HEAD:a.txt"); got != "two\n" {
		t.Errorf("HEAD:a.txt = %q", got)
	}
	if got := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD")); got != c {
		t.Errorf("HEAD = %s, want %s", got, c)
	}
}

func TestSealMessageExact(t *testing.T) {
	_, g, r, s := sealSetup(t, nil, func(root string) { write(t, root, "a.txt", "two\n") })
	if _, err := Seal(g, r, s, "feat: x\n\n# keep", nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSuffix(gitOut(t, g, "log", "-1", "--format=%B"), "\n")
	got = strings.TrimSuffix(got, "\n")
	if got != "feat: x\n\n# keep" {
		t.Errorf("message = %q", got)
	}
	raw := gitOut(t, g, "cat-file", "commit", "HEAD")
	if _, body, _ := strings.Cut(raw, "\n\n"); body != "feat: x\n\n# keep\n" {
		t.Errorf("commit body = %q", body)
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".git", "ORIG_HEAD")); err == nil {
		t.Error("ORIG_HEAD ができました")
	}
	if rl := gitOut(t, g, "reflog", "-1", "--format=%gs"); !strings.Contains(rl, "commit (sigil): feat: x") {
		t.Errorf("reflog = %q", rl)
	}
}

func TestSealInitialCommit(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "one\n")
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cleanup()
	c, err := Seal(g, r, s, "first", nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if out := gitOut(t, g, "rev-list", "--parents", "-n", "1", c); strings.TrimSpace(out) != c {
		t.Errorf("親があります: %q", out)
	}
	if got := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD")); got != c {
		t.Errorf("HEAD = %s", got)
	}
}

func TestSealMovedHead(t *testing.T) {
	root, g, r, s := sealSetup(t, nil, func(root string) { write(t, root, "a.txt", "two\n") })
	write(t, root, "other.txt", "x\n")
	commitAll(t, g, "other")
	other := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD"))
	before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Seal(g, r, s, "msg", nil, io.Discard)
	if !errors.Is(err, ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	if got := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD")); got != other {
		t.Errorf("ブランチが動きました: %s", got)
	}
	after, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("本物の index が変わりました")
	}
}

func TestSealKeepsOtherStaged(t *testing.T) {
	_, g, r, s := sealSetup(t, []string{"a.txt"}, func(root string) {
		write(t, root, "a.txt", "two\n")
		write(t, root, "b.txt", "bee\n")
	})
	gitOut(t, g, "add", "b.txt")
	if _, err := Seal(g, r, s, "msg", []string{"a.txt"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOut(t, g, "diff", "--cached", "--name-only")); got != "b.txt" {
		t.Errorf("staged = %q, want b.txt", got)
	}
}

// 刻印後の本物の index の reset も、パスをそのままの名前として読む（glob で ab.txt まで戻さない）。
func TestSealResetLiteral(t *testing.T) {
	_, g, r, s := sealSetup(t, []string{"a[b].txt"}, func(root string) {
		write(t, root, "a[b].txt", "lit\n")
		write(t, root, "ab.txt", "glob\n")
	})
	gitOut(t, g, "add", "ab.txt")
	if _, err := Seal(g, r, s, "msg", []string{"a[b].txt"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOut(t, g, "diff", "--cached", "--name-only")); got != "ab.txt" {
		t.Errorf("staged = %q, want ab.txt", got)
	}
	if got := strings.TrimSpace(gitOut(t, g, "ls-tree", "--name-only", "HEAD")); got != "a.txt\na[b].txt" {
		t.Errorf("HEAD = %q", got)
	}
}

func TestSealDoesNotRunHooks(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "one\n")
	commitAll(t, g, "init")
	gitOut(t, g, "config", "core.hooksPath", ".husky")
	mark := filepath.Join(t.TempDir(), "mark")
	script := "#!/bin/sh\ntouch '" + mark + "'\n"
	for _, h := range []string{"pre-commit", "commit-msg", "post-commit", "prepare-commit-msg", "reference-transaction"} {
		p := filepath.Join(root, ".husky", h)
		write(t, root, filepath.Join(".husky", h), script)
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "a.txt", "two\n")
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cleanup()
	if _, err := Seal(g, r, s, "msg", nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("hook が実行されました")
	}
	// 比較: 素の git commit では hook が動く（hook を正しく置けている確認）
	gitOut(t, g, "-c", "core.hooksPath=.husky", "commit", "-q", "--allow-empty", "-m", "x")
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("素の git commit でも hook が動きません（テストの準備が誤り）")
	}
}

func TestSealInitialCommitRace(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "one\n")
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cleanup()
	commitAll(t, g, "raced")
	raced := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD"))
	_, err = Seal(g, r, s, "msg", nil, io.Discard)
	if !errors.Is(err, ErrMoved) {
		t.Fatalf("err = %v, want ErrMoved", err)
	}
	if got := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD")); got != raced {
		t.Errorf("ブランチが動きました: %s", got)
	}
}

// stderrRunner は、stderr に固定の文字列を書いて失敗する偽の Runner。
type stderrRunner struct{ text string }

func (f stderrRunner) Run(name string, args []string, o RunOpts) error {
	_, _ = io.WriteString(o.Stderr, f.text)
	return errors.New("exit 1")
}

func TestInteractiveEscapesStderr(t *testing.T) {
	g := Git{R: stderrRunner{"\x1b[2J"}, Root: "/x"}
	var w strings.Builder
	_, err := g.Interactive(nil, &w, "commit-tree")
	if err == nil {
		t.Fatal("エラーになるはず")
	}
	if w.String() != "^[[2J" || strings.Contains(w.String(), "\x1b") {
		t.Errorf("stderr = %q", w.String())
	}
	if !strings.Contains(err.Error(), "^[[2J") || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("err = %q", err.Error())
	}
}
