package secret

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeHome は、一時フォルダに偽のホームと Library/Keychains を作る。
// macOS では /var が /private/var へのリンクなので、解いた結果を返す。
func fakeHome(t *testing.T) (home, kcDir string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(root, "home")
	kcDir = filepath.Join(home, "Library", "Keychains")
	if err := os.MkdirAll(kcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return home, kcDir
}

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckKeychainPathOK(t *testing.T) {
	home, kc := fakeHome(t)
	p := filepath.Join(kc, "s.keychain-db")
	writeFile(t, p)
	got, err := CheckKeychainPath(p, home, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Errorf("got %q, want %q", got, p)
	}
}

func TestCheckKeychainPathSymlinkHome(t *testing.T) {
	home, kc := fakeHome(t)
	p := filepath.Join(kc, "s.keychain-db")
	writeFile(t, p)
	link := filepath.Join(filepath.Dir(home), "link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	got, err := CheckKeychainPath(filepath.Join(link, "Library", "Keychains", "s.keychain-db"), link, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Errorf("got %q, want %q", got, p)
	}
}

func TestCheckKeychainPathRejects(t *testing.T) {
	home, kc := fakeHome(t)
	outside := filepath.Join(filepath.Dir(home), "outside.keychain-db")
	writeFile(t, outside)

	sub := filepath.Join(kc, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(sub, "s.keychain-db"))
	writeFile(t, filepath.Join(kc, "login.keychain-db"))
	writeFile(t, filepath.Join(kc, "login.keychain"))
	escape := filepath.Join(kc, "escape.keychain-db")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(kc, "dir.keychain-db"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(kc, "ok.keychain-db"))

	cases := map[string]struct {
		path string
		uid  int
	}{
		"サブフォルダの下":          {filepath.Join(sub, "s.keychain-db"), os.Getuid()},
		"login.keychain-db": {filepath.Join(kc, "login.keychain-db"), os.Getuid()},
		"login.keychain":    {filepath.Join(kc, "login.keychain"), os.Getuid()},
		"ホームの外":             {outside, os.Getuid()},
		"外へのリンク":            {escape, os.Getuid()},
		"フォルダ":              {filepath.Join(kc, "dir.keychain-db"), os.Getuid()},
		"存在しない":             {filepath.Join(kc, "none.keychain-db"), os.Getuid()},
		"uid が違う":           {filepath.Join(kc, "ok.keychain-db"), os.Getuid() + 1},
		"相対パス":              {"Library/Keychains/ok.keychain-db", os.Getuid()},
		".. で外へ出る":          {kc + "/../../../outside.keychain-db", os.Getuid()},
		".. でサブへ入る":         {kc + "/sub/../sub/s.keychain-db", os.Getuid()},
	}
	for name, c := range cases {
		if got, err := CheckKeychainPath(c.path, home, c.uid); err == nil {
			t.Errorf("%s: エラーになるべき（resolved=%q）", name, got)
		}
	}
}

func TestCheckKeychainPathResolvesLink(t *testing.T) {
	home, kc := fakeHome(t)
	b := filepath.Join(kc, "b.keychain-db")
	writeFile(t, b)
	a := filepath.Join(kc, "a.keychain-db")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	got, err := CheckKeychainPath(a, home, os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	if got != b {
		t.Errorf("got %q, want %q", got, b)
	}
}

func TestSecurityArgs(t *testing.T) {
	got := SecurityArgs("wakatime", "/k")
	want := []string{"find-generic-password", "-s", "wakatime", "-w", "/k"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSecret(t *testing.T) {
	ok := map[string]string{
		"v\n":   "v",
		"v\n\n": "v\n",
		"v\r\n": "v\r",
		"v":     "v",
	}
	for in, want := range ok {
		got, err := ParseSecret([]byte(in))
		if err != nil || got != want {
			t.Errorf("ParseSecret(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseSecret([]byte("a\x00b")); err == nil {
		t.Error("NUL はエラーになるべき")
	} else if strings.Contains(err.Error(), "a") && strings.Contains(err.Error(), "b\n") {
		t.Errorf("エラーに値が含まれている: %v", err)
	}
	big := []byte(strings.Repeat("Z", MaxSecretSize+1))
	if _, err := ParseSecret(big); err == nil {
		t.Error("大きすぎる値はエラーになるべき")
	} else if strings.Contains(err.Error(), "ZZZ") {
		t.Errorf("エラーに値が含まれている: %v", err)
	}
	if _, err := ParseSecret([]byte(strings.Repeat("Z", MaxSecretSize))); err != nil {
		t.Errorf("上限ちょうどは通るべき: %v", err)
	}
}

func TestCleanStderr(t *testing.T) {
	in := "security: \x1b[31mSecKeychainSearch\u202e failed\nline2"
	got := CleanStderr([]byte(in))
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0x202e) {
		t.Errorf("制御文字が残っている: %q", got)
	}
	if strings.ContainsAny(got, "\n\t") || !strings.Contains(got, "failed line2") {
		t.Errorf("改行が空白になっていない: %q", got)
	}
	long := CleanStderr([]byte(strings.Repeat("あ", 300)))
	if utf8.RuneCountInString(long) != 201 || !strings.HasSuffix(long, "…") {
		t.Errorf("200 文字 + … になっていない: %d", utf8.RuneCountInString(long))
	}
}
