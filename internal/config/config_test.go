package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const file = "/p/.sigil"

func publishes(c Config) []string {
	var out []string
	for _, p := range c.Ports {
		out = append(out, p.Publish())
	}
	return out
}

func TestParseValid(t *testing.T) {
	inputs := map[string]string{
		"LF":           "# c\nenv DATABASE_URL # x\n\nport 5173\nport 8080:80\n",
		"CRLF":         "# c\r\nenv DATABASE_URL # x\r\n\r\nport 5173\r\nport 8080:80\r\n",
		"タブ区切り":        "# c\nenv\tDATABASE_URL\t# x\n\nport\t5173\n\tport 8080:80\n",
		"末尾改行なし":       "# c\nenv DATABASE_URL # x\n\nport 5173\nport 8080:80",
		"コメントだけの行や空白行": "   \n  # only comment\nenv DATABASE_URL\nport 5173\nport 8080:80\n",
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			c, err := Parse(file, strings.NewReader(in))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.Envs, []string{"DATABASE_URL"}) {
				t.Errorf("Envs = %v", c.Envs)
			}
			want := []string{"127.0.0.1:5173:5173", "127.0.0.1:8080:80"}
			if got := publishes(c); !reflect.DeepEqual(got, want) {
				t.Errorf("Ports = %v, want %v", got, want)
			}
		})
	}
}

func TestParseStripsBOM(t *testing.T) {
	// Windows のエディタなどで保存すると、先頭に UTF-8 の BOM が付くことがある
	c, err := Parse(file, strings.NewReader("\xef\xbb\xbfenv A\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Envs, []string{"A"}) {
		t.Errorf("Envs = %v", c.Envs)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"env\n", "/p/.sigil:1: 値がありません: env"},
		{"env A B\n", "/p/.sigil:1: 余分な値があります: env A B"},
		{"env $(id)\n", "/p/.sigil:1: 環境変数名が不正です: $(id)"},
		{"env `id`\n", "/p/.sigil:1: 環境変数名が不正です: `id`"},
		{"env 1A\n", "/p/.sigil:1: 環境変数名が不正です: 1A"},
		{"env A=1\n", "/p/.sigil:1: 環境変数名が不正です: A=1"},
		{"env PATH\n", "/p/.sigil:1: PATH は渡せません"},
		{"env SSH_AUTH_SOCK\n", "/p/.sigil:1: SSH_AUTH_SOCK は渡せません"},
		{"env DOCKER_HOST\n", "/p/.sigil:1: DOCKER_HOST は渡せません"},
		{"env HOME\n", "/p/.sigil:1: HOME は渡せません"},
		{"env LD_PRELOAD\n", "/p/.sigil:1: LD_PRELOAD は渡せません"},
		{"env LD_LIBRARY_PATH\n", "/p/.sigil:1: LD_LIBRARY_PATH は渡せません"},
		// sigil 自身や docker / git / op の動作を変えられる名前（前方一致）
		{"env DOCKER_CONTEXT\n", "/p/.sigil:1: DOCKER_CONTEXT は渡せません"},
		{"env XDG_CONFIG_HOME\n", "/p/.sigil:1: XDG_CONFIG_HOME は渡せません"},
		{"env SIGIL_INNER\n", "/p/.sigil:1: SIGIL_INNER は渡せません"},
		{"env GIT_DIR\n", "/p/.sigil:1: GIT_DIR は渡せません"},
		{"env OP_SESSION_x\n", "/p/.sigil:1: OP_SESSION_x は渡せません"},
		{"env DYLD_INSERT_LIBRARIES\n", "/p/.sigil:1: DYLD_INSERT_LIBRARIES は渡せません"},
		{"env LD_AUDIT\n", "/p/.sigil:1: LD_AUDIT は渡せません"},
		{"port 51 73\n", "/p/.sigil:1: 余分な値があります: port 51 73"},
		{"port abc\n", "/p/.sigil:1: ポート指定が不正です: abc"},
		{"port 123456\n", "/p/.sigil:1: ポート指定が不正です: 123456"},
		{"port 80:\n", "/p/.sigil:1: ポート指定が不正です: 80:"},
		{"port 0.0.0.0:80:80\n", "/p/.sigil:1: ポート指定が不正です: 0.0.0.0:80:80"},
		{"port 0\n", "/p/.sigil:1: ポート番号は 1〜65535 です: 0"},
		{"port 70000\n", "/p/.sigil:1: ポート番号は 1〜65535 です: 70000"},
		{"port 8080:0\n", "/p/.sigil:1: ポート番号は 1〜65535 です: 8080:0"},
		{"run rm -rf /\n", "/p/.sigil:1: 余分な値があります: run rm -rf /"},
		{"run x\n", "/p/.sigil:1: 不明な指定です: run（env / port / keychain / ro / ro_root のみ）"},
		{"# ok\nenv OK\nenv $(id)\n", "/p/.sigil:3: 環境変数名が不正です: $(id)"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, err := Parse(file, strings.NewReader(tc.in))
			if err == nil {
				t.Fatalf("エラーにならない")
			}
			if err.Error() != tc.want {
				t.Errorf("err = %q\nwant  %q", err.Error(), tc.want)
			}
		})
	}
}

func TestDenied(t *testing.T) {
	for _, n := range []string{"PATH", "HOME", "SSH_AUTH_SOCK", "DOCKER_HOST", "XDG_STATE_HOME", "SIGIL_IMAGE", "GIT_SSH_COMMAND", "OP_ACCOUNT", "LD_PRELOAD", "DYLD_LIBRARY_PATH"} {
		if !Denied(n) {
			t.Errorf("Denied(%q) = false", n)
		}
	}
	for _, n := range []string{"SIGIL", "SIGIL_INNER", "SIGIL_CONTEXT", "SIGIL_PROJECT_DIR", "SIGIL_OP_TMP", "SIGIL_PLAIN", "SIGIL_GLYPHS"} {
		if !Denied(n) {
			t.Errorf("Denied(%q) = false", n)
		}
	}
	// sigil の動作に関係しない SIGIL_ の名前は渡せる（設計書の検証手順でも SIGIL_TEST_VAR を使う）
	for _, n := range []string{"DATABASE_URL", "GITHUB_TOKEN", "DOCKERFILE_X", "PATHS", "MY_HOME", "SIGIL_TEST_VAR"} {
		if Denied(n) {
			t.Errorf("Denied(%q) = true", n)
		}
	}
}

func TestLoadRejectsSymlinkWhenStrict(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "netrc")
	os.WriteFile(secret, []byte("machine github.com login x password y\n"), 0o600)
	link := filepath.Join(dir, ".sigil")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadStrict(link)
	if err == nil {
		t.Fatal("シンボリックリンクを読んだ")
	}
	if strings.Contains(err.Error(), "password") {
		t.Errorf("リンク先の中身がエラーに出た: %v", err)
	}

	// 全体設定（ホストのファイル）は dotfiles の管理でリンクにされることがあるので、Load はたどる
	os.WriteFile(secret, []byte("env X\n"), 0o600)
	if c, ok, err := Load(link); err != nil || !ok || len(c.Envs) != 1 {
		t.Errorf("Load がリンクをたどらない: %v %v %v", c, ok, err)
	}
}

func TestLoadRejectsHugeAndSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, ".sigil")
	os.WriteFile(big, []byte(strings.Repeat("# x\n", 40000)), 0o644)
	if _, _, err := LoadStrict(big); err == nil {
		t.Error("大きすぎるファイルを読んだ")
	}
	if _, _, err := LoadStrict(dir); err == nil {
		t.Error("フォルダを読んだ")
	}
}

func TestMerge(t *testing.T) {
	a := Config{Envs: []string{"A", "B"}, Ports: []Port{{"1", "1"}}}
	b := Config{Envs: []string{"B", "C"}, Ports: []Port{{"2", "2"}}}
	got := Merge(a, b)
	if !reflect.DeepEqual(got.Envs, []string{"A", "B", "C"}) {
		t.Errorf("Envs = %v", got.Envs)
	}
	if !reflect.DeepEqual(got.Ports, []Port{{"1", "1"}, {"2", "2"}}) {
		t.Errorf("Ports = %v", got.Ports)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	c, ok, err := Load(filepath.Join(dir, "missing"))
	if err != nil || ok || len(c.Envs) != 0 {
		t.Errorf("ファイルがないとき = %v %v %v", c, ok, err)
	}

	p := filepath.Join(dir, ".sigil")
	if err := os.WriteFile(p, []byte("env X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, ok, err = Load(p)
	if err != nil || !ok || !reflect.DeepEqual(c.Envs, []string{"X"}) {
		t.Errorf("Load = %v %v %v", c, ok, err)
	}

	if err := os.WriteFile(p, []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(p); err == nil || !strings.HasPrefix(err.Error(), p+":1: ") {
		t.Errorf("エラーにファイル名と行番号がない: %v", err)
	}
}

func TestParseKeychain(t *testing.T) {
	c, err := Parse("/g/config", strings.NewReader("keychain ~/Library/Keychains/s.keychain-db\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Keychain != "~/Library/Keychains/s.keychain-db" {
		t.Errorf("Keychain = %q", c.Keychain)
	}
}

func TestParseKeychainTwice(t *testing.T) {
	_, err := Parse("/g/config", strings.NewReader("keychain /a\nkeychain /b\n"))
	if err == nil || !strings.Contains(err.Error(), "/g/config:2: keychain が 2 回書かれています") {
		t.Errorf("err = %v", err)
	}
}

func TestParseProjectRejectsKeychain(t *testing.T) {
	_, err := ParseProject("/p/.sigil", strings.NewReader("env A\nkeychain /x\n"))
	if err == nil || !strings.Contains(err.Error(), ":2: keychain は全体設定") {
		t.Errorf("err = %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	for in, want := range map[string]string{"~/a": "/h/a", "/abs": "/abs"} {
		got, err := ExpandHome(in, "/h")
		if err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"~", "~bob/a", "rel/a", ""} {
		if got, err := ExpandHome(in, "/h"); err == nil {
			t.Errorf("ExpandHome(%q) = %q, エラーにならない", in, got)
		}
	}
}

func TestParseProjectReadOnly(t *testing.T) {
	c, err := ParseProject("/p/.sigil", strings.NewReader("env A\nro ~/src/lib\n\nro /opt/other # c\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []ReadOnly{{Path: "~/src/lib", Line: 2}, {Path: "/opt/other", Line: 4}}
	if !reflect.DeepEqual(c.ReadOnly, want) {
		t.Errorf("ReadOnly = %v, want %v", c.ReadOnly, want)
	}
}

func TestParseReadOnlyErrors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ro\n", "/p/.sigil:1: 値がありません: ro"},
		{"ro ~/a ~/b\n", "/p/.sigil:1: 余分な値があります: ro ~/a ~/b"},
	}
	for _, c := range cases {
		_, err := ParseProject("/p/.sigil", strings.NewReader(c.in))
		if err == nil || err.Error() != c.want {
			t.Errorf("%q: err = %v, want %q", c.in, err, c.want)
		}
	}
}

func TestParseGlobalRejectsReadOnly(t *testing.T) {
	// 全体設定に書くと、すべてのプロジェクトのコードからそのフォルダが読めてしまう
	_, err := Parse("/g/config", strings.NewReader("env A\nro ~/src/lib\n"))
	if err == nil || !strings.Contains(err.Error(), "/g/config:2: ro はプロジェクトの .sigil でだけ書けます") {
		t.Errorf("err = %v", err)
	}
}

func TestParseReadOnlyRoots(t *testing.T) {
	c, err := Parse("/g/config", strings.NewReader("ro_root ~/src\nro_root /opt/src\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []ReadOnly{{Path: "~/src", Line: 1}, {Path: "/opt/src", Line: 2}}
	if !reflect.DeepEqual(c.ReadOnlyRoots, want) {
		t.Errorf("ReadOnlyRoots = %v, want %v", c.ReadOnlyRoots, want)
	}
}

func TestParseProjectRejectsReadOnlyRoot(t *testing.T) {
	// マウントしてよい範囲をリポジトリに決めさせない
	_, err := ParseProject("/p/.sigil", strings.NewReader("ro_root /\n"))
	if err == nil || !strings.Contains(err.Error(), "/p/.sigil:1: ro_root は全体設定") {
		t.Errorf("err = %v", err)
	}
}

func TestParseRejectsNUL(t *testing.T) {
	// 承認のハッシュは NUL で区切るので、.sigil に NUL を入れて区切りをずらさせない
	for _, in := range []string{"env A\n# x\x00ro\x00\n", "env A\x00\n"} {
		_, err := ParseProject("/p/.sigil", strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), "NUL") {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

func TestMergeReadOnly(t *testing.T) {
	// ro はプロジェクトの分、ro_root は全体の分。どちらも Merge で失われない
	p := Config{ReadOnly: []ReadOnly{{Path: "~/src/a", Line: 1}}}
	g := Config{ReadOnlyRoots: []ReadOnly{{Path: "~/src", Line: 3}}}
	m := Merge(p, g)
	if len(m.ReadOnly) != 1 || len(m.ReadOnlyRoots) != 1 {
		t.Errorf("Merge = %+v", m)
	}
}
