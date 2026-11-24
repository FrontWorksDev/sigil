package app

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	sigilimage "sigil/image"
	"sigil/internal/docker"
	"sigil/internal/gitcommit"
	"sigil/internal/secret"
)

// Deps は、外部とのやりとりをまとめたもの。
// テストではすべて偽物に差し替え、docker・git・op・端末がなくても動くようにする。
type Deps struct {
	Runner docker.Runner

	// Security は、キーチェーンを読む /usr/bin/security を args で実行する。
	// テストでは偽物に差し替え、本物のキーチェーンに触れないようにする。
	Security func(args []string) (stdout, stderr []byte, err error)

	// Cmd は sigil commit が使う git と docker の実行。環境変数と標準入力を渡せる
	// （一時の index を指す GIT_INDEX_FILE と、銘を標準入力で渡すため）。
	Cmd gitcommit.Runner
	// Pager は、sigil commit の全差分を見せる。content は sigil が Escape して色を付けたもの。
	// Escape 済みの入力から作った delta の出力のこともあり、その場合は SGR と `ESC[K` だけを含む。
	Pager func(content string) error

	// Exec は現在のプロセスを argv0 で置き換える。本物（syscall.Exec）は成功すると戻らない。
	// 偽物は nil を返して戻ってくるので、呼んだ側は直後に必ず return すること。
	Exec func(argv0 string, argv, env []string) error

	LookPath    func(string) (string, error)
	LookupEnv   func(string) (string, bool)
	Environ     func() []string
	Getwd       func() (string, error)
	UserHomeDir func() (string, error)

	// OpenTTY は /dev/tty を開く。承認の入力は標準入力ではなくここから読む。
	OpenTTY func() (io.ReadCloser, error)
	// IsTerminal は、標準入力と標準出力がどちらも端末かを返す。
	IsTerminal func() bool
	// StderrIsTerminal は、標準エラーが端末かを返す。演出は標準エラーが端末のときだけ行う。
	StderrIsTerminal func() bool
	// StdoutIsTerminal は、標準出力が端末かを返す。sigil ls / help を飾るのは、このときだけ。
	StdoutIsTerminal func() bool
	// TermSize は、標準エラーの端末の幅と高さを返す。分からなければ 0, 0。
	TermSize func() (cols, rows int)
	// StdoutTermSize は、標準出力の端末の幅と高さを返す（sigil ls / help 用）。分からなければ 0, 0。
	StdoutTermSize func() (cols, rows int)
	// After は time.After。演出の待ちを、テストでは待たずに進めるため。
	After func(time.Duration) <-chan time.Time
	// Executable は sigil 自身の絶対パスを返す（op run から起動し直すため）。
	Executable func() (string, error)

	Getuid, Getgid func() int

	// WriteBuildContext は dir にイメージのビルドコンテキストを書き出す。
	WriteBuildContext func(dir string) error

	Stdout, Stderr io.Writer
}

// DefaultDeps は本物の Deps を返す。
func DefaultDeps() Deps {
	return Deps{
		Runner: docker.ExecRunner{},
		Security: func(args []string) ([]byte, []byte, error) {
			return runSecurity(secret.SecurityPath, args, secret.SecurityTimeout)
		},
		Cmd:         gitcommit.ExecRunner{},
		Pager:       pager,
		Exec:        syscall.Exec,
		LookPath:    exec.LookPath,
		LookupEnv:   os.LookupEnv,
		Environ:     os.Environ,
		Getwd:       os.Getwd,
		UserHomeDir: os.UserHomeDir,
		OpenTTY: func() (io.ReadCloser, error) {
			return os.Open("/dev/tty")
		},
		IsTerminal: func() bool {
			return isCharDevice(os.Stdin) && isCharDevice(os.Stdout)
		},
		StderrIsTerminal: func() bool { return isCharDevice(os.Stderr) },
		StdoutIsTerminal: func() bool { return isCharDevice(os.Stdout) },
		TermSize:         func() (int, int) { return termSize(os.Stderr) },
		StdoutTermSize:   func() (int, int) { return termSize(os.Stdout) },
		After:            time.After,
		Executable: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(p)
		},
		Getuid:            os.Getuid,
		Getgid:            os.Getgid,
		WriteBuildContext: sigilimage.WriteContext,
		Stdout:            os.Stdout,
		Stderr:            os.Stderr,
	}
}

// pager は content を less -R -S で見せる。less がなければ、そのまま標準エラーに出す。
// -R（エスケープをそのまま通す）を付けてよいのは、content に残るエスケープが、
// sigil が付けた色だけだから（ui.DiffPage が行ごとに Escape してから色を付ける）。
// LESSSECURE=1 は、less の中からシェルや編集を起動したり、LESSOPEN の前処理を
// 動かしたりできなくする。差分はコンテナが書いた内容なので、ホストで何も実行させないため。
func pager(content string) error {
	less, err := exec.LookPath("less")
	if err != nil {
		_, err := io.WriteString(os.Stderr, content)
		return err
	}
	cmd := exec.Command(less, "-R", "-S")
	cmd.Stdin = strings.NewReader(content)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "LESSSECURE=1")
	return cmd.Run()
}

// isCharDevice は f が端末かを返す。標準ライブラリだけで判定するため、文字デバイスかどうかで見る。
func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// termSize は f の端末の幅と高さを返す。外部モジュールや cgo を使わないよう、
// ioctl(TIOCGWINSZ) を syscall で直接呼ぶ。端末でなければ 0, 0。
func termSize(f *os.File) (cols, rows int) {
	var ws struct{ Row, Col, Xpixel, Ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
}
