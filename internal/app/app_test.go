package app

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// ---- Task 8: 振り分け、build / ls / help / stop ----

func TestHelpWorksOutsideProjectWithoutDocker(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		e := newEnv(t)
		e.cwd = "/"
		e.dockerErr = os.ErrNotExist
		if code := e.run(arg); code != 0 {
			t.Errorf("%s: code = %d, stderr = %s", arg, code, e.stderr.String())
		}
		if !strings.Contains(e.stdout.String(), "sigil stop") {
			t.Errorf("%s: 使い方が出ない: %q", arg, e.stdout.String())
		}
		if len(e.runner.calls) != 0 {
			t.Errorf("%s: 外部コマンドを呼んだ: %v", arg, e.runner.calls)
		}
	}
}

func TestDockerNotFound(t *testing.T) {
	e := newEnv(t)
	e.dockerErr = os.ErrNotExist
	if code := e.run(); code != 1 {
		t.Errorf("code = %d", code)
	}
	// 1 行目は世界観の言葉、2 行目は普通の言葉での対処（"sigil: " の幅だけ字下げ）
	if got := e.stderr.String(); got != "sigil: 領域を開く術がありません\n       docker が見つかりません。Docker Desktop をインストールしてください\n" {
		t.Errorf("stderr = %q", got)
	}
}

func TestBuildUsesEmbeddedContext(t *testing.T) {
	e := newEnv(t)
	e.cwd = "/"
	var ctxDir string
	e.d.WriteBuildContext = func(dir string) error { ctxDir = dir; return nil }
	if code := e.run("build", "--no-cache"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	builds := e.runner.find("docker build")
	if len(builds) != 1 {
		t.Fatalf("build calls = %v", e.runner.calls)
	}
	want := []string{"build", "--pull", "--build-arg", "USER_UID=501", "--build-arg", "USER_GID=20",
		"--build-arg", "SIGIL_REFRESH=2026-11-25T01:02:03Z", "-t", "sigil:latest", "--no-cache", ctxDir}
	if ctxDir == "" || !reflect.DeepEqual(builds[0].args, want) {
		t.Errorf("argv = %v (ctx %q)", builds[0].args, ctxDir)
	}
	if _, err := os.Stat(ctxDir); !os.IsNotExist(err) {
		t.Errorf("一時フォルダが残っている: %s", ctxDir)
	}
}

func TestBuildWithContextAndImageEnv(t *testing.T) {
	e := newEnv(t)
	e.vars["SIGIL_CONTEXT"] = "/ctx"
	e.vars["SIGIL_IMAGE"] = "sigil:dev-test"
	called := false
	e.d.WriteBuildContext = func(string) error { called = true; return nil }
	if code := e.run("build"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if called {
		t.Error("SIGIL_CONTEXT があるのに埋め込みを書き出した")
	}
	args := e.runner.find("docker build")[0].args
	if args[len(args)-1] != "/ctx" || args[len(args)-2] != "sigil:dev-test" {
		t.Errorf("argv = %v", args)
	}
}

func TestLs(t *testing.T) {
	e := newEnv(t)
	e.cwd = "/"
	if code := e.run("ls"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := e.runner.find("docker ps")
	want := []string{"ps", "-a", "--filter", "label=sigil.project",
		"--format", "table {{.Names}}\t{{.Status}}\t{{.Label \"sigil.project\"}}"}
	if len(got) != 1 || !reflect.DeepEqual(got[0].args, want) {
		t.Errorf("calls = %v", e.runner.calls)
	}
}

func TestStopRemovesContainer(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	rms := e.runner.find("docker rm -f sigil-proj-")
	if len(rms) != 1 {
		t.Errorf("rm calls = %v", e.runner.calls)
	}
	if len(e.execs) != 0 {
		t.Errorf("Exec を呼んだ: %v", e.execs)
	}
}

func TestStopMissingContainer(t *testing.T) {
	e := newEnv(t)
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker rm")) != 0 {
		t.Error("ないコンテナを消そうとした")
	}
	if !strings.Contains(e.stderr.String(), "はありません") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
}

func TestDangerousPathNeverCallsDocker(t *testing.T) {
	for _, args := range [][]string{{"stop"}, {}, {"check"}, {"nvim", "."}} {
		e := newEnv(t)
		e.cwd = e.home
		e.runner.on("git -C", "", os.ErrNotExist)
		if code := e.run(args...); code != 1 {
			t.Errorf("%v: code = %d", args, code)
		}
		if got := e.runner.find("docker"); len(got) != 0 {
			t.Errorf("%v: docker を呼んだ: %v", args, got)
		}
		if len(e.execs) != 0 {
			t.Errorf("%v: Exec を呼んだ", args)
		}
		if !strings.HasPrefix(e.stderr.String(), "sigil: ") {
			t.Errorf("stderr = %q", e.stderr.String())
		}
	}
}

// sigil stop はボリュームを消さず、残っていることと消し方を知らせる（設計書 21 章）。
func TestStopKeepsVolumes(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	e.runner.on("docker volume ls", e.name()+"-cache\n", nil)
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if len(e.runner.find("docker volume rm")) != 0 {
		t.Error("sigil stop でボリュームを消した")
	}
	if !strings.Contains(e.stderr.String(), "領域の残響は残しています。消すときは sigil purge") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestStopNoVolumesNoHint(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(e.stderr.String(), "残響") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

// コンテナがなくても、ボリュームが残っていれば知らせる。
func TestStopMissingContainerWithVolumes(t *testing.T) {
	e := newEnv(t)
	e.runner.on("docker volume ls", e.name()+"-go\n", nil)
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	out := e.stderr.String()
	if !strings.Contains(out, "はありません") || !strings.Contains(out, "sigil purge") {
		t.Errorf("stderr = %s", out)
	}
}

// ボリュームを確かめられなくても、コンテナは消せているので成功のまま、案内も出さない。
func TestStopVolumeCheckFailureIsQuiet(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	e.runner.on("docker volume ls", "", errors.New("exit status 1"))
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), "残響") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

// ラベルの違うボリュームしかなければ、purge で消せないので案内しない。
func TestStopForeignVolumeNoHint(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	e.runner.on("docker volume ls", e.name()+"-cache\n", nil)
	e.runner.on("docker volume inspect", labelsJSON("/other"), nil)
	if code := e.run("stop"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(e.stderr.String(), "残響") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}
