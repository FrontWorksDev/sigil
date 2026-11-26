package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FrontWorksDev/sigil/internal/docker"
	"github.com/FrontWorksDev/sigil/internal/project"
)

// roEnv は、ro_root（~/src）と、マウントされる側のリポジトリ（~/src/lib、~/src/lib-v2）を用意し、
// ~/src/alias を ~/src/lib へのシンボリックリンクにする。
func roEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newEnv(t)
	for _, d := range []string{"lib", "lib-v2"} {
		mustMkdir(t, filepath.Join(e.home, "src", d))
	}
	if err := os.Symlink(filepath.Join(e.home, "src", "lib"), filepath.Join(e.home, "src", "alias")); err != nil {
		t.Fatal(err)
	}
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), "ro_root ~/src\n")
	return e
}

// retarget は ~/src/alias の向きを ~/src/lib-v2 に変える。
func retarget(t *testing.T, e *testEnv) {
	t.Helper()
	link := filepath.Join(e.home, "src", "alias")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.home, "src", "lib-v2"), link); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyMountApprovedAndPassed(t *testing.T) {
	e := roEnv(t)
	e.write(".sigil", "ro ~/src/alias\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	// 承認の画面には、書かれた文字列ではなく、解決したパスを出す
	lib := filepath.Join(e.home, "src", "lib")
	if !strings.Contains(e.stderr.String(), lib+" → /workspace/alias") {
		t.Errorf("承認の画面に解決したパスがない:\n%s", e.stderr.String())
	}
	if !slices.Contains(e.runArgs(), lib+":/workspace/alias:ro") {
		t.Errorf("run = %v", e.runArgs())
	}

	// 2 回目（同じ向き）は聞かない
	e2 := newEnvSharing(t, e)
	if code := e2.run(); code != 0 || e2.ttyOpened != 0 {
		t.Errorf("記録済みなのに聞いた: code=%d opened=%d %s", code, e2.ttyOpened, e2.stderr.String())
	}

	// .sigil が同じでも、シンボリックリンクの向きが変われば聞き直す
	retarget(t, e)
	e3 := newEnvSharing(t, e)
	e3.tty = "n\n"
	if code := e3.run(); code != 1 || e3.ttyOpened != 1 {
		t.Errorf("向きが変わったのに聞かない: code=%d opened=%d", code, e3.ttyOpened)
	}
	if len(e3.runner.find("docker run")) != 0 {
		t.Error("中止したのに作成した")
	}
}

func TestReadOnlyMountWithoutRootStopsBeforeDocker(t *testing.T) {
	e := newEnv(t)
	mustMkdir(t, filepath.Join(e.home, "src", "lib"))
	e.write(".sigil", "ro ~/src/lib\n")
	e.tty = "y\n"
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "ro_root") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
	if e.ttyOpened != 0 || len(e.runner.find("docker run")) != 0 {
		t.Error("確かめる前に聞いたか、作成した")
	}
}

func TestReadOnlyMountSensitiveStopsBeforeDocker(t *testing.T) {
	e := roEnv(t)
	mustMkdir(t, filepath.Join(e.home, ".ssh"))
	if err := os.Symlink(filepath.Join(e.home, ".ssh"), filepath.Join(e.home, "src", "keys")); err != nil {
		t.Fatal(err)
	}
	e.write(".sigil", "ro ~/src/keys\n")
	e.tty = "y\n"
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if e.ttyOpened != 0 || len(e.runner.find("docker run")) != 0 {
		t.Error("~/.ssh を指すのに聞いたか、作成した")
	}
}

func TestReadOnlyMountInnerRechecks(t *testing.T) {
	// op run から起動し直された側（確認はしない）でも、解決し直して承認記録と照合する
	e := roEnv(t)
	e.write(".sigil", "ro ~/src/alias\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	retarget(t, e)
	e2 := newEnvSharing(t, e)
	e2.vars["SIGIL_INNER"] = "1"
	if code := e2.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if e2.ttyOpened != 0 || len(e2.runner.find("docker run")) != 0 {
		t.Error("inner で確認したか、承認と違うフォルダで作成した")
	}
	// .sigil は同じなので、.sigil のせいだけに見せず、場所が変わった可能性を伝える
	if !strings.Contains(e2.stderr.String(), "ro のフォルダの場所が変わりました") {
		t.Errorf("stderr = %s", e2.stderr.String())
	}
}

func TestReadOnlyMountRecheckedBeforeRun(t *testing.T) {
	// 承認のあと、docker run までの間に向きを変えられたら、作らない
	e := roEnv(t)
	e.write(".sigil", "ro ~/src/alias\n")
	e.tty = "y\n"
	e.runner.onFunc("docker image inspect", func() (string, error) {
		retarget(t, e)
		return "", nil
	})
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 {
		t.Error("承認と違うフォルダで作成した")
	}
	if !strings.Contains(e.stderr.String(), "承認したあとで") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestReadOnlyMountMissingBeforeRunIsNotCalledRewritten(t *testing.T) {
	// 承認のあとでフォルダが消えただけなら、「書き換えられた」とは言わない
	e := roEnv(t)
	e.write(".sigil", "ro ~/src/lib-v2\n")
	e.tty = "y\n"
	e.runner.onFunc("docker image inspect", func() (string, error) {
		if err := os.Remove(filepath.Join(e.home, "src", "lib-v2")); err != nil {
			t.Fatal(err)
		}
		return "", nil
	})
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 {
		t.Error("消えたフォルダで作成した")
	}
	out := e.stderr.String()
	if !strings.Contains(out, "写本の在り処を確かめられません") || strings.Contains(out, "書き換えられました") {
		t.Errorf("stderr = %s", out)
	}
}

// stoppedWithMount は、~/src/lib を ro でマウントした、停止中のコンテナを偽の docker に設定する。
func stoppedWithMount(t *testing.T, e *testEnv) string {
	t.Helper()
	lib, err := filepath.EvalSymlinks(filepath.Join(e.home, "src", "lib"))
	if err != nil {
		t.Fatal(err)
	}
	e.container("exited", "2")
	label := docker.EncodeReadOnly([]project.Mount{{Host: lib, Container: "/workspace/lib"}})
	labels, _ := json.Marshal(map[string]string{docker.LayoutLabel: "2", docker.ReadOnlyLabel: label})
	e.runner.on("docker container inspect", string(labels)+"\n", nil)
	return lib
}

func TestStoppedContainerWithUnchangedMountIsStarted(t *testing.T) {
	e := roEnv(t)
	stoppedWithMount(t, e)
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.runner.find("docker start")) != 1 {
		t.Errorf("calls = %v", e.runner.calls)
	}
}

func TestStoppedContainerWithSwappedMountIsNotStarted(t *testing.T) {
	// docker start はマウント元を解決し直すので、作成のあとで差し替えられたパスを起動前に見つける
	e := roEnv(t)
	lib := stoppedWithMount(t, e)
	if err := os.Rename(lib, lib+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(e.home, "src", "lib-v2"), lib); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker start")) != 0 {
		t.Error("差し替えられたのに起動した")
	}
	out := e.stderr.String()
	if !strings.Contains(out, "写本が書き換えられました") || !strings.Contains(out, "sigil stop") {
		t.Errorf("stderr = %s", out)
	}
}

func TestStoppedContainerWithMissingMountIsNotStarted(t *testing.T) {
	e := roEnv(t)
	lib := stoppedWithMount(t, e)
	if err := os.Remove(lib); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker start")) != 0 {
		t.Error("消えたのに起動した")
	}
	out := e.stderr.String()
	if !strings.Contains(out, "写本の在り処を確かめられません") || strings.Contains(out, "書き換えられました") {
		t.Errorf("stderr = %s", out)
	}
}

func TestStoppedContainerLabelUnreadableIsNotStarted(t *testing.T) {
	e := newEnv(t)
	e.container("exited", "2")
	e.runner.on("docker container inspect", "", errors.New("daemon error"))
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker start")) != 0 {
		t.Error("確かめられないのに起動した")
	}
}

func TestRunningContainerDoesNotReadMounts(t *testing.T) {
	// 実行中のコンテナは、マウントがすでに済んでいるので確かめない（待ち時間を足さない）
	e := newEnv(t)
	e.container("running", "2")
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.runner.find("docker container inspect")) != 0 {
		t.Error("実行中なのにラベルを読んだ")
	}
}
