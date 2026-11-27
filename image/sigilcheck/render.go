package main

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

type palette struct{ ok, ng, warn, head, dim, bold, off string }

func newPalette(color bool) palette {
	if !color {
		return palette{}
	}
	return palette{
		ok: "\x1b[38;5;114m", ng: "\x1b[38;5;203m", warn: "\x1b[38;5;221m", head: "\x1b[38;5;141m",
		dim: "\x1b[2m", bold: "\x1b[1m", off: "\x1b[0m",
	}
}

// render は、点検の結果を出す。失敗が 1 つでもあれば true を返す。
// 本文はすべて clean を通し、端末に届くエスケープは、ここで付ける色だけにする
// （出力はホストの端末にそのまま流れる。設計書 16 章）。
func render(w io.Writer, r report, color bool) (failed bool) {
	c := newPalette(color)
	fmt.Fprintf(w, "\n  %s⟡ 結界の点検%s %s（コンテナの隔離が効いているかの確認）%s\n", c.bold, c.off, c.dim, c.off)
	for _, sec := range r {
		// 見出し: 世界観の言葉に、普通の言葉での意味を添える
		fmt.Fprintf(w, "\n%s■ %s%s %s── %s%s\n", c.head, clean(sec.title, 0), c.off, c.dim, clean(sec.sub, 0), c.off)
		for _, it := range sec.items {
			text := clean(it.text, 0)
			switch it.kind {
			case kindOK:
				fmt.Fprintf(w, "  %s✓%s %s\n", c.ok, c.off, text)
			case kindNG:
				fmt.Fprintf(w, "  %s✗%s %s\n", c.ng, c.off, text)
			case kindWarn:
				fmt.Fprintf(w, "  %s!%s %s\n", c.warn, c.off, text)
			case kindNote:
				fmt.Fprintf(w, "    %s\n", text)
			case kindDimNote:
				fmt.Fprintf(w, "    %s%s%s\n", c.dim, text, c.off)
			case kindTool:
				if it.warn {
					text = c.warn + text + c.off
				}
				fmt.Fprintf(w, "    %-12s %s\n", clean(it.tool, 0), text)
			}
		}
	}

	pass, fail, warns := r.counts()
	fmt.Fprintln(w)
	note := ""
	if warns > 0 {
		note = fmt.Sprintf("、注意: %d", warns)
	}
	if fail == 0 {
		fmt.Fprintf(w, "%s✔ 結界は完全です%s %s── すべて OK (%d 項目%s)%s\n", c.ok, c.off, c.dim, pass, note, c.off)
		return false
	}
	fmt.Fprintf(w, "%s✗ 結界に綻びがあります%s ── %d 項目に問題あり（OK: %d%s）\n", c.ng, c.off, fail, pass, note)
	return true
}

// clean は、外から来る文字列を 1 行の表示用にする。不正な UTF-8 を置き換え、制御文字（C0 / C1 / DEL。
// 改行やタブを含む）と双方向の書式文字を取り除く。max > 0 なら、その文字数で切って … を付ける。
func clean(s string, max int) string {
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || isBidi(r) {
			continue
		}
		if max > 0 && n == max {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// isBidi は、表示の順番を入れ替える書式文字か（見た目と中身を食い違わせられる）。
func isBidi(r rune) bool {
	switch {
	case r == 0x061C, r == 0x200E, r == 0x200F:
		return true
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// firstLine は、最初の行（\r と \n の手前まで）を返す。
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
