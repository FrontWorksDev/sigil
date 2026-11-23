package gitcommit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepo は、利用者の全体設定に左右されない git リポジトリを作る。
func newRepo(t *testing.T) (string, Git) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git がありません")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := Git{R: ExecRunner{}, Root: root, Env: []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1"}}
	run := func(args ...string) {
		t.Helper()
		if _, err := g.Out(nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "t")
	run("config", "user.email", "t@example.com")
	run("config", "commit.gpgSign", "false")
	return root, g
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, g Git, msg string) {
	t.Helper()
	if _, err := g.Out(nil, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Out(nil, "commit", "-q", "-m", msg); err != nil {
		t.Fatal(err)
	}
}
