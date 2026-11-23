package gitcommit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectRefuses(t *testing.T) {
	cases := map[string]func(t *testing.T, root string, g Git){
		"detached HEAD": func(t *testing.T, root string, g Git) {
			write(t, root, "a.txt", "a")
			commitAll(t, g, "c1")
			if _, err := g.Out(nil, "checkout", "-q", "--detach"); err != nil {
				t.Fatal(err)
			}
		},
		"include": func(t *testing.T, root string, g Git) {
			write(t, root, "evil.gitconfig", "[core]\n\tsshCommand = x\n")
			f, err := os.OpenFile(filepath.Join(root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("[include]\n\tpath = ../evil.gitconfig\n"); err != nil {
				t.Fatal(err)
			}
		},
		"include missing file": func(t *testing.T, root string, g Git) {
			f, err := os.OpenFile(filepath.Join(root, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("[include]\n\tpath = ../.gitconfig-proj\n[includeIf \"gitdir:/nonexistent/\"]\n\tpath = ../.gitconfig-if\n"); err != nil {
				t.Fatal(err)
			}
		},
		"gitfile into worktree": func(t *testing.T, root string, g Git) {
			if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "gitdir")); err != nil {
				t.Fatal(err)
			}
			write(t, root, ".git", "gitdir: "+filepath.Join(root, "gitdir")+"\n")
		},
		"MERGE_HEAD": func(t *testing.T, root string, g Git) {
			write(t, root, ".git/MERGE_HEAD", "0000000000000000000000000000000000000000\n")
		},
		"lfsconfig": func(t *testing.T, root string, g Git) {
			write(t, root, ".lfsconfig", "[lfs]\n\turl = https://example.com\n")
		},
		"submodule in HEAD": func(t *testing.T, root string, g Git) {
			write(t, root, "a.txt", "a")
			commitAll(t, g, "c1")
			commitSubmodule(t, root, g, "sub")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			root, g := newRepo(t)
			setup(t, root, g)
			_, err := Inspect(g)
			var ref *Refusal
			if !errors.As(err, &ref) {
				t.Fatalf("Refusal を期待しましたが %v でした", err)
			}
			if ref.Msg == "" || ref.Lore == "" {
				t.Errorf("Msg / Lore が空です: %+v", ref)
			}
		})
	}
}

// HEAD にある gitlink は、変更がなくても拒否する。git add がサブモジュールの中で git を動かし、
// コンテナが書ける入れ子の設定（filter など）をホストで読むため。パスは Escape して出す。
func TestInspectRefusesSubmoduleInHEAD(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	commitSubmodule(t, root, g, "d/s\x1bub")
	_, err := Inspect(g)
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("Refusal を期待しましたが %v でした", err)
	}
	if !strings.Contains(ref.Msg, "サブモジュール（gitlink）を含むリポジトリには対応していません。手動でコミットしてください") ||
		!strings.Contains(ref.Msg, "d/s^[ub") || strings.Contains(ref.Msg, "\x1b") {
		t.Errorf("Msg=%q", ref.Msg)
	}
}

func TestInspectOK(t *testing.T) {
	root, g := newRepo(t)
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.Head != "" || r.Branch != "refs/heads/main" {
		t.Errorf("Head=%q Branch=%q", r.Head, r.Branch)
	}
	if r.Root != root || r.GitDir != filepath.Join(root, ".git") || r.CommonDir != filepath.Join(root, ".git") {
		t.Errorf("パスが違います: %+v", r)
	}
	if r.HooksDir != filepath.Join(root, ".git", "hooks") {
		t.Errorf("HooksDir=%q", r.HooksDir)
	}

	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	if _, err := g.Out(nil, "config", "core.hooksPath", ".husky/_"); err != nil {
		t.Fatal(err)
	}
	r, err = Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Head) < 40 {
		t.Errorf("Head=%q", r.Head)
	}
	if r.HooksDir != root+"/.husky/_" {
		t.Errorf("HooksDir=%q", r.HooksDir)
	}
}

func TestInspectRefusesLinkedWorktree(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a")
	commitAll(t, g, "c1")
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(other, "wt")
	if _, err := g.Out(nil, "worktree", "add", "-q", "-b", "wt", wt); err != nil {
		t.Fatal(err)
	}
	wg := g
	wg.Root = wt
	_, err = Inspect(wg)
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("Refusal を期待しましたが %v でした", err)
	}
}

func TestInspectRootMustBeToplevel(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "sub/a.txt", "a")
	g.Root = filepath.Join(root, "sub")
	_, err := Inspect(g)
	if err == nil {
		t.Fatal("エラーを期待しました")
	}
	var ref *Refusal
	if errors.As(err, &ref) {
		t.Errorf("Refusal ではなく通常のエラーのはず: %v", err)
	}
}

func TestInspectEmptyHooksPath(t *testing.T) {
	root, g := newRepo(t)
	if _, err := g.Out(nil, "config", "core.hooksPath", ""); err != nil {
		t.Fatal(err)
	}
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	if r.HooksDir != root {
		t.Errorf("HooksDir=%q", r.HooksDir)
	}
}
