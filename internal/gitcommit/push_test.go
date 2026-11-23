package gitcommit

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newBareOrigin は一時フォルダに bare リポジトリを作り、repo の origin にする。
func newBareOrigin(t *testing.T, g Git) (string, Git) {
	t.Helper()
	bare := t.TempDir()
	bg := Git{R: ExecRunner{}, Root: bare, Env: g.Env}
	if out, err := exec.Command("git", "init", "--bare", "-q", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := g.Out(nil, "remote", "add", "origin", bare); err != nil {
		t.Fatal(err)
	}
	return bare, bg
}

func TestFindUpstreamNone(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a\n")
	commitAll(t, g, "first")
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := FindUpstream(g, r)
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestFindUpstreamLocalDot(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a\n")
	commitAll(t, g, "first")
	for _, a := range [][]string{
		{"branch", "other"},
		{"config", "branch.main.remote", "."},
		{"config", "branch.main.merge", "refs/heads/other"},
	} {
		if _, err := g.Out(nil, a...); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := Inspect(g)
	if _, ok, err := FindUpstream(g, r); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestPushToBare(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a\n")
	commitAll(t, g, "first")
	bare, bg := newBareOrigin(t, g)
	if _, err := g.Out(nil, "push", "-q", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	write(t, root, "a.txt", "b\n")
	commitAll(t, g, "second")

	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	u, ok, err := FindUpstream(g, r)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if u.Ahead != 1 || u.MergeRef != "refs/heads/main" || u.Remote != "origin" || u.URL != bare {
		t.Fatalf("%+v", u)
	}
	in := u.PrePushInput(r, r.Head)
	want := "refs/heads/main " + r.Head + " refs/heads/main " + u.RemoteSha + "\n"
	if in != want {
		t.Fatalf("got %q want %q", in, want)
	}

	var so, se bytes.Buffer
	if err := Push(g, u, r.Head, &so, &se); err != nil {
		t.Fatal(err, se.String())
	}
	got, err := bg.Out(nil, "rev-parse", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	head, _ := g.Out(nil, "rev-parse", "HEAD")
	if strings.TrimSpace(got) != strings.TrimSpace(head) {
		t.Fatalf("bare=%s head=%s", got, head)
	}
}

func TestFindUpstreamNoRemoteTrackingRef(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a\n")
	commitAll(t, g, "first")
	newBareOrigin(t, g)
	// 追跡 ref のない upstream 設定。RemoteSha は 0 の並びで、Ahead は全コミット数。
	for _, a := range [][]string{
		{"config", "branch.main.remote", "origin"},
		{"config", "branch.main.merge", "refs/heads/main"},
	} {
		if _, err := g.Out(nil, a...); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := Inspect(g)
	u, ok, err := FindUpstream(g, r)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if u.RemoteSha != strings.Repeat("0", len(r.Head)) || u.Ahead != 1 {
		t.Fatalf("%+v", u)
	}
}

func TestPushSkipsPrePushHook(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a\n")
	commitAll(t, g, "first")
	newBareOrigin(t, g)
	if _, err := g.Out(nil, "push", "-q", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	// 印はリポジトリの外に置く。絶対パスをスクリプトに書く。
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntouch '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// SafeConfig の -c が .git/config より優先されることを確かめる設定。
	if _, err := g.Out(nil, "config", "core.hooksPath", hooks); err != nil {
		t.Fatal(err)
	}

	write(t, root, "a.txt", "b\n")
	commitAll(t, g, "second")
	r, _ := Inspect(g)
	u, ok, err := FindUpstream(g, r)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}

	// 対照: SafeConfig なしの素の git push ならフックが動く。テストが有効であることの証明。
	ctl := exec.Command("git", "-C", root, "push", "-q", "origin", "HEAD:refs/heads/control")
	ctl.Env = append(os.Environ(), g.Env...)
	if out, err := ctl.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("対照でフックが動いていません: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	var so, se bytes.Buffer
	if err := Push(g, u, r.Head, &so, &se); err != nil {
		t.Fatal(err, se.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("pre-push フックが実行されました")
	}
}

// push するのは刻印したコミットで、そのあとに HEAD が動いても変わらない。Ahead も刻印したコミットで数える。
func TestPushSealedCommitNotHEAD(t *testing.T) {
	root, g := newRepo(t)
	write(t, root, "a.txt", "a\n")
	commitAll(t, g, "first")
	_, bg := newBareOrigin(t, g)
	if _, err := g.Out(nil, "push", "-q", "-u", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	write(t, root, "a.txt", "b\n")
	commitAll(t, g, "sealed")
	r, err := Inspect(g)
	if err != nil {
		t.Fatal(err)
	}
	sealed := r.Head
	// 刻印のあと、利用者がホストで別のコミットを足した
	write(t, root, "a.txt", "c\n")
	commitAll(t, g, "later")

	u, ok, err := FindUpstream(g, r)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if u.Ahead != 1 {
		t.Errorf("Ahead=%d", u.Ahead)
	}
	var so, se bytes.Buffer
	if err := Push(g, u, sealed, &so, &se); err != nil {
		t.Fatal(err, se.String())
	}
	got, err := bg.Out(nil, "rev-parse", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != sealed {
		t.Errorf("bare=%s sealed=%s", got, sealed)
	}
}

// pushRecorder は git の呼び出しを記録するだけの偽物。
type pushRecorder struct{ args []string }

func (p *pushRecorder) Run(_ string, args []string, _ RunOpts) error {
	p.args = append([]string(nil), args...)
	return nil
}

// push はサブモジュールに降りない（入れ子のリポジトリの設定を読まない）。
func TestPushArgs(t *testing.T) {
	rec := &pushRecorder{}
	g := Git{R: rec, Root: "/r"}
	u := Upstream{Remote: "origin", MergeRef: "refs/heads/main"}
	if err := Push(g, u, "abc123", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rec.args, " ")
	if !strings.HasSuffix(got, " push --no-verify --recurse-submodules=no origin abc123:refs/heads/main") {
		t.Errorf("args = %q", got)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://user:pass@github.com/o/r.git": "https://github.com/o/r.git",
		"https://TOKEN@github.com/o/r.git":     "https://github.com/o/r.git",
		"https://a:b@c@host:8443/x?q=1#f":      "https://host:8443/x?q=1#f",
		"ssh://git@host/x":                     "ssh://host/x",
		"git@github.com:o/r.git":               "git@github.com:o/r.git",
		"/srv/git/r.git":                       "/srv/git/r.git",
		"./a@b/r.git":                          "./a@b/r.git",
		"https://github.com/o/r.git":           "https://github.com/o/r.git",
		"https://host/path/with@at":            "https://host/path/with@at",
		"":                                     "",
		"://x@y":                               "://x@y",
		"1http://u@h/":                         "1http://u@h/",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
