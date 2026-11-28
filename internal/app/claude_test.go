package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ---- 神託（Claude Code の共通の設定。設計書 22 章） ----

// claudeDir は、ホームの .config/sigil/claude を作って返す。
func (e *testEnv) claudeDir() string {
	e.t.Helper()
	d := filepath.Join(e.home, ".config", "sigil", "claude")
	mustMkdir(e.t, d)
	if err := os.Chmod(d, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func TestClaudeSharedMounted(t *testing.T) {
	e := newEnv(t)
	d := e.claudeDir()
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	args := e.runArgs()
	if !slices.Contains(args, d+":/etc/claude-code:ro") || !slices.Contains(args, "sigil.claude="+d) {
		t.Errorf("run = %v", args)
	}
}

func TestClaudeSharedAbsentNoMount(t *testing.T) {
	e := newEnv(t)
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d", code)
	}
	for _, a := range e.runArgs() {
		if strings.Contains(a, "/etc/claude-code") {
			t.Errorf("神託がないのにマウントした: %s", a)
		}
	}
}

// フォルダがリンクなら、ボリュームも作らずに止まる。
func TestClaudeSharedSymlinkStops(t *testing.T) {
	e := newEnv(t)
	real := filepath.Join(filepath.Dir(e.home), "real")
	mustMkdir(t, real)
	mustMkdir(t, filepath.Join(e.home, ".config", "sigil"))
	if err := os.Symlink(real, filepath.Join(e.home, ".config", "sigil", "claude")); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker run"))+len(e.runner.find("docker volume create")) != 0 {
		t.Errorf("calls = %v", e.runner.calls)
	}
	if !strings.Contains(e.stderr.String(), "神託が届きません") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestClaudeVolumeCreated(t *testing.T) {
	e := newEnv(t)
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d", code)
	}
	found := false
	for _, c := range e.runner.find("docker volume create") {
		if c.args[len(c.args)-1] == e.name()+"-claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("calls = %v", e.runner.calls)
	}
}

// 停止中のコンテナを起動する前に、作成時の神託のパスがリンクに差し替えられていないかを確かめる。
func TestExistingStoppedVerifiesClaudeShared(t *testing.T) {
	e := newEnv(t)
	d := e.claudeDir()
	e.containerWithLabels("exited", "4", map[string]string{"sigil.claude": d})
	moved := filepath.Join(filepath.Dir(e.home), "moved")
	if err := os.Rename(d, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, d); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker start")) != 0 {
		t.Error("差し替えられたのに起動した")
	}
	if !strings.Contains(e.stderr.String(), "神託が歪められました") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestExistingStoppedClaudeSharedOK(t *testing.T) {
	e := newEnv(t)
	d := e.claudeDir()
	e.containerWithLabels("exited", "4", map[string]string{"sigil.claude": d})
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if len(e.runner.find("docker start")) != 1 {
		t.Errorf("calls = %v", e.runner.calls)
	}
}

// 神託なしで作ったコンテナに、あとからフォルダを作っても渡らない。入るときに知らせる。
func TestExistingWithoutClaudeSharedHints(t *testing.T) {
	e := newEnv(t)
	e.claudeDir()
	e.containerWithLabels("running", "4", map[string]string{"sigil.claude": ""})
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "この領域は神託なしで展開されています") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

// 動いているコンテナに入るときは、docker ps の 1 回で済ませる（神託のラベルも ps で読む）。
func TestRunningContainerNoExtraInspect(t *testing.T) {
	e := newEnv(t)
	d := e.claudeDir()
	e.containerWithLabels("running", "4", map[string]string{"sigil.claude": d})
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if n := len(e.runner.find("docker container inspect")); n != 0 {
		t.Errorf("container inspect = %d 回", n)
	}
	if strings.Contains(e.stderr.String(), "神託なし") {
		t.Errorf("神託を渡したコンテナなのに案内した: %s", e.stderr.String())
	}
}

// 神託のフォルダが不正（リンク）なら、作り直しても渡せないので、作り直しを勧めない。
func TestNoHintWhenClaudeSharedInvalid(t *testing.T) {
	e := newEnv(t)
	real := filepath.Join(filepath.Dir(e.home), "real")
	mustMkdir(t, real)
	mustMkdir(t, filepath.Join(e.home, ".config", "sigil"))
	if err := os.Symlink(real, filepath.Join(e.home, ".config", "sigil", "claude")); err != nil {
		t.Fatal(err)
	}
	e.containerWithLabels("running", "4", map[string]string{"sigil.claude": ""})
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(e.stderr.String(), "神託なし") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

// 神託の中に書き込めるファイルがあるなど、作り直しても渡せないときは、作り直しを勧めない。
func TestNoHintWhenClaudeSharedHasWritableFile(t *testing.T) {
	e := newEnv(t)
	d := e.claudeDir()
	p := filepath.Join(d, "CLAUDE.md")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	e.containerWithLabels("running", "4", map[string]string{"sigil.claude": ""})
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(e.stderr.String(), "神託なし") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}
