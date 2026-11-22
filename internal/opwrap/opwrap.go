// Package opwrap は、1Password CLI（op run）を経由して sigil を起動し直すための判定と argv を作る。
//
// sigil 自身は秘密の値を扱わない。.env からはキー名と op:// 参照（どの項目か）だけを取り出し、
// 値の解決は op run に任せ、環境変数として sigil に渡してもらう。
package opwrap

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InnerEnv は、op run から起動し直された側であることを示す環境変数。
// これがあれば、承認の確認と op run をもう一度行わない（無限ループ防止）。
const InnerEnv = "SIGIL_INNER"

// HasOpRef は、.env の内容に op:// 参照があるかを返す。
func HasOpRef(data []byte) bool {
	return bytes.Contains(data, []byte("op://"))
}

// Entry は .env の 1 行。値そのものは持たない。
type Entry struct {
	Name string
	Ref  string // 値が op:// 参照ならその参照（秘密ではないので表示してよい）。それ以外は空
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ParseEnv は .env のキー名と op:// 参照を取り出す。
//
// op run は .env の行をすべて sigil 自身の環境変数にする。何が設定されるかを
// 確かめるために、キー名を読む必要がある。値は参照（op://...）のときだけ返し、
// それ以外の値（直接書かれた秘密かもしれない）は返さず、エラーにも出さない。
func ParseEnv(path string, data []byte) ([]Entry, error) {
	var out []Entry
	// エディタによっては先頭に UTF-8 の BOM が付くので取り除く
	text := strings.TrimPrefix(string(data), "\ufeff")
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: KEY=VALUE の形ではありません", path, i+1)
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if !envNameRe.MatchString(key) {
			return nil, fmt.Errorf("%s:%d: 環境変数名が不正です", path, i+1)
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		e := Entry{Name: key}
		if strings.HasPrefix(val, "op://") {
			if !safeRef(val) {
				return nil, fmt.Errorf("%s:%d: %s の 1Password 参照に使えない文字があります", path, i+1, key)
			}
			e.Ref = val
		}
		out = append(out, e)
	}
	return out, nil
}

// safeRef は、参照を確認の画面に表示し、RenderEnv で書き出しても安全かを返す。
//   - 表示されない文字（ESC などの制御文字、U+202E などの書式文字）は、端末の表示を書き換えたり
//     並べ替えたりして確認の画面を偽装できるので拒否する
//   - 引用符・バックスラッシュ・$ は、op run の env ファイルの解釈（引用や変数の展開）を変えうるので拒否する
//
// 日本語の項目名や空白は使えるようにする。
func safeRef(ref string) bool {
	for _, r := range ref {
		switch {
		case r == ' ', r == '　':
			// 半角と全角の空白は、項目名に使われることがあり、見た目にも分かるので許す
		case r == utf8.RuneError:
			return false
		case !unicode.IsPrint(r):
			// 制御文字（ESC など）に加えて、表示を並べ替える文字（U+202E など）や
			// 幅のない文字（U+200B など）も、確認の画面で別の参照に見せかけられるので拒否する
			return false
		case r == '"', r == '\'', r == '\\', r == '$', r == '`':
			return false
		}
	}
	return true
}

// RenderEnv は、確かめた名前と参照の組だけで、op run に渡す env ファイルの中身を作る。
//
// 利用者の .env を op run にそのまま渡さないのは、次の理由から:
//   - sigil が確かめたあとで .env を差し替えられると、確かめていない内容が使われる
//   - sigil と op run で .env の解釈（改行、引用符など）がずれると、別の名前が設定されうる
//   - .env にはアプリの設定（VITE_API_URL など）も並ぶが、それらを sigil の環境に入れる必要はない
func RenderEnv(entries []Entry) []byte {
	if len(entries) == 0 {
		return nil
	}
	var b strings.Builder
	for _, e := range entries {
		// 参照は safeRef で確かめてあり、引用符やバックスラッシュを含まない
		fmt.Fprintf(&b, "%s=\"%s\"\n", e.Name, e.Ref)
	}
	return []byte(b.String())
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
