// Package sigilimage は、sigil イメージのビルドコンテキスト（Dockerfile と
// sigil-check の Go のソース）を sigil のバイナリに埋め込む。
//
// embed は親フォルダを参照できないので、image/ 自体をパッケージにしている。
// 書き出すのは埋め込んだファイルと sigilcheck/go.mod だけなので、このファイルやテストが
// ビルドコンテキストに入ることはない。
package sigilimage

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// sigilcheck/ の _test.go 以外の .go だけを書き出す（置き忘れたファイルを持ち込まないよう、
// 埋め込むのも .go だけにする）。
//
//go:embed Dockerfile sigilcheck/*.go
var files embed.FS

// sigilcheckGoVersion は、sigilcheck/go.mod の go の行。ルートの go.mod と同じ版にする
// （embed_test.go で確かめる）。go.mod をリポジトリに置かないのは、入れ子のモジュールになり、
// ホストの go test / go vet / govulncheck の対象から外れるため（設計書 11 章）。
const sigilcheckGoVersion = "1.27"

var goVersionRe = regexp.MustCompile(`^1\.\d+(\.\d+)?$`)

// WriteContext は dir にビルドコンテキストを書き出す。
func WriteContext(dir string) error {
	if err := writeFile(dir, "Dockerfile", mustRead("Dockerfile"), 0o644); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(dir, "sigilcheck"), 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(files, "sigilcheck")
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		p := path.Join("sigilcheck", name)
		if err := writeFile(dir, p, mustRead(p), 0o644); err != nil {
			return err
		}
	}
	if !goVersionRe.MatchString(sigilcheckGoVersion) {
		return fmt.Errorf("sigilcheck の go の版が不正: %q", sigilcheckGoVersion)
	}
	// toolchain の行は書かない（GOTOOLCHAIN=local でビルドするので、別の toolchain を求めさせない）
	mod := "module sigilcheck\n\ngo " + sigilcheckGoVersion + "\n"
	return writeFile(dir, "sigilcheck/go.mod", []byte(mod), 0o644)
}

func mustRead(name string) []byte {
	b, err := files.ReadFile(name)
	if err != nil {
		// 埋め込みの指定と一致しない名前は、プログラムの誤り
		panic(err)
	}
	return b
}

func writeFile(dir, name string, b []byte, mode os.FileMode) error {
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.WriteFile(p, b, mode); err != nil {
		return err
	}
	// umask の影響を受けないように、モードを付け直す
	return os.Chmod(p, mode)
}
