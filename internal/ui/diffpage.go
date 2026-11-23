package ui

import (
	"strings"
)

// DiffPage は、git diff の素の出力に色を付けた文字列を返す（ページャーには渡さない。呼ぶ側が行う）。
//
// 差分にはコンテナが書いた内容が入るので、行ごとに Escape をかけてから色を付ける。
// こうすると、残るエスケープは sigil の付けた SGR だけになり、`less -R` に渡してよい。
// Mono / Plain は色を付けない（fg が何もしない）。
func (u *UI) DiffPage(diff string) string {
	trailing := strings.HasSuffix(diff, "\n")
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	var b strings.Builder
	// hunk の中には、中身が "-- " で始まる削除行などもあるので、先頭の文字だけで追加・削除と決める。
	// ファイル名の行（--- / +++）や mode の行は、diff --git から最初の @@ までの間（inHeader）だけで見分ける。
	inHeader := false
	for _, raw := range lines {
		l := Escape(raw)
		switch {
		case strings.HasPrefix(l, "diff --git "):
			inHeader = true
			// 次のファイルの始まり。罫線にして、境目を見つけやすくする
			name := strings.TrimPrefix(l, "diff --git ")
			b.WriteString(u.fg(violet, "──── "+name+" "+strings.Repeat("─", max(4, 72-Width(name)-6))))
		case inHeader && !strings.HasPrefix(l, "@@"):
			// ファイル名や mode などの行は、追加・削除の行と区別して控えめに出す
			b.WriteString(u.dim(l))
		case strings.HasPrefix(l, "\\ "):
			b.WriteString(u.dim(l))
		case strings.HasPrefix(l, "+"):
			b.WriteString(u.fg(green, l))
		case strings.HasPrefix(l, "-"):
			b.WriteString(u.fg(red, l))
		case strings.HasPrefix(l, "@@"):
			inHeader = false
			b.WriteString(u.fg(cyan, l))
		default:
			b.WriteString(l)
		}
		b.WriteString("\n")
	}
	out := b.String()
	if !trailing {
		out = strings.TrimSuffix(out, "\n")
	}
	return out
}
