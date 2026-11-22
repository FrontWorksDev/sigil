package app

import (
	"errors"
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
