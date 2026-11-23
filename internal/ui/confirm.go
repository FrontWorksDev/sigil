package ui

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// FileRow はコミットに含まれるファイル 1 つ。Status は git の文字（A / M / D / R / T）。
// Path などはコンテナが決めた文字列なので、描くときに Escape する（呼ぶ側は生のまま渡す）。
type FileRow struct {
	Status         byte
	Path, OldPath  string
	Added, Deleted int
	Binary         bool
	Size           int64
}

// WarnRow は「確かめてほしいもの」1 件。
type WarnRow struct{ Path, Reason string }

// HookRow は、コンテナで実行したフックの結果。
type HookRow struct {
	Name string
	OK   bool
}

// CommitView は、コミットの確認の画面に出す内容。
type CommitView struct {
	Branch, HeadShort, HeadSubject string
	Pathspec                       []string
	Files                          []FileRow
	Warnings                       []WarnRow
	Message                        string
	Hooks                          []HookRow
}

// 件名の長さの目安（git の慣習）
const subjectLimit = 72

// statusStyle は、状態の印・言葉・色。
func statusStyle(s byte) (mark, word string, color int) {
	switch s {
	case 'A':
		return "✚", "追加", green
	case 'M':
		return "✎", "変更", yellow
	case 'D':
		return "✖", "削除", red
	case 'R':
		return "➜", "名前の変更", cyan
	case 'T':
		return "✎", "種類の変更", yellow
	}
	return "?", "不明", gray
}

// humanSize は、バイト数を読みやすくする。
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

// bar は、増減の数に比例した棒（最大 10 マス、半マスは ▌）。largest は、全ファイルの増減の最大値。
// 追加の割合のぶんを緑、残りを赤で描く。色がなくても、長さと数で伝わる。
func (u *UI) bar(added, deleted, largest int) string {
	total := added + deleted
	if total == 0 || largest == 0 {
		return ""
	}
	halves := min(20, max(1, (20*total+largest-1)/largest))
	cells := (halves + 1) / 2
	g := added * cells / total
	if added > 0 && g == 0 {
		g = 1
	}
	if deleted > 0 && g == cells && cells > 1 {
		g = cells - 1
	}
	var b strings.Builder
	for i := 0; i < cells; i++ {
		ch := "█"
		if i == cells-1 && halves%2 == 1 {
			ch = "▌"
		}
		if i < g {
			b.WriteString(u.fg(green, ch))
		} else {
			b.WriteString(u.fg(red, ch))
		}
	}
	return b.String()
}

// stat は、1 行の右側（+12 -3 ████▌、バイナリなら大きさ）。
func (u *UI) stat(f FileRow, largest int) string {
	if f.Binary {
		return u.dim("バイナリ " + humanSize(f.Size))
	}
	s := u.fg(green, fmt.Sprintf("+%d", f.Added)) + " " + u.fg(red, fmt.Sprintf("-%d", f.Deleted))
	if b := u.bar(f.Added, f.Deleted, largest); b != "" {
		s += " " + b
	}
	return s
}

// emit は、画面の 1 行を出す。Plain は字下げだけ、それ以外は u.line と同じ 2 桁の字下げ。
// どちらも、端末の右端を越えないよう切り詰める（折り返すと枠や行が崩れる）。
func (u *UI) emit(indent int, s string) {
	io.WriteString(u.w, strings.Repeat(" ", indent)+u.fitStyled(s, u.avail(indent+1))+"\n")
}

// section は見出しと本文を出す。Plain と Color / Mono のどちらでも同じ情報を出す。
// boxColor が 0 でなければ、枠で囲む（Plain では枠なし）。
func (u *UI) section(heading string, lines []string, boxColor int) {
	if u.mode == Plain {
		u.emit(5, heading)
		for _, l := range lines {
			u.emit(7, l)
		}
		return
	}
	if boxColor != 0 {
		u.boxColor(boxColor, append([]string{heading}, lines...)...)
		return
	}
	u.emit(2, heading)
	for _, l := range lines {
		u.emit(4, l)
	}
}

// fileRows は、刻むもの（ファイルの一覧）の本文の行を作る。
func (u *UI) fileRows(files []FileRow) []string {
	largest, wordW := 0, 0
	for _, f := range files {
		largest = max(largest, f.Added+f.Deleted)
		_, w, _ := statusStyle(f.Status)
		wordW = max(wordW, Width(w))
	}
	// 本文は 4 桁（Plain は 7 桁）字下げされる。切り詰めの余裕は、狭いほうの Plain に合わせて 8 桁引く
	rowW := u.avail(8)
	prefixW := 2 + wordW + 1
	var out []string
	for _, f := range files {
		mark, word, color := statusStyle(f.Status)
		path := EscapeLine(f.Path)
		if f.OldPath != "" {
			path = EscapeLine(f.OldPath) + " → " + path
		}
		prefix := u.fg(color, mark+" "+word) + strings.Repeat(" ", wordW-Width(word)) + " "
		stat := u.stat(f, largest)
		pathW := rowW - prefixW - 2 - Width(stat)
		switch {
		case Width(path) <= pathW || pathW >= 20:
			out = append(out, prefix+u.fit(path, pathW)+"  "+stat)
		default:
			// 1 行に収まらないときは、増減を次の行に回す
			out = append(out, prefix+u.fit(path, rowW-prefixW), strings.Repeat(" ", prefixW)+stat)
		}
	}
	return out
}

// messageRows は、銘（コミットメッセージ）の本文の行を作る。
func (u *UI) messageRows(msg string) []string {
	// タブは幅が決まらず枠を崩すので、空白にする
	msg = strings.ReplaceAll(Escape(msg), "\t", "    ")
	lines := strings.Split(strings.Trim(msg, "\n"), "\n")
	out := []string{u.bold(lines[0])}
	if n := utf8.RuneCountInString(lines[0]); n > subjectLimit {
		out = append(out, u.fg(yellow, fmt.Sprintf("⚠ 件名が 72 文字を超えています（%d 文字）", n)))
	}
	for _, l := range lines[1:] {
		if l == "" {
			out = append(out, "")
		} else {
			out = append(out, "  "+l)
		}
	}
	return out
}

// CommitScreen は、コミットの確認の画面（問いの行は除く）を出す。
// 色や枠で飾るが、動きは付けない（待っているあいだは静止させる）。
func (u *UI) CommitScreen(v CommitView) {
	// 1. 見出し
	head := []string{
		"ブランチ  " + EscapeLine(v.Branch),
		"HEAD      " + EscapeLine(v.HeadShort) + " " + EscapeLine(v.HeadSubject),
	}
	if len(v.Pathspec) > 0 {
		var ps []string
		for _, p := range v.Pathspec {
			ps = append(ps, EscapeLine(p))
		}
		head = append(head, "対象      "+strings.Join(ps, " "))
	}
	title := "⟡ 刻印の儀 ── コミットの確認"
	if u.mode == Plain {
		io.WriteString(u.w, "sigil: "+title+"\n")
		for _, l := range head {
			u.emit(5, l)
		}
	} else {
		u.Box(append([]string{u.fg(purple, "⟡") + u.bold(strings.TrimPrefix(title, "⟡"))}, head...)...)
	}

	// 2. 刻むもの
	u.section(u.fg(green, "◆")+" "+u.bold("刻むもの（コミットに含まれるファイル）"), u.fileRows(v.Files), 0)

	// 3. 確かめてほしいもの
	if len(v.Warnings) > 0 {
		var lines []string
		for _, w := range v.Warnings {
			lines = append(lines, u.fg(yellow, "▸ ")+EscapeLine(w.Path), u.dim("    "+EscapeLine(w.Reason)))
		}
		u.section(u.fg(red, "⚠ "+u.bold("確かめてほしいもの")), lines, red)
	}

	// 4. 銘
	u.section(u.fg(purple, "◆")+" "+u.bold("銘（コミットメッセージ）"), u.messageRows(v.Message), 0)

	// 5. 試練
	var hooks []string
	for _, h := range v.Hooks {
		if h.OK {
			hooks = append(hooks, u.fg(green, "✔ ")+EscapeLine(h.Name))
		} else {
			hooks = append(hooks, u.fg(red, "✗ ")+EscapeLine(h.Name))
		}
	}
	hooks = append(hooks, u.dim("ホストでは hooks を実行していません"))
	u.section(u.fg(cyan, "◆")+" "+u.bold("試練（フック）"), hooks, 0)
	io.WriteString(u.w, "\n")
}

// CommitPrompt は問いの行を出す。答えを同じ行に打てるよう、改行は付けない。
func (u *UI) CommitPrompt() {
	io.WriteString(u.w, "sigil: この内容で刻印する（コミットする）なら y、差分を見るなら d を入力してください [y/N/d] ")
}

// PushView は、push の確認に出す内容。
type PushView struct {
	Remote, URL, Branch string
	Ahead               int
}

// PushPrompt は、push 先を見せて問いの行を出す（問いの行に改行は付けない）。
// push 先の名前や URL は、制御文字を処理してから出す。
func (u *UI) PushPrompt(v PushView) {
	lines := []string{
		"リモート      " + EscapeLine(v.Remote),
		"URL           " + EscapeLine(v.URL),
		"ブランチ      " + EscapeLine(v.Branch),
		fmt.Sprintf("送る commit  %d 件", v.Ahead),
	}
	if u.mode == Plain {
		io.WriteString(u.w, "sigil: ⟡ 外界へ解き放つ ── push 先の確認\n")
		for _, l := range lines {
			u.emit(5, l)
		}
	} else {
		u.Box(append([]string{u.fg(purple, "⟡") + " " + u.bold("外界へ解き放つ ── push 先の確認")}, lines...)...)
	}
	io.WriteString(u.w, "sigil: 外界へ解き放つ（push する）なら y を入力してください [y/N] ")
}

// Sealed は、刻印のあとの表示。Color / Mono では、すべて済んだ段階の儀式で魔法陣を
// 明滅させてから結果の枠を出す。lines には外から来た文字列（件名など）が入るので Escape する。
func (u *UI) Sealed(title string, lines ...string) {
	title = EscapeLine(title)
	esc := make([]string, len(lines))
	for i, l := range lines {
		esc[i] = EscapeLine(l)
	}
	if u.mode == Plain {
		io.WriteString(u.w, "sigil: ✔ "+title+"\n")
		for _, l := range esc {
			u.emit(5, l)
		}
		return
	}
	stages := []Stage{
		{Label: "試練に挑む", State: Done},
		{Label: "紋様を写し取る", State: Done},
		{Label: "銘を検める", State: Done},
		{Label: "刻印する", State: Done},
	}
	// 作業は済んでいる。魔法陣は、完了の明滅を見せるためだけに描く
	u.Ritual(title, "", stages, func(io.Writer) error { return nil })
	u.Box(append([]string{u.fg(green, "✔") + " " + u.bold(title)}, esc...)...)
}
