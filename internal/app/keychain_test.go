package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"sigil/internal/docker"
	"sigil/internal/secret"
	"sigil/internal/ui"
)

// ---- キーチェーンから読んで渡す（issue #25） ----

// keychainEnv は、偽のホームに専用キーチェーンのファイルを作った環境を返す。
// 全体設定には keychain と env WAKATIME_API_KEY を、全体の secrets.env には
// キーチェーンの参照を書く（プロジェクトの .sigil / .env がないので、承認は要らない）。
// 返すのは、security に渡されるはずの、リンクを解いたキーチェーンのパス。
func keychainEnv(t *testing.T, withKeychain bool) (*testEnv, string) {
	t.Helper()
	e := newEnv(t)
	kc := e.write(filepath.Join(e.home, "Library", "Keychains", "s.keychain-db"), "")
	// ファイルの持ち主の確かめは、テストを実行しているユーザーで行う
	e.d.Getuid = os.Getuid
	cfg := "env WAKATIME_API_KEY\n"
	if withKeychain {
		cfg = "keychain ~/Library/Keychains/s.keychain-db\n" + cfg
	}
	e.globalConfig(cfg)
	e.write(filepath.Join(e.home, ".config", "sigil", "secrets.env"), "WAKATIME_API_KEY=keychain://w\n")
	return e, kc
}

// runCall は docker run の呼び出しを 1 つ返す。
func (e *testEnv) runCall() call {
	e.t.Helper()
	runs := e.runner.find("docker run")
	if len(runs) != 1 {
		e.t.Fatalf("docker run の回数 = %d。stderr = %s", len(runs), e.stderr.String())
	}
	return runs[0]
}

func TestKeychainValuePassedViaEnvOnly(t *testing.T) {
	e, kc := keychainEnv(t, true)
	e.sec.on(secret.SecurityArgs("w", kc), "v4lue\n", "", nil)
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	c := e.runCall()
	if !strings.Contains(strings.Join(c.args, " "), "-e WAKATIME_API_KEY") {
		t.Errorf("-e WAKATIME_API_KEY がない: %v", c.args)
	}
	for _, x := range e.runner.calls {
		if strings.Contains(x.String(), "v4lue") {
			t.Errorf("値が argv に出ている: %s", x)
		}
	}
	if !reflect.DeepEqual(c.env, []string{"WAKATIME_API_KEY=v4lue"}) {
		t.Errorf("docker の環境 = %v", c.env)
	}
	if want := [][]string{secret.SecurityArgs("w", kc)}; !reflect.DeepEqual(e.sec.calls, want) {
		t.Errorf("security の引数 = %v", e.sec.calls)
	}
	if strings.Contains(e.stderr.String(), "v4lue") {
		t.Errorf("値を表示した: %q", e.stderr.String())
	}
}

func TestKeychainBeatsHostEnv(t *testing.T) {
	// ホストのシェルに同じ名前があっても、キーチェーンの値を使う
	e, kc := keychainEnv(t, true)
	e.vars["WAKATIME_API_KEY"] = "host"
	e.sec.on(secret.SecurityArgs("w", kc), "v4lue\n", "", nil)
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if c := e.runCall(); !reflect.DeepEqual(c.env, []string{"WAKATIME_API_KEY=v4lue"}) {
		t.Errorf("docker の環境 = %v", c.env)
	}
}

func TestKeychainNotConfiguredDropsName(t *testing.T) {
	// keychain の設定がなければ、ホストに同じ名前があっても渡さない（シェルの広い権限のトークンを渡さないため）
	e, _ := keychainEnv(t, false)
	e.vars["WAKATIME_API_KEY"] = "host"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "注意: 全体設定に keychain がないので、WAKATIME_API_KEY をキーチェーンから読み込みません") {
		t.Errorf("注意が出ない: %q", e.stderr.String())
	}
	c := e.runCall()
	if slices.Contains(c.args, "WAKATIME_API_KEY") || len(c.env) != 0 {
		t.Errorf("読めなかった名前を渡した: args=%v env=%v", c.args, c.env)
	}
	if len(e.sec.calls) != 0 {
		t.Errorf("security を呼んだ: %v", e.sec.calls)
	}
	if len(e.runner.find("docker exec")) != 0 {
		t.Error("渡していないのに WakaTime を設定した")
	}
}

func TestKeychainNotConfiguredInnerLiteral(t *testing.T) {
	// op run が secrets.env の keychain:// の行を文字列のまま環境に入れても、それを渡さない
	e, _ := keychainEnv(t, false)
	e.vars["SIGIL_INNER"] = "1"
	e.vars["WAKATIME_API_KEY"] = "keychain://w"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if c := e.runCall(); slices.Contains(c.args, "WAKATIME_API_KEY") {
		t.Errorf("keychain:// の文字列を渡した: %v", c.args)
	}
}

func TestKeychainFailureStops(t *testing.T) {
	e, kc := keychainEnv(t, true)
	e.sec.on(secret.SecurityArgs("w", kc), "v4lue", "\x1b[2Jlocked", errors.New("exit status 51"))
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker run")) != 0 {
		t.Error("読めなかったのにコンテナを作った")
	}
	out := e.stderr.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("標準エラーの制御文字をそのまま出した: %q", out)
	}
	if strings.Contains(out, "v4lue") {
		t.Errorf("標準出力（値）を出した: %q", out)
	}
	for _, want := range []string{"禁書庫を開けませんでした", "locked", "security unlock-keychain " + kc} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない: %q", want, out)
		}
	}
}

func TestKeychainBadPathStops(t *testing.T) {
	e, _ := keychainEnv(t, false)
	e.write(filepath.Join(e.home, "x.keychain-db"), "")
	e.globalConfig("keychain ~/x.keychain-db\nenv WAKATIME_API_KEY\n")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.sec.calls) != 0 || len(e.runner.find("docker run")) != 0 {
		t.Errorf("確かめられないキーチェーンなのに進んだ: sec=%v", e.sec.calls)
	}
	if !strings.Contains(e.stderr.String(), "禁書庫を開けませんでした") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
}

func TestKeychainReadInInnerOnly(t *testing.T) {
	e, kc := keychainEnv(t, true)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env A\nenv W\n")
	e.write(".env", "A=op://v/i/f\nW=keychain://k\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if e.lastExec().argv0 != "/opt/bin/op" {
		t.Fatal("op run に置き換えていない")
	}
	if len(e.sec.calls) != 0 {
		t.Errorf("起動し直す前の側で security を呼んだ: %v", e.sec.calls)
	}

	e2 := newEnvSharing(t, e)
	e2.d.Getuid = os.Getuid
	e2.opPath = "/opt/bin/op"
	e2.vars["SIGIL_INNER"] = "1"
	e2.vars["A"] = "from-op"
	e2.vars["W"] = "keychain://k" // op run が .env の行を文字列のまま入れたもの
	e2.sec.on(secret.SecurityArgs("k", kc), "kv\n", "", nil)
	e2.sec.on(secret.SecurityArgs("w", kc), "wv\n", "", nil)
	if code := e2.run(); code != 0 {
		t.Fatal(e2.stderr.String())
	}
	c := e2.runCall()
	// 名前の順は plan の順（プロジェクト → 全体）
	if want := []string{"W=kv", "WAKATIME_API_KEY=wv"}; !reflect.DeepEqual(c.env, want) {
		t.Errorf("docker の環境 = %v", c.env)
	}
	for _, n := range []string{"A", "W", "WAKATIME_API_KEY"} {
		if !slices.Contains(c.args, n) {
			t.Errorf("%s を渡していない: %v", n, c.args)
		}
	}
}

func TestKeychainWakaTime(t *testing.T) {
	e, kc := keychainEnv(t, true)
	e.sec.on(secret.SecurityArgs("w", kc), "v4lue\n", "", nil)
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if got := e.runner.find("docker exec sigil-proj-"); len(got) != 2 {
		t.Errorf("wakatime の exec = %v", got)
	}
}

func TestKeychainExistingContainerNoRead(t *testing.T) {
	e, _ := keychainEnv(t, true)
	e.container("running", docker.LayoutVersion)
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.sec.calls) != 0 {
		t.Errorf("既存のコンテナなのに security を呼んだ: %v", e.sec.calls)
	}
}

func TestKeychainReadBeforeRitual(t *testing.T) {
	// 解錠のダイアログを待つあいだに演出を描かないよう、演出を始める前に読む
	e, kc := keychainEnv(t, true)
	e.fancy()
	e.sec.on(secret.SecurityArgs("w", kc), "v4lue\n", "", nil)
	var atCall string
	run := e.d.Security
	e.d.Security = func(args []string) ([]byte, []byte, error) {
		atCall = e.stderr.String()
		return run(args)
	}
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.sec.calls) != 1 {
		t.Fatalf("security の呼び出し = %v", e.sec.calls)
	}
	if strings.Contains(atCall, "\x1b") {
		t.Errorf("security を呼ぶ前に演出を始めた: %q", atCall)
	}
	if !strings.Contains(e.stderr.String(), "\x1b") {
		t.Error("演出が描かれていない（テストの前提が崩れている）")
	}
}

func TestKeychainFailureReason(t *testing.T) {
	exitErr := exec.Command("/usr/bin/false").Run()
	var ee *exec.ExitError
	if !errors.As(exitErr, &ee) {
		t.Fatalf("ExitError を作れない: %v", exitErr)
	}
	for _, tc := range []struct {
		name   string
		stderr string
		err    error
		want   string
	}{
		{"標準エラー", "bad\x1b[31m\n", ee, "（bad[31m）"},
		{"終了コード", "", ee, fmt.Sprintf("（終了コード %d）", ee.ExitCode())},
		{"時間切れ", "", fmt.Errorf("x: %w", context.DeadlineExceeded), "（時間切れ）"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, kc := keychainEnv(t, true)
			e.sec.on(secret.SecurityArgs("w", kc), "", tc.stderr, tc.err)
			if code := e.run(); code != 1 {
				t.Fatalf("code = %d", code)
			}
			if !strings.Contains(e.stderr.String(), tc.want) {
				t.Errorf("%q がない: %q", tc.want, e.stderr.String())
			}
		})
	}
}

func TestSummonStageNote(t *testing.T) {
	for _, tc := range []struct {
		inner, keychain bool
		note            string
		state           ui.StageState
	}{
		{false, false, "1Password", ui.Skipped},
		{true, false, "1Password", ui.Done},
		{false, true, "キーチェーン", ui.Done},
		{true, true, "1Password・キーチェーン", ui.Done},
	} {
		s := summonStage(tc.inner, tc.keychain)
		if s.Label != "禁書庫から召喚する" || s.Note != tc.note || s.State != tc.state {
			t.Errorf("inner=%v keychain=%v: %+v", tc.inner, tc.keychain, s)
		}
	}
}
