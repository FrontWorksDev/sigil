package gitcommit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"sigil/internal/ui"
)

// RunOpts は外部コマンドの実行に渡す入出力と環境変数。
type RunOpts struct {
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Runner は外部コマンドを実行する。Docker や git がなくてもテストできるよう差し替え可能にする。
type Runner interface {
	Run(name string, args []string, o RunOpts) error
}

// ExecRunner は os/exec で実行する。
type ExecRunner struct{}

// Run は name を実行する。Env は os.Environ() の後ろに足す（後のものが勝つ）。
// Stdin が nil のときは何もつながない。端末の入力をコマンドに渡さないため。
func (ExecRunner) Run(name string, args []string, o RunOpts) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), o.Env...)
	cmd.Stdin = o.Stdin
	cmd.Stdout = o.Stdout
	cmd.Stderr = o.Stderr
	return cmd.Run()
}

// SafeConfig は、信頼できないリポジトリの設定で git がコマンドを実行しないようにする -c オプション。
// フックや fsmonitor は、ホストで任意のコードを動かす経路になるため無効にする。
var SafeConfig = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "submodule.recurse=false",
	"-c", "diff.ignoreSubmodules=all",
}

// Git は、ホストの git を安全な設定で呼ぶ。
type Git struct {
	R    Runner
	Root string
	Env  []string
}

// Out は git -C Root SafeConfig... args... を実行し、標準出力を返す。
// 失敗時のエラーには標準エラーを含めるが、端末を操作されないよう Escape する。
func (g Git) Out(stdin io.Reader, args ...string) (string, error) {
	var errb bytes.Buffer
	return g.run(stdin, &errb, &errb, args)
}

// Interactive は Out と同じ安全な設定で実行するが、標準エラーを stderr にも流す。
// 署名の進み具合など、git が出す表示を利用者に見せるため。
// 流すときは ui.Escape を通す。git の出力にはコンテナが作った文字列（ファイル名など）が
// 混じりうるので、制御文字で画面を操作されないようにする。
// パスフレーズや pinentry の問い合わせは /dev/tty に出るので、これで損なわれない。
// 失敗時のエラー文には、Out と同じく標準エラーの内容（Escape 済み）を含める。
// 標準出力は取り込んで返す。
func (g Git) Interactive(stdin io.Reader, stderr io.Writer, args ...string) (string, error) {
	var errb bytes.Buffer
	return g.run(stdin, io.MultiWriter(ui.NewEscapeWriter(stderr), &errb), &errb, args)
}

// run は git を実行する。errb が nil でなければ、失敗時のエラー文に標準エラーの内容を使う。
func (g Git) run(stdin io.Reader, stderr io.Writer, errb *bytes.Buffer, args []string) (string, error) {
	full := append([]string{"-C", g.Root}, SafeConfig...)
	full = append(full, args...)
	var out bytes.Buffer
	err := g.R.Run("git", full, RunOpts{Env: g.Env, Stdin: stdin, Stdout: &out, Stderr: stderr})
	if err != nil {
		if errb == nil {
			errb = &bytes.Buffer{}
		}
		sub := ""
		if len(args) > 0 {
			sub = args[0]
		}
		msg := strings.TrimSpace(ui.Escape(errb.String()))
		if msg == "" {
			msg = ui.Escape(err.Error())
		}
		return "", fmt.Errorf("git %s に失敗しました: %s", ui.EscapeLine(sub), msg)
	}
	return out.String(), nil
}

// With は Env を足したコピーを返す。元の Git は変えない。
func (g Git) With(env ...string) Git {
	g.Env = append(append([]string(nil), g.Env...), env...)
	return g
}
