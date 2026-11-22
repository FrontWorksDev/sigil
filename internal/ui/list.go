package ui

import (
	"io"
	"strings"
)

// Realm は sigil ls の 1 行（コンテナ）。
type Realm struct {
	Name, State, Status, Project string
}

// Realms は、コンテナの一覧を、状態ごとの印と色を付けて出す（sigil ls）。
// 標準出力に書く UI で呼ぶこと。Plain のときは何も書かない（呼ぶ側が今までどおりの表を出す）。
func (u *UI) Realms(rs []Realm) {
	if u.mode == Plain {
		return
	}
	io.WriteString(u.w, "\n")
	u.line(u.fg(purple, "⟡") + " " + u.bold("展開中の領域") + u.dim("（sigil のコンテナ）"))
	io.WriteString(u.w, "\n")
	if len(rs) == 0 {
		u.line(u.dim("展開中の領域はありません"))
		io.WriteString(u.w, "\n")
		return
	}
	// 外から来た値（コンテナの名前やラベル）なので、制御文字を取り除いてから幅をそろえる
	type row struct{ mark, name, state, status, project string }
	rows := make([]row, len(rs))
	var wn, ws int
	for i, r := range rs {
		mark, state := u.stateMark(r.State)
		rows[i] = row{mark, Sanitize(r.Name), state, Sanitize(r.Status), Sanitize(r.Project)}
		wn = max(wn, Width(rows[i].name))
		ws = max(ws, Width(rows[i].status))
	}
	for _, r := range rows {
		u.line(r.mark + " " + u.bold(pad(r.name, wn)) + "  " + r.state + "  " +
			u.dim(pad(r.status, ws)) + "  " + r.project)
	}
	io.WriteString(u.w, "\n")
}

// stateMark は、docker の状態（{{.State}}）に合う印と、その言葉（幅 6 にそろえたもの）を返す。
func (u *UI) stateMark(state string) (mark, label string) {
	switch state {
	case "running":
		return u.fg(green, "●"), u.fg(green, "展開中")
	case "paused":
		return u.fg(yellow, "◐"), u.fg(yellow, "休止中")
	case "restarting":
		return u.fg(cyan, "◌"), u.fg(cyan, "再展開")
	default: // exited / created / dead など
		return u.fg(gray, "○"), u.fg(gray, "停止中")
	}
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-Width(s)))
}
