package ui

import (
	"bufio"
	"io"
	"strings"
)

// ReadAnswer は、tty から 1 行読み、末尾の改行（\r\n）を除いて返す。
// 読めなければ空文字列（= 承認しない）。
//
// 呼ぶ側は、同じ tty から続けて何度か答えを読む（d のあとの y など）。呼ぶたびに
// bufio.Reader を作ると、先読みした分の行を捨ててしまうので、tty がすでに
// *bufio.Reader ならそのまま使う。
func ReadAnswer(tty io.Reader) string {
	br, ok := tty.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(tty)
	}
	line, _ := br.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

// IsYes は、答えが承認かを返す。前後の空白は許さない（見た目で紛れないように）。
func IsYes(s string) bool {
	switch s {
	case "y", "Y", "yes", "YES":
		return true
	}
	return false
}
