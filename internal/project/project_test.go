package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerNameVectors(t *testing.T) {
	cases := map[string]string{
		"/tmp/t/home/proj":    "sigil-proj-33bf5961",
		"/tmp/t/home/my proj": "sigil-my-proj-42cb34c6",
	}
	for root, want := range cases {
		if got := New(root).ContainerName; got != want {
			t.Errorf("New(%q).ContainerName = %q, want %q", root, got, want)
		}
	}
}

func TestSafeName(t *testing.T) {
	if got := SafeName(strings.Repeat("a", 50)); got != strings.Repeat("a", 40) {
		t.Errorf("40 バイトに切り詰められない: %q", got)
	}
	// 1 文字 3 バイトなので、バイトごとに置き換わる（シェル版の tr -c と同じ）
	if got := SafeName("日本"); got != "------" {
		t.Errorf("SafeName(日本) = %q", got)
	}
	if got := SafeName("a.b_c-d e:f,g"); got != "a.b_c-d-e-f-g" {
		t.Errorf("SafeName = %q", got)
	}
}

func TestContainerPath(t *testing.T) {
	cases := map[string]string{
		"/x/my proj": "/workspace/my proj",
		"/x/日本語":     "/workspace/日本語",
		"/x/a:b":     "/workspace/a-b",
		"/x/a,b":     "/workspace/a-b",
	}
	for root, want := range cases {
		if got := New(root).ContainerPath; got != want {
			t.Errorf("New(%q).ContainerPath = %q, want %q", root, got, want)
		}
	}
}

func TestWorkdir(t *testing.T) {
	p := New("/src/app")
	cases := map[string]string{
		"/src/app":         "/workspace/app",
		"/src/app/web/src": "/workspace/app/web/src",
		"/src/application": "/workspace/app",
		"/elsewhere":       "/workspace/app",
	}
	for cwd, want := range cases {
		if got := p.Workdir(cwd); got != want {
			t.Errorf("Workdir(%q) = %q, want %q", cwd, got, want)
		}
	}
}

func TestCheckSafe(t *testing.T) {
	home := "/Users/k"
	for _, root := range []string{"/", "/Users", "/home", "/Users/k"} {
		if err := CheckSafe(root, home); err == nil {
			t.Errorf("CheckSafe(%q) が通ってしまう", root)
		}
	}
	// ホームを含むフォルダ
	if err := CheckSafe("/data", "/data/users/k"); err == nil {
		t.Error("ホームを含むフォルダが通ってしまう")
	}
	for _, root := range []string{"/Users/k/x", "/Users/kk", "/tmp/p", "/Users/k/src/.config-tools"} {
		if err := CheckSafe(root, home); err != nil {
			t.Errorf("CheckSafe(%q) = %v", root, err)
		}
	}
}

func TestCheckSafeHomeWithTrailingSlash(t *testing.T) {
	err := CheckSafe("/Users/k", "/Users/k/")
	if err == nil || !strings.Contains(err.Error(), "全体はマウントできません") {
		t.Errorf("CheckSafe = %v", err)
	}
}

func TestResolveRejectsColonInPath(t *testing.T) {
	// docker の -v は ":" で区切るので、ホスト側のパスに ":" があると正しくマウントできない
	base := realTemp(t)
	for _, rel := range []string{"a:b/proj", "proj:x"} {
		dir := filepath.Join(base, rel)
		mustMkdir(t, dir)
		git := func(string) (string, error) { return dir, nil }
		_, err := Resolve(dir, "/nonexistent-home", nil, git)
		if err == nil || !strings.Contains(err.Error(), `":"`) {
			t.Errorf("%s: err = %v", rel, err)
		}
	}
}

func TestCheckSafeIgnoresCase(t *testing.T) {
	// macOS の APFS は大文字小文字を区別しないので、/users/k は /Users/k と同じ場所
	for _, root := range []string{"/users", "/USERS", "/users/k", "/Users/K", "/HOME"} {
		if err := CheckSafe(root, "/Users/k"); err == nil {
			t.Errorf("CheckSafe(%q) が通ってしまう", root)
		}
	}
}

func TestCheckSafeSensitiveDirs(t *testing.T) {
	home := "/Users/k"
	for _, root := range []string{
		"/Users/k/.ssh", "/Users/k/.aws/x", "/Users/k/.gnupg", "/Users/k/.docker",
		"/Users/k/.config", "/Users/k/.config/nvim", "/Users/k/Library/Application Support/x",
		"/Users/k/.CONFIG",
	} {
		if err := CheckSafe(root, home); err == nil {
			t.Errorf("CheckSafe(%q) が通ってしまう", root)
		}
	}
	// 追加で渡した場所（sigil の状態フォルダなど）の中も、それを含むフォルダも拒否する
	extra := "/data/state/sigil"
	for _, root := range []string{"/data/state/sigil/approved", "/data/state", "/data"} {
		if err := CheckSafe(root, home, extra); err == nil {
			t.Errorf("CheckSafe(%q, extra) が通ってしまう", root)
		}
	}
	if err := CheckSafe("/data/other", home, extra); err != nil {
		t.Errorf("CheckSafe(/data/other) = %v", err)
	}
}

func TestResolveRejectsCaseVariantOfHome(t *testing.T) {
	base := realTemp(t)
	home := filepath.Join(base, "Home")
	mustMkdir(t, home)
	lower := filepath.Join(base, "home") // 大文字小文字を区別しないファイルシステムでは同じ場所
	noGit := func(string) (string, error) { return "", errors.New("no") }
	if _, err := Resolve(lower, home, nil, noGit); err == nil {
		t.Error("大文字小文字を変えたホームが通ってしまう")
	}
}

func TestResolveRejectsFolderOfRepos(t *testing.T) {
	base := realTemp(t)
	mustMkdir(t, filepath.Join(base, "src", "repo-a", ".git"))
	mustMkdir(t, filepath.Join(base, "src", "notes"))
	noGit := func(string) (string, error) { return "", errors.New("not a git repo") }
	_, err := Resolve(filepath.Join(base, "src"), "/nonexistent-home", nil, noGit)
	if err == nil || !strings.Contains(err.Error(), "repo-a") {
		t.Errorf("git ではないフォルダの中のリポジトリを拒否しない: %v", err)
	}
	// git リポジトリの中なら、入れ子のリポジトリがあっても使える（モノレポなど）
	git := func(string) (string, error) { return filepath.Join(base, "src"), nil }
	if _, err := Resolve(filepath.Join(base, "src"), "/nonexistent-home", nil, git); err != nil {
		t.Errorf("git リポジトリなのに拒否した: %v", err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestResolveFollowsSymlinks(t *testing.T) {
	base := realTemp(t)
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	noGit := func(string) (string, error) { return "", errors.New("not a git repo") }

	p, err := Resolve(filepath.Join(link, "sub"), "/nonexistent-home", nil, noGit)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != filepath.Join(real, "sub") {
		t.Errorf("Root = %q", p.Root)
	}
}

func TestResolveUsesGitTopTrimmed(t *testing.T) {
	base := realTemp(t)
	sub := filepath.Join(base, "web", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	var gotDir string
	git := func(dir string) (string, error) { gotDir = dir; return base + "\n", nil }

	p, err := Resolve(sub, "/nonexistent-home", nil, git)
	if err != nil {
		t.Fatal(err)
	}
	if gotDir != sub {
		t.Errorf("git に渡したフォルダ = %q", gotDir)
	}
	if p.Root != base {
		t.Errorf("Root = %q, want %q", p.Root, base)
	}
}

func TestResolveRejectsHome(t *testing.T) {
	home := realTemp(t)
	noGit := func(string) (string, error) { return "", errors.New("no") }
	if _, err := Resolve(home, home, nil, noGit); err == nil {
		t.Error("ホームそのものが通ってしまう")
	}
	if _, err := Resolve(filepath.Dir(home), home, nil, noGit); err == nil {
		t.Error("ホームを含むフォルダが通ってしまう")
	}
}
