package gitcommit

import (
	"errors"
	"os"
	"path/filepath"
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
		"MERGE_HEAD": func(t *testing.T, root string, g Git) {
			write(t, root, ".git/MERGE_HEAD", "0000000000000000000000000000000000000000\n")
		},
		"lfs attributes": func(t *testing.T, root string, g Git) {
			write(t, root, "sub/.gitattributes", "*.bin filter=lfs diff=lfs merge=lfs -text\n")
		},
		"lfsconfig": func(t *testing.T, root string, g Git) {
			write(t, root, ".lfsconfig", "[lfs]\n\turl = https://example.com\n")
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
