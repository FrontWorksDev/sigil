package app

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"sigil/internal/ui"
)

// fancy は、標準エラーを広い端末にする（演出あり）。
func (e *testEnv) fancy() {
	e.stderrTTY = true
	e.cols, e.rows = 120, 40
}

// hasSGRColor は、色の SGR（\x1b[38;5;...m や太字など、リセット以外）があるかを返す。
func hasSGRColor(s string) bool {
	return strings.Contains(s, "\x1b[38;5;") || strings.Contains(s, "\x1b[1m") || strings.Contains(s, "\x1b[2m")
}

func TestPlainWhenStderrIsNotTerminal(t *testing.T) {
	e := newEnv(t)
	e.cols, e.rows = 120, 40 // 大きさが分かっても、端末でなければ演出しない
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), "\x1b") {
		t.Errorf("パイプやリダイレクトでエスケープが出ている: %q", e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "コンテナ "+e.name()+" を作成します") {
		t.Errorf("今までどおりの素のテキストを出すこと: %q", e.stderr.String())
	}
}

func TestSigilPlainStopsEffects(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.vars["SIGIL_PLAIN"] = "1"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), "\x1b") {
		t.Errorf("SIGIL_PLAIN=1 でエスケープが出ている: %q", e.stderr.String())
	}
}

func TestCreateRitualRestoresBeforeExec(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	if code := e.run("nvim", "."); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.stderrAtExec) != 1 {
		t.Fatalf("Exec の回数 = %d", len(e.stderrAtExec))
	}
	at := e.stderrAtExec[0]
	if !strings.HasSuffix(at, ui.RestoreSeq) {
		t.Errorf("docker exec に置き換える直前に、端末を元に戻していない: %q", tail(at))
	}
	for _, want := range []string{"seal your code", "領域を展開する", "は展開されました", "領域へ入ります"} {
		if !strings.Contains(at, want) {
			t.Errorf("%q が出ていない", want)
		}
	}
	if !hasSGRColor(at) {
		t.Errorf("色が付いていない")
	}
	// 標準出力には何も足さない
	if e.stdout.Len() != 0 {
		t.Errorf("標準出力 = %q", e.stdout.String())
	}
}

func TestNoColorKeepsMotionWithoutColor(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.vars["NO_COLOR"] = "1"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if hasSGRColor(e.stderr.String()) {
		t.Errorf("NO_COLOR で色が出ている")
	}
	if !strings.Contains(e.stderr.String(), "領域を展開する") {
		t.Errorf("NO_COLOR でも演出はする")
	}
}

func TestNarrowTerminalUsesOneLineSpinner(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.cols = 60
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), "経過") {
		t.Errorf("狭い端末で魔法陣を描いている")
	}
	if !strings.HasSuffix(e.stderrAtExec[0], ui.RestoreSeq) {
		t.Errorf("端末を元に戻していない")
	}
}

func TestEnterExistingIsOneLineWithoutWaiting(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.vars["NO_COLOR"] = "1"
	e.container("running", "2")
	// 待ち時間を足していないことを、After が呼ばれないことで確かめる
	e.d.After = func(time.Duration) <-chan time.Time {
		t.Error("既存のコンテナに入るときに待っている")
		return nil
	}
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if got, want := e.stderrAtExec[0], "  ⟡ 領域 proj に入ります\n"; !strings.Contains(got, want) || !strings.HasSuffix(got, ui.RestoreSeq) {
		t.Errorf("stderr = %q", got)
	}
}

func TestEnterStoppedSpinsWhileStarting(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.container("exited", "2")
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.runner.find("docker start")) != 1 {
		t.Errorf("docker start を呼んでいない")
	}
	at := e.stderrAtExec[0]
	if !strings.Contains(at, "領域を再展開する") || !strings.Contains(at, "領域 \x1b[1mproj\x1b[0m に入ります") || !strings.HasSuffix(at, ui.RestoreSeq) {
		t.Errorf("stderr = %q", tail(at))
	}
}

func TestOpReexecRestoresTerminalFirst(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env FOO\n")
	e.tty = "y\n"
	e.write(".env", "FOO=op://vault/item/field\n")
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.execs) != 1 || e.execs[0].argv0 != "/opt/bin/op" {
		t.Fatalf("op run に置き換えていない")
	}
	if !strings.HasSuffix(e.stderrAtExec[0], ui.RestoreSeq) {
		t.Errorf("op run に置き換える直前に、端末を元に戻していない: %q", tail(e.stderrAtExec[0]))
	}
}

func TestInnerShowsSummonDone(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.vars["NO_COLOR"] = "1"
	e.vars["SIGIL_INNER"] = "1"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	// op run から起動し直した側では、禁書庫から召喚した（済んだ）ことにする
	if strings.Contains(e.stderr.String(), "禁書庫から召喚する （なし）") {
		t.Errorf("inner なのに召喚を省略と出している")
	}
}

func TestCreateFailureInRitualShowsDockerError(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.runner.on("docker run", "", errors.New("port is already allocated"))
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	out := e.stderr.String()
	if !strings.Contains(out, "✗") || !strings.Contains(out, "作成できませんでした") || len(e.execs) != 0 {
		t.Errorf("stderr = %q", tail(out))
	}
	if !strings.Contains(out, ui.RestoreSeq) {
		t.Errorf("失敗しても端末を元に戻すこと")
	}
}

func tail(s string) string {
	if len(s) > 300 {
		return "…" + s[len(s)-300:]
	}
	return s
}

// ---- sigil build ----

const buildLog = "#3 [stage-4  1/3] FROM docker.io/library/php:cli-trixie\n#3 DONE 0.2s\n" +
	"#4 [stage-4  2/3] RUN apt-get update\n#4 0.1 Get:1 http://deb.debian.org\n#4 DONE 1.0s\n" +
	"#5 [stage-4  3/3] COPY sigil-check /usr/local/bin/sigil-check\n#5 DONE 0.1s\n" +
	"#6 exporting to image\n#6 DONE 0.3s\n"

func TestBuildRitualReadsPlainProgress(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.cwd = "/"
	e.runner.onStderr("docker build", buildLog, nil)
	if code := e.run("build", "--no-cache"); code != 0 {
		t.Fatal(e.stderr.String())
	}
	args := e.runner.find("docker build")[0].args
	if !slices.Contains(args, "--progress=plain") || !slices.Contains(args, "--no-cache") {
		t.Errorf("argv = %v", args)
	}
	out := e.stderr.String()
	for _, want := range []string{"紋章の鋳造", "紋章を刻む", "の鋳造が完了しました", ui.RestoreSeq} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出ていない", want)
		}
	}
	// 成功したときは、docker のログは見せない
	if strings.Contains(out, "Get:1 http://deb.debian.org") {
		t.Errorf("成功したのに docker のログを出している")
	}
	if e.stdout.Len() != 0 {
		t.Errorf("標準出力 = %q", e.stdout.String())
	}
}

func TestBuildRitualFailureShowsDockerLog(t *testing.T) {
	e := newEnv(t)
	e.fancy()
	e.cwd = "/"
	e.runner.onStderr("docker build", "#4 [stage-4  2/3] RUN false\n#4 ERROR: process did not complete successfully\n", errors.New("exit status 1"))
	if code := e.run("build"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	out := e.stderr.String()
	r := strings.LastIndex(out, ui.RestoreSeq)
	l := strings.Index(out, "#4 ERROR: process did not complete successfully")
	if r < 0 || l < r || !strings.Contains(out, "docker build に失敗しました") {
		t.Errorf("端末を戻してから、docker のログをそのまま見せること: %q", tail(out))
	}
}

func TestBuildKeepsUserProgressAndLegacyBuilder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
		vars  map[string]string
	}{
		{"--progress の指定", []string{"--progress=tty"}, nil},
		{"-q", []string{"-q"}, nil},
		{"BuildKit を使わない", nil, map[string]string{"DOCKER_BUILDKIT": "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.fancy()
			e.cwd = "/"
			maps.Copy(e.vars, tc.vars)
			e.runner.onStderr("docker build", "plain output\n", nil)
			if code := e.run(append([]string{"build"}, tc.extra...)...); code != 0 {
				t.Fatal(e.stderr.String())
			}
			args := e.runner.find("docker build")[0].args
			if slices.Contains(args, "--progress=plain") {
				t.Errorf("--progress=plain を足している: %v", args)
			}
			if e.stderr.String() != "plain output\n" {
				t.Errorf("docker の出力をそのまま流すこと: %q", e.stderr.String())
			}
		})
	}
}

// ---- sigil ls / help / check ----

func TestLsDecoratedOnTerminal(t *testing.T) {
	e := newEnv(t)
	e.cwd = "/"
	e.stdoutTTY = true
	e.cols, e.rows = 120, 40
	e.runner.on("docker ps -a --filter label=sigil.project --format {{.Names}}",
		"sigil-a-1\trunning\tUp 2 hours\t/src/a\nsigil-b-2\texited\tExited (0) 1 day ago\t/src/b\n", nil)
	if code := e.run("ls"); code != 0 {
		t.Fatal(e.stderr.String())
	}
	out := e.stdout.String()
	for _, want := range []string{"展開中の領域", "●", "sigil-a-1", "展開中", "○", "停止中", "/src/b"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない: %q", want, out)
		}
	}
}

func TestLsPlainWhenPiped(t *testing.T) {
	e := newEnv(t)
	e.cwd = "/"
	e.stderrTTY = true // 標準エラーが端末でも、標準出力がパイプなら飾らない
	e.runner.on("docker ps -a --filter label=sigil.project --format table", "NAMES\tSTATUS\n", nil)
	if code := e.run("ls"); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if e.stdout.String() != "NAMES\tSTATUS\n" {
		t.Errorf("パイプでは docker ps の表をそのまま出すこと: %q", e.stdout.String())
	}
}

func TestHelpBannerOnlyOnTerminal(t *testing.T) {
	e := newEnv(t)
	if e.run("help"); strings.Contains(e.stdout.String(), "seal your code") || strings.Contains(e.stdout.String(), "\x1b") {
		t.Errorf("パイプの help にバナーやエスケープが入っている")
	}
	e2 := newEnv(t)
	e2.stdoutTTY = true
	e2.cols, e2.rows = 80, 24
	if e2.run("help"); !strings.Contains(e2.stdout.String(), "seal your code") || !strings.Contains(e2.stdout.String(), "使い方:") {
		t.Errorf("端末の help にバナーがない: %q", e2.stdout.String())
	}
}

func TestCheckPassesNoColorWhenHostDisablesColor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tty    bool
		vars   map[string]string
		expect bool
	}{
		{"端末で色あり", true, nil, false},
		{"NO_COLOR", true, map[string]string{"NO_COLOR": "1"}, true},
		{"SIGIL_PLAIN", true, map[string]string{"SIGIL_PLAIN": "1"}, true},
		{"パイプ", false, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.container("running", "2")
			e.stdoutTTY = tc.tty
			maps.Copy(e.vars, tc.vars)
			if code := e.run("check"); code != 0 {
				t.Fatal(e.stderr.String())
			}
			argv := strings.Join(e.lastExec().argv, " ")
			if got := strings.Contains(argv, "-e NO_COLOR=1"); got != tc.expect {
				t.Errorf("NO_COLOR=1 を渡す = %v, want %v: %s", got, tc.expect, argv)
			}
			if !strings.HasSuffix(argv, e.name()+" sigil-check") {
				t.Errorf("argv = %s", argv)
			}
		})
	}
	// check 以外のコマンドには渡さない
	e := newEnv(t)
	e.container("running", "2")
	e.vars["NO_COLOR"] = "1"
	e.run("nvim", ".")
	if strings.Contains(strings.Join(e.lastExec().argv, " "), "NO_COLOR") {
		t.Errorf("check 以外で NO_COLOR を渡している")
	}
}
