package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// claudeEnv は、一時フォルダにホームと sigil の設定フォルダを作る。
func claudeEnv(t *testing.T) (home, sigilConfig, dir string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(base, "home")
	sigilConfig = filepath.Join(home, ".config", "sigil")
	dir = filepath.Join(sigilConfig, "claude")
	mkdirMode(t, dir, 0o755)
	return home, sigilConfig, dir
}

func mkdirMode(t *testing.T, p string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func writeMode(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeSharedMissing(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	got, w, err := ResolveClaudeShared(dir, home, sc)
	if got != "" || w != nil || err != nil {
		t.Errorf("(%q, %v, %v)", got, w, err)
	}
}

func TestClaudeSharedEmptyDir(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	got, _, err := ResolveClaudeShared(dir, home, sc)
	if err != nil || got != dir {
		t.Errorf("(%q, %v), want %q", got, err, dir)
	}
}

func TestClaudeSharedOK(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	writeMode(t, filepath.Join(dir, "CLAUDE.md"), "x", 0o644)
	writeMode(t, filepath.Join(dir, ".claude", "skills", "x", "SKILL.md"), "x", 0o644)
	writeMode(t, filepath.Join(dir, "managed-settings.json"), "{}", 0o600)
	got, w, err := ResolveClaudeShared(dir, home, sc)
	if err != nil || got != dir || len(w) != 0 {
		t.Errorf("(%q, %v, %v)", got, w, err)
	}
}

func TestClaudeSharedRejects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string                                          // エラーに含まれるはずの文言（意図した理由で弾いたか）
		setup  func(t *testing.T, home, sc, dir string) string // 確かめる dir を返す
	}{
		{"フォルダがシンボリックリンク", "シンボリックリンク", func(t *testing.T, home, sc, dir string) string {
			real := filepath.Join(filepath.Dir(home), "elsewhere")
			mkdirMode(t, real, 0o755)
			os.Remove(dir)
			if err := os.Symlink(real, dir); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{"フォルダでない", "フォルダではありません", func(t *testing.T, home, sc, dir string) string {
			os.Remove(dir)
			writeMode(t, dir, "x", 0o644)
			return dir
		}},
		{"グループが書き込める", "書き込めます", func(t *testing.T, home, sc, dir string) string {
			mkdirMode(t, dir, 0o775)
			return dir
		}},
		{"ほかのユーザーが書き込める", "書き込めます", func(t *testing.T, home, sc, dir string) string {
			mkdirMode(t, dir, 0o757)
			return dir
		}},
		{"中のファイルが書き込める", "書き込めます", func(t *testing.T, home, sc, dir string) string {
			writeMode(t, filepath.Join(dir, "CLAUDE.md"), "x", 0o666)
			return dir
		}},
		{"中にシンボリックリンク", "中にシンボリックリンク", func(t *testing.T, home, sc, dir string) string {
			mkdirMode(t, filepath.Join(dir, ".claude", "skills"), 0o755)
			if err := os.Symlink("/home/dev/.claude/CLAUDE.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
		{"上の階層がホストの ~/.claude を指す", "守る場所", func(t *testing.T, home, sc, dir string) string {
			// sigil の設定フォルダを ~/.claude へのリンクにすると、claude は ~/.claude/claude になる
			hc := filepath.Join(home, ".claude")
			mkdirMode(t, filepath.Join(hc, "claude"), 0o755)
			os.RemoveAll(sc)
			if err := os.Symlink(hc, sc); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(sc, "claude")
		}},
		{"解決したパスに :", "\":\"", func(t *testing.T, home, sc, dir string) string {
			d := filepath.Join(sc, "a:b")
			mkdirMode(t, d, 0o755)
			return d
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, sc, dir := claudeEnv(t)
			d := tc.setup(t, home, sc, dir)
			got, _, err := ResolveClaudeShared(d, home, sc)
			if err == nil {
				t.Fatalf("エラーにならない: %q", got)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("理由が違う: %v", err)
			}
		})
	}
}

// dotfiles の構成（~/.config がリンク）でも、リンクの先の sigil/claude は使える。
// sigil の設定フォルダの外（~/.config/gh など）は見せない。
func TestClaudeSharedThroughConfigSymlink(t *testing.T) {
	home, _, _ := claudeEnv(t)
	cfg := filepath.Join(home, ".config")
	dot := filepath.Join(filepath.Dir(home), "dotfiles", "config")
	mkdirMode(t, filepath.Join(dot, "sigil", "claude"), 0o755)
	mkdirMode(t, filepath.Join(dot, "gh"), 0o755)
	os.RemoveAll(cfg)
	if err := os.Symlink(dot, cfg); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(cfg, "sigil")
	got, _, err := ResolveClaudeShared(filepath.Join(sc, "claude"), home, sc)
	if err != nil || got != filepath.Join(dot, "sigil", "claude") {
		t.Errorf("(%q, %v)", got, err)
	}

	// sigil の設定フォルダを ~/.config/gh へのリンクにした場合
	os.RemoveAll(filepath.Join(dot, "sigil"))
	mkdirMode(t, filepath.Join(dot, "gh", "claude"), 0o755)
	if err := os.Symlink(filepath.Join(dot, "gh"), filepath.Join(dot, "sigil")); err != nil {
		t.Fatal(err)
	}
	if got, _, err := ResolveClaudeShared(filepath.Join(sc, "claude"), home, sc); err == nil {
		t.Errorf("sigil の設定フォルダの外を通した: %q", got)
	}
}

func TestClaudeSharedHardlinkWarns(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	src := filepath.Join(filepath.Dir(home), "orig.md")
	writeMode(t, src, "x", 0o644)
	if err := os.Link(src, filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	_, w, err := ResolveClaudeShared(dir, home, sc)
	if err != nil || len(w) != 1 || w[0] != "CLAUDE.md" {
		t.Errorf("(%v, %v)", w, err)
	}
}

func TestClaudeSharedTooDeep(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	deep := dir
	for range 9 {
		deep = filepath.Join(deep, "d")
	}
	mkdirMode(t, deep, 0o755)
	if _, _, err := ResolveClaudeShared(dir, home, sc); err == nil {
		t.Error("深すぎるのにエラーにならない")
	}
}

func TestClaudeSharedUnprintable(t *testing.T) {
	home, sc, _ := claudeEnv(t)
	d := filepath.Join(sc, "c\x1b[31m")
	mkdirMode(t, d, 0o755)
	_, _, err := ResolveClaudeShared(d, home, sc)
	if err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("err = %v", err)
	}
}

func TestVerifyClaudeShared(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	got, _, err := ResolveClaudeShared(dir, home, sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyClaudeShared(got, home, sc); err != nil {
		t.Fatalf("変えていないのにエラー: %v", err)
	}

	// 途中（sigil の設定フォルダ）をリンクに差し替える
	moved := filepath.Join(filepath.Dir(home), "moved")
	if err := os.Rename(sc, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, sc); err != nil {
		t.Fatal(err)
	}
	if err := VerifyClaudeShared(got, home, sc); !errors.Is(err, ErrClaudeSharedChanged) {
		t.Errorf("リンクに差し替えたのに: %v", err)
	}
	os.Remove(sc)
	if err := VerifyClaudeShared(got, home, sc); err == nil {
		t.Error("消したのにエラーにならない")
	}
	if err := os.Rename(moved, sc); err != nil {
		t.Fatal(err)
	}
	mkdirMode(t, dir, 0o775)
	if err := VerifyClaudeShared(got, home, sc); err == nil {
		t.Error("g+w なのにエラーにならない")
	}
}

// 深さ 8 までは通す（設計書 22 章）。
func TestClaudeSharedDepth8OK(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	deep := dir
	for range 8 {
		deep = filepath.Join(deep, "d")
	}
	mkdirMode(t, deep, 0o755)
	if _, _, err := ResolveClaudeShared(dir, home, sc); err != nil {
		t.Errorf("深さ 8 で: %v", err)
	}
}

// 起動前の確かめ直しでも、":" のあるパスは弾く（作成時と同じ）。
func TestVerifyClaudeSharedColon(t *testing.T) {
	home, sc, _ := claudeEnv(t)
	d := filepath.Join(sc, "a:b")
	mkdirMode(t, d, 0o755)
	if err := VerifyClaudeShared(d, home, sc); err == nil || !strings.Contains(err.Error(), "\":\"") {
		t.Errorf("err = %v", err)
	}
}

// 作ったあとで中にリンクを足したら、起動の前の確かめ直しでも弾く。
func TestVerifyClaudeSharedWalksContents(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	got, _, err := ResolveClaudeShared(dir, home, sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/home/dev/.claude/CLAUDE.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := VerifyClaudeShared(got, home, sc); err == nil || !strings.Contains(err.Error(), "シンボリックリンク") {
		t.Errorf("err = %v", err)
	}
}

// 作ったあとで XDG_CONFIG_HOME を変えても（sigil の設定フォルダが別の場所になっても）、
// フォルダが動いていなければ起動できる。
func TestVerifyClaudeSharedAfterXDGChange(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	got, _, err := ResolveClaudeShared(dir, home, sc)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(home), "opt", "cfg", "sigil")
	if err := VerifyClaudeShared(got, home, other); err != nil {
		t.Errorf("XDG を変えただけで: %v", err)
	}
}

// ---- 確かめの分岐ごとのテスト（どの理由で弾いたかまで確かめる） ----

func expectErr(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("エラーにならない（%q を期待）", reason)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("理由が違う: %v（%q を期待）", err, reason)
	}
}

func baseDir(t *testing.T) string {
	t.Helper()
	b, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 親が読めず、フォルダを確かめられない。
func TestClaudeSharedLstatError(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	if err := os.Chmod(sc, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sc, 0o755) })
	_, _, err := ResolveClaudeShared(dir, home, sc)
	expectErr(t, err, "確かめられません")
}

// 上の階層のリンクを解決した先に、表示されない文字がある。
func TestClaudeSharedResolvedUnprintable(t *testing.T) {
	home, sc, _ := claudeEnv(t)
	odd := filepath.Join(filepath.Dir(home), "o\x1bdd")
	mkdirMode(t, filepath.Join(odd, "claude"), 0o755)
	link := filepath.Join(sc, "via")
	if err := os.Symlink(odd, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := ResolveClaudeShared(filepath.Join(link, "claude"), home, sc)
	if err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("err = %v", err)
	}
}

// 中のフォルダが読めない。
func TestClaudeSharedUnreadableSubdir(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	sub := filepath.Join(dir, ".claude")
	mkdirMode(t, sub, 0o000)
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	_, _, err := ResolveClaudeShared(dir, home, sc)
	expectErr(t, err, "読めません")
}

// 中に、ファイルでもフォルダでもないもの（FIFO）がある。
func TestClaudeSharedSpecialFile(t *testing.T) {
	home, sc, dir := claudeEnv(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := ResolveClaudeShared(dir, home, sc)
	expectErr(t, err, "ファイルでもフォルダでもない")
}

// 解決した先がホームを含む。
func TestClaudeSharedContainsHome(t *testing.T) {
	base := baseDir(t)
	home := filepath.Join(base, "c", "home")
	mkdirMode(t, home, 0o755)
	if err := os.Chmod(filepath.Join(base, "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(base, "s")
	if err := os.Symlink(base, sc); err != nil {
		t.Fatal(err)
	}
	_, _, err := ResolveClaudeShared(filepath.Join(sc, "c"), home, sc)
	expectErr(t, err, "ホームディレクトリを含む")
}

// 守る場所（~/.ssh）がリンクなら、その実体の中も守る。
func TestClaudeSharedProtectedSymlinkTarget(t *testing.T) {
	home, sc, _ := claudeEnv(t)
	real := filepath.Join(filepath.Dir(home), "ssh-real")
	mkdirMode(t, filepath.Join(real, "claude"), 0o755)
	if err := os.Symlink(real, filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sc, "via")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := ResolveClaudeShared(filepath.Join(link, "claude"), home, sc)
	expectErr(t, err, "守る場所")
}

// 解決した先が ~/.config の実体を含む。
func TestClaudeSharedContainsConfig(t *testing.T) {
	base := baseDir(t)
	home := filepath.Join(base, "home")
	mkdirMode(t, home, 0o755)
	realCfg := filepath.Join(base, "claude", "config")
	mkdirMode(t, realCfg, 0o755)
	if err := os.Chmod(filepath.Join(base, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realCfg, filepath.Join(home, ".config")); err != nil {
		t.Fatal(err)
	}
	sc := filepath.Join(base, "s")
	if err := os.Symlink(base, sc); err != nil {
		t.Fatal(err)
	}
	_, _, err := ResolveClaudeShared(filepath.Join(sc, "claude"), home, sc)
	expectErr(t, err, "を含むので")
}

// XDG_CONFIG_HOME が ~/.config の外なら、その下の sigil/claude を使える。
func TestClaudeSharedOutsideConfig(t *testing.T) {
	base := baseDir(t)
	home := filepath.Join(base, "home")
	mkdirMode(t, filepath.Join(home, ".config"), 0o755)
	sc := filepath.Join(base, "xdg", "sigil")
	dir := filepath.Join(sc, "claude")
	mkdirMode(t, dir, 0o755)
	got, _, err := ResolveClaudeShared(dir, home, sc)
	if err != nil || got != dir {
		t.Errorf("(%q, %v)", got, err)
	}
}

func TestVerifyClaudeSharedRejects(t *testing.T) {
	t.Run("記録が相対パス", func(t *testing.T) {
		home, sc, _ := claudeEnv(t)
		expectErr(t, VerifyClaudeShared("rel/claude", home, sc), "記録が不正")
	})
	t.Run("途中をたどれない", func(t *testing.T) {
		home, sc, dir := claudeEnv(t)
		if err := os.Chmod(sc, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(sc, 0o755) })
		expectErr(t, VerifyClaudeShared(dir, home, sc), "たどれません")
	})
	t.Run("フォルダではなくなった", func(t *testing.T) {
		home, sc, dir := claudeEnv(t)
		os.Remove(dir)
		writeMode(t, dir, "x", 0o644)
		err := VerifyClaudeShared(dir, home, sc)
		if !errors.Is(err, ErrClaudeSharedChanged) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("持ち主・権限", func(t *testing.T) {
		home, sc, dir := claudeEnv(t)
		mkdirMode(t, dir, 0o757)
		expectErr(t, VerifyClaudeShared(dir, home, sc), "書き込めます")
	})
	t.Run("守る場所", func(t *testing.T) {
		home, sc, _ := claudeEnv(t)
		d := filepath.Join(home, ".claude", "x")
		mkdirMode(t, d, 0o755)
		expectErr(t, VerifyClaudeShared(d, home, sc), "守る場所")
	})
}
