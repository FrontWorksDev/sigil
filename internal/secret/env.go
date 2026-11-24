package secret

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scheme は、.env の値がどの提供元の参照かを表す。
type Scheme int

const (
	// None は、参照ではない値（アプリの設定や、直接書かれた値）。値は持たない。
	None Scheme = iota
	// Op は op:// 参照（1Password）。
	Op
	// Keychain は keychain://<service> 参照（macOS のキーチェーン）。
	Keychain
)

const keychainPrefix = "keychain://"

// Entry は .env の 1 行。値そのものは持たない。
type Entry struct {
	Name   string
	Ref    string // 参照全体（op://... / keychain://<service>）。秘密ではないので表示してよい。参照でなければ空
	Scheme Scheme
}

// Service は、Keychain の参照から keychain:// を除いた service を返す。
func (e Entry) Service() string {
	if e.Scheme != Keychain {
		return ""
	}
	return strings.TrimPrefix(e.Ref, keychainPrefix)
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// serviceRe は service に使える形。先頭を英数字に限るのは、`-` で始まる値が
// security のオプションとして読まれないようにするため。
var serviceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidService は、キーチェーンの service として使える文字列かを返す。
// 空白・引用符・制御文字・書式文字を含まないので、確認の画面に出しても偽装されず、
// security の引数にしても別の意味に読まれない。
func ValidService(s string) bool {
	return serviceRe.MatchString(s)
}

// ParseEnv は .env のキー名と、op:// / keychain:// 参照を取り出す。
//
// op run は .env の行をすべて sigil 自身の環境変数にする。何が設定されるかを
// 確かめるために、キー名を読む必要がある。値は参照のときだけ返し、
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
		switch {
		case strings.HasPrefix(val, "op://"):
			if !safeRef(val) {
				return nil, fmt.Errorf("%s:%d: %s の 1Password 参照に使えない文字があります", path, i+1, key)
			}
			e.Ref, e.Scheme = val, Op
		case strings.HasPrefix(val, keychainPrefix):
			// 不正な service はエラーに出さない（表示を偽装する文字を含みうるため）
			if !ValidService(strings.TrimPrefix(val, keychainPrefix)) {
				return nil, fmt.Errorf("%s:%d: %s のキーチェーンの参照に使えない文字があります", path, i+1, key)
			}
			e.Ref, e.Scheme = val, Keychain
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

// RenderEnv は、確かめた名前と参照の組だけで、env ファイルの中身を作る。
// 承認のハッシュと、op run に渡す env ファイルの両方に使うので、スキームは問わず並べる。
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
		// 参照は safeRef / ValidService で確かめてあり、引用符やバックスラッシュを含まない
		fmt.Fprintf(&b, "%s=\"%s\"\n", e.Name, e.Ref)
	}
	return []byte(b.String())
}
