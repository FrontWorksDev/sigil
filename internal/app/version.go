package app

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version は、make build が -ldflags "-X sigil/internal/app.version=..." で埋め込む
// （git describe --tags --always --dirty の結果）。go build だけで作ったときは空で、dev と表示する。
var version string

// printVersion は sigil --version / -V の表示。docker も op も呼ばずに、バイナリの情報だけを出す。
func printVersion(d Deps) {
	bi, ok := debug.ReadBuildInfo()
	fmt.Fprint(d.Stdout, versionString(version, bi, ok, runtime.Version(), runtime.GOOS, runtime.GOARCH))
}

// versionString は「sigil v0.1.0 (commit e9dcf5c, go1.27.4, darwin/arm64)」の形の 1 行を作る。
// コミットは、Go が git リポジトリの中でビルドしたときに自動で埋め込む VCS の情報から取る
// （-trimpath でも残る）。make を通さずに go build しても、どのコミットかは分かる。
func versionString(embedded string, bi *debug.BuildInfo, ok bool, goVersion, goos, goarch string) string {
	v := embedded
	if v == "" {
		v = "dev"
	}
	var rev string
	var modified bool
	if ok && bi != nil {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
	}
	commit := ""
	if rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		if modified {
			rev += "-dirty"
		}
		commit = "commit " + rev + ", "
	}
	return fmt.Sprintf("sigil %s (%s%s, %s/%s)\n", v, commit, goVersion, goos, goarch)
}
