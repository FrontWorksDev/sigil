// Package sigilimage は、sigil イメージのビルドコンテキスト（Dockerfile と
// sigil-check）を sigil のバイナリに埋め込む。
//
// embed は親フォルダを参照できないので、image/ 自体をパッケージにしている。
// 書き出すのは埋め込んだ 2 ファイルだけなので、このファイルがビルドコンテキストに入ることはない。
package sigilimage

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed Dockerfile sigil-check
var files embed.FS

// embed はファイルのモードを保存しないので、書き出すときに明示する。
// sigil-check はイメージ内で実行されるので 0755 にする。
var modes = map[string]os.FileMode{
	"Dockerfile":  0o644,
	"sigil-check": 0o755,
}

// WriteContext は dir にビルドコンテキストを書き出す。
func WriteContext(dir string) error {
	for name, mode := range modes {
		b, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, mode); err != nil {
			return err
		}
		// umask の影響を受けないように、モードを付け直す
		if err := os.Chmod(p, mode); err != nil {
			return err
		}
	}
	return nil
}
