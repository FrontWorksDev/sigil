package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
