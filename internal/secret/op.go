// Package secret は、1Password とキーチェーンの参照を扱う。
//
// 1Password は、1Password CLI（op run）を経由して sigil を起動し直すための判定と argv を作る。
// sigil 自身は 1Password の秘密の値を扱わない。.env からはキー名と参照（どの項目か）だけを取り出し、
// 値の解決は op run に任せ、環境変数として sigil に渡してもらう。
package secret

import "bytes"

// InnerEnv は、op run から起動し直された側であることを示す環境変数。
// これがあれば、承認の確認と op run をもう一度行わない（無限ループ防止）。
const InnerEnv = "SIGIL_INNER"

// HasOpRef は、.env の内容に op:// 参照があるかを返す。
func HasOpRef(data []byte) bool {
	return bytes.Contains(data, []byte("op://"))
}

// Argv は op run の argv を返す（argv[0] は "op"）。
//
//   - files はプロジェクト → 全体の順で渡すこと。op run は後のファイルを優先するので、
//     プロジェクトの .env で全体のシークレットを上書きされないようにするため。
//   - --no-masking は必須。付けないと op が出力を加工し、Neovim などの対話的なプログラムが壊れる。
func Argv(self string, files []string, args []string) []string {
	argv := []string{"op", "run", "--no-masking"}
	for _, f := range files {
		argv = append(argv, "--env-file="+f)
	}
	argv = append(argv, "--", self)
	return append(argv, args...)
}
