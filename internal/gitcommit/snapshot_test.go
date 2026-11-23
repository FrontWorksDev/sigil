package gitcommit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snap(t *testing.T, g Git, pathspec ...string) Snapshot {
	t.Helper()
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, pathspec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Cleanup)
	return s
}

func byPath(s Snapshot) map[string]Change {
	m := map[string]Change{}
	for _, c := range s.Changes {
		m[c.Path] = c
	}
	return m
}

func TestSnapshotAllChanges(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "mod.txt", "1\n")
	write(t, root, "del.txt", "x\n")
	commitAll(t, g, "c1")
	write(t, root, "mod.txt", "1\n2\n")
	os.Remove(filepath.Join(root, "del.txt"))
	write(t, root, "new.txt", "n\n")
	write(t, root, "a b/日本語.txt", "j\n")
	write(t, root, MessageFile, "msg\n")

	s := snap(t, g)
	m := byPath(s)
	if len(s.Changes) != 4 {
		t.Fatalf("Changes=%+v", s.Changes)
	}
	want := map[string]byte{"mod.txt": 'M', "del.txt": 'D', "new.txt": 'A', "a b/日本語.txt": 'A'}
	for p, st := range want {
		if m[p].Status != st {
			t.Errorf("%s: Status=%c want %c", p, m[p].Status, st)
		}
	}
	if m["mod.txt"].Added != 1 || m["mod.txt"].Deleted != 0 {
		t.Errorf("mod numstat: %+v", m["mod.txt"])
	}
	if m["new.txt"].Size != 2 {
		t.Errorf("Size=%d", m["new.txt"].Size)
	}
	if _, ok := m[MessageFile]; ok {
		t.Error(".sigil-commit が入っています")
	}
	if out, _ := g.Out(nil, "diff", "--cached", "--name-only"); out != "" {
		t.Errorf("本物の index が変わっています: %q", out)
	}
	if st, err := os.Stat(s.TmpDir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("TmpDir: %v %v", st, err)
	}
	s.Cleanup()
	if _, err := os.Stat(s.TmpDir); !os.IsNotExist(err) {
		t.Error("Cleanup で消えていません")
	}
}

func TestSnapshotPathspec(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	write(t, root, "b.txt", "b")
	s := snap(t, g, "a.txt")
	if len(s.Changes) != 1 || s.Changes[0].Path != "a.txt" {
		t.Errorf("%+v", s.Changes)
	}
}

// 利用者のパスは glob ではなく、そのままの名前として読む。
func TestSnapshotPathspecLiteral(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a[b].txt", "1")
	write(t, root, "ab.txt", "2")
	s := snap(t, g, "a[b].txt")
	if len(s.Changes) != 1 || s.Changes[0].Path != "a[b].txt" {
		t.Errorf("%+v", s.Changes)
	}
}

func TestEffectivePathspec(t *testing.T) {
	got := strings.Join(EffectivePathspec(nil), " ")
	if got != ":(top) :(exclude,top).sigil-commit" {
		t.Errorf("既定 = %q", got)
	}
	got = strings.Join(EffectivePathspec([]string{"a[b].txt", "d/*", "."}), " ")
	if got != ":(top,literal)a[b].txt :(top,literal)d/* :(top) :(exclude,top).sigil-commit" {
		t.Errorf("指定 = %q", got)
	}
}

// 最上位で -- . と指定したとき（commitArgs は "." を返す）も、全体が入る。
func TestSnapshotPathspecDot(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	write(t, root, "d/b.txt", "b")
	write(t, root, MessageFile, "m")
	s := snap(t, g, ".")
	if len(s.Changes) != 2 {
		t.Errorf("%+v", s.Changes)
	}
}

func TestSnapshotInitialCommit(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	write(t, root, "d/b.txt", "b")
	s := snap(t, g)
	if len(s.Changes) != 2 {
		t.Fatalf("%+v", s.Changes)
	}
	for _, c := range s.Changes {
		if c.Status != 'A' {
			t.Errorf("%+v", c)
		}
	}
	if s.Base == "" || s.Tree == "" {
		t.Errorf("%+v", s)
	}
}

func TestSnapshotNameWithNewline(t *testing.T) {
	root, g := newRepo(t)
	name := "x\ny\x1b.txt"
	write(t, root, name, "a")
	s := snap(t, g)
	if len(s.Changes) != 1 || s.Changes[0].Path != name {
		t.Errorf("%+v", s.Changes)
	}
}

func TestSnapshotRefusesGitlink(t *testing.T) {
	tmpRoot := t.TempDir()
	t.Setenv("TMPDIR", tmpRoot)
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	sub := filepath.Join(root, "sub")
	write(t, root, "sub/x.txt", "x")
	sg := Git{R: ExecRunner{}, Root: sub, Env: g.Env}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.name", "t"}, {"config", "user.email", "t@example.com"},
		{"add", "-A"}, {"commit", "-q", "-m", "s"},
	} {
		if _, err := sg.Out(nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, nil)
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("Refusal を期待しましたが %v でした", err)
	}
	if s.TmpDir != "" {
		t.Error("失敗時は TmpDir が空のはず")
	}
	// 一時フォルダが残っていない
	if m, _ := filepath.Glob(filepath.Join(tmpRoot, "sigil-commit-*")); len(m) != 0 {
		t.Errorf("一時フォルダが残っています: %v", m)
	}
}

func TestSnapshotNothing(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	write(t, root, MessageFile, "msg")
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TakeSnapshot(g, r, nil); !errors.Is(err, ErrNothing) {
		t.Errorf("err=%v", err)
	}
}

func TestSnapshotBinaryAndLink(t *testing.T) {
	root, g := newRepo(t)
	if err := os.WriteFile(filepath.Join(root, "b.bin"), []byte{0, 1, 2, 0, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../target\x1b", filepath.Join(root, "ln")); err != nil {
		t.Fatal(err)
	}
	s := snap(t, g)
	m := byPath(s)
	if !m["b.bin"].Binary || m["b.bin"].Size != 5 {
		t.Errorf("%+v", m["b.bin"])
	}
	if m["ln"].NewMode != "120000" || m["ln"].LinkTarget != "../target\x1b" {
		t.Errorf("%+v", m["ln"])
	}
}

func TestSnapshotRename(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "old.txt", strings.Repeat("line\n", 20))
	commitAll(t, g, "c1")
	if err := os.Rename(filepath.Join(root, "old.txt"), filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	s := snap(t, g)
	if len(s.Changes) != 1 || s.Changes[0].Status != 'R' || s.Changes[0].OldPath != "old.txt" || s.Changes[0].Path != "new.txt" {
		t.Errorf("%+v", s.Changes)
	}
}

func TestSnapshotKeepsStagedStateOutOfRealIndex(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	write(t, root, "staged.txt", "s")
	if _, err := g.Out(nil, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	write(t, root, "other.txt", "o")
	s := snap(t, g, "other.txt")
	if len(s.Changes) != 1 || s.Changes[0].Path != "other.txt" {
		t.Errorf("%+v", s.Changes)
	}
	if out, _ := g.Out(nil, "diff", "--cached", "--name-only"); strings.TrimSpace(out) != "staged.txt" {
		t.Errorf("index=%q", out)
	}
}

func TestWarnings(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "keep.txt", "k")
	commitAll(t, g, "c1")
	write(t, root, ".github/workflows/ci.yml", "x")
	write(t, root, ".env.local", "x")
	write(t, root, ".gitignore", "x")
	write(t, root, ".husky/pre-commit", "x")
	write(t, root, ".sigil", "x")
	write(t, root, "src/a.go", "package a\n")
	write(t, root, "tool.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(root, "tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("src/a.go", filepath.Join(root, "ln")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "big.dat", strings.Repeat("x", 2<<20))
	os.Remove(filepath.Join(root, "keep.txt"))

	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	s, err := TakeSnapshot(g, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cleanup()
	ws := Warnings(s.Changes, r)
	got := map[string]string{}
	for _, w := range ws {
		got[w.Path] += w.Reason + "|"
	}
	for p, sub := range map[string]string{
		".github/workflows/ci.yml": "GitHub などで実行されます",
		".env.local":               "秘密",
		".gitignore":               "git の動作",
		".husky/pre-commit":        "ほかの人の環境や CI で実行されます",
		".sigil":                   "sigil",
		"tool.sh":                  "実行権限",
		"ln":                       "src/a.go",
		"big.dat":                  "1MiB",
	} {
		if !strings.Contains(got[p], sub) {
			t.Errorf("%s: %q に %q がありません", p, got[p], sub)
		}
	}
	if _, ok := got["src/a.go"]; ok {
		t.Error("src/a.go に警告が付いています")
	}
	if _, ok := got["keep.txt"]; ok {
		t.Error("削除に警告が付いています")
	}
}

func TestWarningsHooksDir(t *testing.T) {
	r := Repo{Root: "/p", HooksDir: "/p/.git/hooks"}
	cs := []Change{{Status: 'A', Path: ".git/hooks/pre-commit", NewMode: "100644"}}
	if ws := Warnings(cs, r); len(ws) != 1 || !strings.Contains(ws[0].Reason, "ほかの人") {
		t.Errorf("%+v", ws)
	}
}

func TestSnapshotInitialCommitIgnoresRealIndex(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	write(t, root, "b.txt", "b")
	if _, err := g.Out(nil, "add", "b.txt"); err != nil {
		t.Fatal(err)
	}
	s := snap(t, g, "a.txt")
	if len(s.Changes) != 1 || s.Changes[0].Path != "a.txt" {
		t.Errorf("%+v", s.Changes)
	}
}

func TestSnapshotLeavesNoResetTraces(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	write(t, root, "a.txt", "b")
	before, _ := os.ReadFile(filepath.Join(root, ".git", "logs", "HEAD"))
	snap(t, g)
	after, _ := os.ReadFile(filepath.Join(root, ".git", "logs", "HEAD"))
	if string(before) != string(after) {
		t.Errorf("HEAD の reflog が変わっています:\n%s\n---\n%s", before, after)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "ORIG_HEAD")); !os.IsNotExist(err) {
		t.Errorf("ORIG_HEAD ができています: %v", err)
	}
}

func TestSnapshotLFS(t *testing.T) {
	lfsAttrs := "*.bin filter=lfs diff=lfs merge=lfs -text\n"
	refuses := func(t *testing.T, g Git) {
		t.Helper()
		r, err := Inspect(g)
		if err != nil {
			t.Fatal(err)
		}
		_, err = TakeSnapshot(g, r, nil)
		var ref *Refusal
		if !errors.As(err, &ref) {
			t.Fatalf("Refusal を期待しましたが %v でした", err)
		}
	}
	t.Run("top-level", func(t *testing.T) {
		root, g := newRepo(t)
		write(t, root, ".gitattributes", lfsAttrs)
		write(t, root, "x.bin", "x")
		refuses(t, g)
	})
	t.Run("ignored gitattributes", func(t *testing.T) {
		root, g := newRepo(t)
		write(t, root, ".gitignore", ".gitattributes\n")
		write(t, root, ".gitattributes", lfsAttrs)
		write(t, root, "x.bin", "x")
		refuses(t, g)
	})
	t.Run("nested", func(t *testing.T) {
		root, g := newRepo(t)
		write(t, root, "sub/.gitattributes", lfsAttrs)
		write(t, root, "sub/x.bin", "x")
		refuses(t, g)
	})
	// add のあとで作業ツリーの .gitattributes が書き換えられても、確定した tree の属性で調べる。
	t.Run("tree attributes", func(t *testing.T) {
		root, g := newRepo(t)
		write(t, root, ".gitattributes", lfsAttrs)
		write(t, root, "x.bin", "x")
		commitAll(t, g, "c1")
		tree := strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD^{tree}"))
		write(t, root, ".gitattributes", "*.txt text\n")
		err := checkLFS(g, tree, []Change{{Status: 'A', Path: "x.bin"}})
		var ref *Refusal
		if !errors.As(err, &ref) {
			t.Fatalf("Refusal を期待しましたが %v でした", err)
		}
		// 対照: どちらにも lfs がなければ拒否しない
		write(t, root, ".gitattributes", "*.txt text\n")
		commitAll(t, g, "c2")
		tree = strings.TrimSpace(gitOut(t, g, "rev-parse", "HEAD^{tree}"))
		if err := checkLFS(g, tree, []Change{{Status: 'A', Path: "x.bin"}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("not in commit", func(t *testing.T) {
		root, g := newRepo(t)
		write(t, root, ".gitattributes", lfsAttrs)
		write(t, root, "x.bin", "x")
		write(t, root, "ok.txt", "ok")
		r, err := Inspect(g)
		if err != nil {
			t.Fatal(err)
		}
		s, err := TakeSnapshot(g, r, []string{"ok.txt"})
		if err != nil {
			t.Fatal(err)
		}
		s.Cleanup()
	})
}
