package ui

import (
	"fmt"
	"strings"
	"unicode"
)

// Escape は、端末に出しても画面を操作されないよう、制御文字や見えない書式文字を
// 見える形に直す。Sanitize が取り除くのに対し、こちらは何があったかを残す
// （git の出力やファイル名など、外から来た文字列をそのまま見せたい場面用）。
// 改行とタブは、複数行の出力を壊さないためそのまま残す。
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20:
			// ^@ 〜 ^_
			b.WriteByte('^')
			b.WriteByte(byte(r) + '@')
		case r == 0x7f:
			b.WriteString("^?")
		case !unicode.IsPrint(r) && (!unicode.IsSpace(r) || r == 0x85):
			// C1 や U+202E（向きの上書き）、U+200B（幅ゼロ）など。見た目を偽装できるため明示する
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EscapeLine は Escape に加えて、改行とタブも見える形にする。
// ファイル名のように 1 行で出すもの用。
func EscapeLine(s string) string {
	return strings.NewReplacer("\n", "^J", "\t", "^I").Replace(Escape(s))
}
