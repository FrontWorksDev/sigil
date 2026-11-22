package app

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	sigilimage "sigil/image"
	"sigil/internal/docker"
)

// Deps は、外部とのやりとりをまとめたもの。
// テストではすべて偽物に差し替え、docker・git・op・端末がなくても動くようにする。
type Deps struct {
	Runner docker.Runner

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
		Runner:      docker.ExecRunner{},
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

// isCharDevice は f が端末かを返す。標準ライブラリだけで判定するため、文字デバイスかどうかで見る。
func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
