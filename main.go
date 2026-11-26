// sigil は、プロジェクトのコードを隔離コンテナ（sigil）で動かすための CLI。
// 使い方は sigil help、仕様は docs/superpowers/specs/ を参照。
package main

import (
	"os"

	"github.com/FrontWorksDev/sigil/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], app.DefaultDeps()))
}
