package main

import (
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 本物の関数（realSys・toolVersion・runSafe）のうち、一時フォルダと手元のサーバーだけで確かめられるもの。
// 本物の HOME・/proc・api.github.com には触れない。

func TestRunSafeHidesPanic(t *testing.T) {
	s := newFake().sys()
	s.user = func() (int, string) { panic("github_pat_SECRETINPANIC") }
	var b strings.Builder
	if code := runSafe(&b, s, false); code != 1 {
		t.Errorf("panic のときの終了コード = %d, want 1", code)
	}
	if !strings.Contains(b.String(), "点検の途中で想定外のエラーが起きた") {
		t.Errorf("panic の知らせがない: %q", b.String())
	}
	if strings.Contains(b.String(), "SECRET") || strings.Contains(b.String(), "goroutine") {
		t.Errorf("panic の値やスタックトレースが出た: %q", b.String())
	}
}

func TestRunSafeNormal(t *testing.T) {
	var b strings.Builder
	if code := runSafe(&b, newFake().sys(), false); code != 0 {
		t.Errorf("終了コード = %d, want 0", code)
	}
}

func TestColorEnabledRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if colorEnabled() {
		t.Error("NO_COLOR があるのに色を付ける")
	}
}

func TestLimitedBuffer(t *testing.T) {
	var l limitedBuffer
	big := strings.Repeat("a", outLimit-10)
	if n, err := l.Write([]byte(big)); n != len(big) || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if n, err := l.Write([]byte(strings.Repeat("b", 100))); n != 100 || err != nil {
		t.Fatalf("上限を超える Write = %d, %v（捨てても書けたことにする）", n, err)
	}
	l.Write([]byte("c"))
	if len(l.b) != outLimit || !strings.HasSuffix(string(l.b), strings.Repeat("b", 10)) {
		t.Errorf("上限で切れていない: len=%d", len(l.b))
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("シェルのスクリプトを使う")
	}
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestToolVersionEnvAndDir(t *testing.T) {
	t.Setenv("GH_TOKEN", "github_pat_should_not_pass")
	t.Setenv("HOME", "/home/test")
	p := writeScript(t, `echo "args=$* pwd=$(pwd) tok=${GH_TOKEN:-none} home=$HOME upd=$DISABLE_AUTOUPDATER tc=$GOTOOLCHAIN"
echo second line
`)
	got := toolVersion(p, "rg")
	want := "args=--version pwd=/ tok=none home=/home/test upd=1 tc=local"
	if got != want {
		t.Errorf("toolVersion = %q\nwant         %q", got, want)
	}
}

func TestToolVersionArgsPerTool(t *testing.T) {
	p := writeScript(t, `echo "out $*"; echo "err $*" >&2
`)
	if got := toolVersion(p, "go"); got != "out version" {
		t.Errorf("go: %q", got)
	}
	// lua5.1 は標準エラーに版を出すので、標準エラーも読む
	pl := writeScript(t, `echo "Lua 5.1.5 $*" >&2
`)
	if got := toolVersion(pl, "lua5.1"); got != "Lua 5.1.5 -v" {
		t.Errorf("lua5.1: %q", got)
	}
	// ほかのツールの標準エラーは読まない
	pe := writeScript(t, `echo "noise" >&2
`)
	if got := toolVersion(pe, "rg"); got != "" {
		t.Errorf("標準エラーを読んだ: %q", got)
	}
}

func TestToolVersionTimeout(t *testing.T) {
	old := cmdTimeout
	cmdTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cmdTimeout = old })
	p := writeScript(t, "exec sleep 30\n")
	start := time.Now()
	toolVersion(p, "rg")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("打ち切られなかった: %v", d)
	}
}

func TestToolVersionOutputLimit(t *testing.T) {
	p := writeScript(t, "head -c 100000 /dev/zero | tr '\\0' a\n")
	if got := toolVersion(p, "rg"); len(got) != outLimit {
		t.Errorf("出力の長さ = %d, want %d", len(got), outLimit)
	}
}

func TestRealRateLimit(t *testing.T) {
	var gotAuth string
	srv, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	})
	ca := filepath.Join(t.TempDir(), "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	oldURL, oldCA := rateLimitURL, caFile
	t.Cleanup(func() { rateLimitURL, caFile = oldURL, oldCA })
	rateLimitURL, caFile = srv.URL+"/rate_limit", ca

	status, _, err := realSys().rateLimit("github_pat_x")
	if err != nil || status != http.StatusUnauthorized || gotAuth != "Bearer github_pat_x" {
		t.Errorf("status=%d err=%v auth=%q", status, err, gotAuth)
	}

	caFile = filepath.Join(t.TempDir(), "none.crt")
	if _, _, err := realSys().rateLimit("github_pat_x"); err == nil {
		t.Error("ルート証明書がないのに問い合わせた")
	}
}

func TestRealSysFileHelpers(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(f, link); err != nil {
		t.Fatal(err)
	}
	s := realSys()
	if m, err := s.lstat(link); err != nil || m&os.ModeSymlink == 0 {
		t.Errorf("lstat がリンクをたどった: %v %v", m, err)
	}
	if _, err := s.lstat(filepath.Join(dir, "none")); !notExist(err) {
		t.Errorf("ないものの lstat = %v", err)
	}
	if !s.isDir(dir) || s.isDir(f) {
		t.Error("isDir が違う")
	}
	want, _ := filepath.EvalSymlinks(f)
	if got := s.resolve(link); got != want {
		t.Errorf("resolve = %q, want %q", got, want)
	}
	if got := s.resolve(filepath.Join(dir, "none")); got != filepath.Join(dir, "none") {
		t.Errorf("解決できないときはそのまま: %q", got)
	}
	if uid, name := s.user(); uid != os.Getuid() || name == "" {
		t.Errorf("user = %d %q", uid, name)
	}
	if _, err := s.lookPath("sh"); err != nil {
		t.Errorf("lookPath(sh) = %v", err)
	}
	if s.now().IsZero() {
		t.Error("now がゼロ")
	}
}

func TestFirstLineNoNewline(t *testing.T) {
	if got := firstLine("abc"); got != "abc" {
		t.Errorf("firstLine = %q", got)
	}
}
