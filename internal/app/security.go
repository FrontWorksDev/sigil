package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/FrontWorksDev/sigil/internal/secret"
)

// maxSecurityStderr は、security の標準エラーとしてためる最大のバイト数。
// 案内に使うだけなので、少しあれば足りる。
const maxSecurityStderr = 4 << 10

// runSecurity は path（本物は /usr/bin/security）を args で実行し、標準出力と標準エラーを返す。
//
// security はホストのキーチェーンを読む。子プロセスに余計なものを渡さず、
// 子が sigil の端末や環境に頼れないようにする:
//   - 環境は minimalEnv() だけ。sigil の環境には op run が入れたシークレットがありうるので渡さない。
//   - 作業フォルダは "/"。プロジェクトのフォルダ（コンテナが書ける）に依存させない。
//   - 標準入力は /dev/null。Setsid で新しいセッションにし、制御端末を持たせない。
//     security がパスワードを端末から尋ねて止まったり、端末の入力を読んだりしないようにする。
//
// 標準出力は secret.MaxSecretSize+1 バイトまでためる（上限を超えたことを ParseSecret が判定できるように）。
// それより後は読み捨てる。途中で読むのをやめると、子がパイプが詰まって止まるため。
// timeout を過ぎたら子を止め、context.DeadlineExceeded を包んだエラーを返す。
func runSecurity(path string, args []string, timeout time.Duration) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = minimalEnv()
	if cmd.Env == nil {
		// nil だと os/exec は親の環境を引き継ぐ。空の環境にするには空のスライスを渡す
		cmd.Env = []string{}
	}
	cmd.Dir = "/"
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// 時間切れのときは、子だけでなくプロセスグループごと止める。Setsid で子がグループの長
	// （pgid = pid）になっているので、-pid に送れば孫も止まり、ホストに取り残されない。
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// それでも孫（グループを抜けたものなど）がパイプを開いたままだと Wait が戻らない。
	// 少し待ってもパイプが閉じなければ、あきらめて戻る。
	cmd.WaitDelay = 200 * time.Millisecond

	out := &capWriter{max: secret.MaxSecretSize + 1}
	errOut := &capWriter{max: maxSecurityStderr}
	cmd.Stdout = out
	cmd.Stderr = errOut

	err = cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// 出力はエラーの文に載せない（呼ぶ側が CleanStderr などで扱う）
		return out.buf.Bytes(), errOut.buf.Bytes(), fmt.Errorf("security が %v 以内に終わりませんでした: %w", timeout, context.DeadlineExceeded)
	}
	return out.buf.Bytes(), errOut.buf.Bytes(), err
}

// minimalEnv は security に渡す環境。最初は空にしておき、実機で足りないものがあれば、
// 足したものと理由をここと設計書の決定事項に書く。
func minimalEnv() []string {
	return nil
}

// capWriter は、最初の max バイトだけをため、残りは読み捨てる io.Writer。
// 書き込みは常に成功したことにする（子のパイプを詰まらせないため）。
type capWriter struct {
	buf bytes.Buffer
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		w.buf.Write(p[:room])
	}
	return len(p), nil
}
