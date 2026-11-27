// sigil-check は、コンテナの中から隔離が効いているかを確かめる（使い方: ホストで sigil check）。
//
// イメージのビルドでコンパイルされ、/usr/local/bin/sigil-check に置かれる（設計書 11 章、issue #23）。
// コンテナの中で、信頼できないプロジェクトのコードと同じ環境で動き、出力はホストの端末に流れる。
// sigil 本体のパッケージは import しない（ビルドコンテキストには、このフォルダだけを書き出す）。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	code := 1
	func() {
		// 想定外の panic で、値を含みうるスタックトレースを出さない。失敗として終える
		defer func() {
			if recover() != nil {
				fmt.Fprintln(os.Stdout, "  ✗ 点検の途中で想定外のエラーが起きた")
				code = 1
			}
		}()
		code = run(os.Stdout, realSys(), colorEnabled())
	}()
	os.Exit(code)
}

func run(w io.Writer, s *sys, color bool) int {
	if render(w, check(s), color) {
		return 1
	}
	return 0
}

// 色は、標準出力が端末で、NO_COLOR がないときだけ付ける（パイプやリダイレクトでは素のテキスト）。
// ホストで色を止めているときは、sigil check が NO_COLOR=1 を渡してくる。
func colorEnabled() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
}

var errNotRegular = errors.New("普通のファイルではない")

const (
	cmdTimeout = 10 * time.Second
	outLimit   = 64 << 10
)

func realSys() *sys {
	return &sys{
		lookupEnv: os.LookupEnv,
		environ:   os.Environ,
		lstat: func(p string) (fs.FileMode, error) {
			fi, err := os.Lstat(p)
			if err != nil {
				return 0, err
			}
			return fi.Mode(), nil
		},
		isDir: func(p string) bool {
			fi, err := os.Stat(p)
			return err == nil && fi.IsDir()
		},
		tryWrite: func(dir string) error {
			// 毎回違う名前で、新しく作る（O_EXCL）。決まった名前だと、同じ名前のものが先にあるだけで
			// 失敗し、書き込めるのに「読み取り専用」に見える。シンボリックリンクもたどらない
			f, err := os.CreateTemp(dir, ".sigil-write-test-")
			if err != nil {
				return err
			}
			f.Close()
			os.Remove(f.Name())
			return nil
		},
		tryOpenWrite: func(p string) error {
			// O_CREATE も O_TRUNC も付けないので、作らず、中身も変えない。O_NONBLOCK は、FIFO に
			// 差し替えられていても止まらないため。O_NOFOLLOW でシンボリックリンクはたどらない
			f, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
			if err != nil {
				return err
			}
			return f.Close()
		},
		readFile: func(p string) ([]byte, error) {
			// O_NONBLOCK で開き、普通のファイルだけを読む。プロジェクトのコードが hosts.yml を FIFO にすると、
			// ふつうに開くだけで止まり、点検が終わらなくなるため
			f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil {
				return nil, err
			}
			if !fi.Mode().IsRegular() {
				return nil, errNotRegular
			}
			return io.ReadAll(io.LimitReader(f, 1<<20))
		},
		lookPath: exec.LookPath,
		resolve: func(p string) string {
			if r, err := filepath.EvalSymlinks(p); err == nil {
				return r
			}
			return p
		},
		user: func() (int, string) {
			uid := os.Getuid()
			name := strconv.Itoa(uid)
			if u, err := user.LookupId(name); err == nil {
				name = u.Username
			}
			return uid, name
		},
		rateLimit: func(tok string) (int, http.Header, error) {
			roots, err := loadRoots(caFile)
			if err != nil {
				return 0, nil, err
			}
			return newRateLimit(rateLimitURL, roots, cmdTimeout)(tok)
		},
		version: toolVersion,
		now:     time.Now,
	}
}

// toolEnv は、版を問い合わせるコマンドに渡す環境変数。トークンなどの秘密は渡さない。
// PATH はイメージの場所だけにし、GOTOOLCHAIN=local で go に toolchain を取りに行かせない。
func toolEnv() []string {
	home := os.Getenv("HOME")
	return []string{
		"PATH=/usr/local/bin:/usr/local/go/bin:/usr/local/cargo/bin:/usr/bin:/bin",
		"HOME=" + home,
		"LANG=C.UTF-8",
		"GOTOOLCHAIN=local",
		// 更新はイメージの作り直しで行う（イメージの ENV と同じ）。claude --version で自分を更新させない
		"DISABLE_AUTOUPDATER=1",
		"RUSTUP_HOME=/usr/local/rustup",
		"CARGO_HOME=/usr/local/cargo",
	}
}

// limitedBuffer は、上限を超えた分を捨てる。
type limitedBuffer struct{ b []byte }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := outLimit - len(l.b); room > 0 {
		if len(p) > room {
			l.b = append(l.b, p[:room]...)
		} else {
			l.b = append(l.b, p...)
		}
	}
	return len(p), nil
}

func toolVersion(path, name string) string {
	// go は version サブコマンド、lua5.1 は -v（標準エラーに出す）で版を出す
	args := []string{"--version"}
	switch name {
	case "go":
		args = []string{"version"}
	case "lua5.1":
		args = []string{"-v"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	// path は確かめたあとの解決済みのパス。argv[0] は、シェルで実行したときと同じく名前にする
	// （bat → batcat のように、argv[0] で動きを変えるものがあるため）
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Args[0] = name
	// 作業フォルダはプロジェクトではなく / にする（go.mod の toolchain や .npmrc を読ませない）
	cmd.Dir = "/"
	cmd.Env = toolEnv()
	cmd.WaitDelay = time.Second
	var out limitedBuffer
	cmd.Stdout = &out
	if name == "lua5.1" {
		cmd.Stderr = &out
	}
	_ = cmd.Run()
	return firstLine(string(out.b))
}
