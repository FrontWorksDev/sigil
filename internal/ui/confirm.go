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
		return "+", "追加", green
	case 'M':
		return "~", "変更", yellow
	case 'D':
		return "-", "削除", red
	case 'R':
		return "→", "名前の変更", cyan
	case 'T':
		return "~", "種類の変更", yellow
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

// ---- 折り返し ----
//
// 判断の画面では、内容を切り詰めない。右端で切ると、メッセージ行の末尾、パスの拡張子（.env）、
// 改名の新しい側、リンク先、push 先のホストなど、肝心の部分が見えなくなりうるため。
// 幅を超える行は、表示幅（全角は 2）で折り返し、続きの行は字下げして 1 つの項目に見せる。

// wrapText は、text を表示幅で折り返す。1 行目は w1、2 行目以降は w2 まで。
// 空白も含めて 1 文字も落とさない（単語の途中でも折る）。
func wrapText(text string, w1, w2 int) []string {
	w1, w2 = max(w1, 2), max(w2, 2)
	var out []string
	var b strings.Builder
	n, limit := 0, w1
	for _, r := range text {
		rw := runeWidth(r)
		if n+rw > limit && n > 0 {
			out = append(out, b.String())
			b.Reset()
			n, limit = 0, w2
		}
		b.WriteRune(r)
		n += rw
	}
	return append(out, b.String())
}

// wrapItem は、1 つの項目を幅 w に折り返した行にする。first は 1 行目の頭（印など）、cont は続きの行の頭。
// style は文字の部分だけに行ごとにかける（色は行ごとに閉じ、折り返しで色が混ざらない）。
func wrapItem(first, cont, text string, w int, style func(string) string) []string {
	if style == nil {
		style = func(s string) string { return s }
	}
	parts := wrapText(text, w-Width(first), w-Width(cont))
	out := make([]string, len(parts))
	for i, p := range parts {
		head := cont
		if i == 0 {
			head = first
		}
		out[i] = head + style(p)
	}
	return out
}

func spaces(n int) string { return strings.Repeat(" ", max(n, 0)) }

// kv は「ラベル  値」の項目。値が長ければ、ラベルの幅だけ字下げして続ける。
func kv(label, text string, w int) []string {
	return wrapItem(label, spaces(Width(label)), text, w, nil)
}

// bodyW は、section の本文に使える幅（emit と枠の余白に合わせる）。
func (u *UI) bodyW(boxed bool) int {
	switch {
	case u.mode == Plain:
		return u.avail(8)
	case boxed:
		return u.avail(7)
	}
	return u.avail(5)
}

// emit は、画面の 1 行を出す。Plain は字下げだけ、それ以外は u.line と同じ 2 桁の字下げ。
// 呼ぶ側が折り返し済みなので、切り詰めは見出しなど固定の文言が溢れたときの保険。
func (u *UI) emit(indent int, s string) {
	io.WriteString(u.w, spaces(indent)+u.fitStyled(s, u.avail(indent+1))+"\n")
}

// section は見出しと本文を出す。Plain と Color / Mono のどちらでも同じ情報を出す。
// body は、使える幅を受け取って、折り返し済みの行を返す。
// boxColor が 0 でなければ、枠で囲む（Plain では枠なし）。
func (u *UI) section(mark string, markColor int, title string, body func(w int) []string, boxColor int) {
	// 見出しも、狭い端末で切れないよう折り返す
	hw := u.bodyW(false) + 2
	if u.mode != Plain {
		hw = u.avail(3)
	}
	headLines := wrapItem(u.fg(markColor, mark)+" ", "  ", title, hw, u.bold)
	lines := body(u.bodyW(boxColor != 0))
	if u.mode == Plain {
		for _, l := range headLines {
			u.emit(5, l)
		}
		for _, l := range lines {
			u.emit(7, l)
		}
		return
	}
	if boxColor != 0 {
		u.boxColor(boxColor, append(wrapItem(u.fg(markColor, mark)+" ", "  ", title, u.avail(7), u.bold), lines...)...)
		return
	}
	for _, l := range headLines {
		u.emit(2, l)
	}
	for _, l := range lines {
		u.emit(4, l)
	}
}

// fileRows は、刻むもの（ファイルの一覧）の本文の行を作る。パスは詰めずに折り返す。
// 改名は、旧と新を別の行にする（新しい側が押し出されて見えなくならないように）。
func (u *UI) fileRows(files []FileRow, w int) []string {
	largest, wordW := 0, 0
	for _, f := range files {
		largest = max(largest, f.Added+f.Deleted)
		_, word, _ := statusStyle(f.Status)
		wordW = max(wordW, Width(word))
	}
	prefixW := 2 + wordW + 1
	var out []string
	for _, f := range files {
		mark, word, color := statusStyle(f.Status)
		prefix := u.fg(color, mark+" "+word) + spaces(wordW-Width(word)) + " "
		cont := spaces(prefixW + 2)
		var lines []string
		if f.OldPath != "" {
			lines = wrapItem(prefix, cont, EscapeLine(f.OldPath), w, nil)
			lines = append(lines, wrapItem(spaces(prefixW)+"→ ", cont, EscapeLine(f.Path), w, nil)...)
		} else {
			lines = wrapItem(prefix, cont, EscapeLine(f.Path), w, nil)
		}
		stat := u.stat(f, largest)
		if last := lines[len(lines)-1]; Width(last)+2+Width(stat) <= w {
			lines[len(lines)-1] = last + "  " + stat
		} else {
			// 増減だけの行は、幅に収まる範囲で字下げする（棒は最大 10 マスなので、切らずに済む）
			lines = append(lines, spaces(min(prefixW+2, w-Width(stat)))+stat)
		}
		out = append(out, lines...)
	}
	return out
}

// messageRows は、銘（コミットメッセージ）の本文の行を作る。長い行は折り返す。
func (u *UI) messageRows(msg string, w int) []string {
	trimmed := strings.Trim(msg, "\n")
	// 件名の長さは、Escape する前（実際にコミットされる文字）で数える
	n := utf8.RuneCountInString(strings.SplitN(trimmed, "\n", 2)[0])
	// タブは幅が決まらず枠を崩すので、空白にする
	lines := strings.Split(strings.ReplaceAll(Escape(trimmed), "\t", "    "), "\n")
	out := wrapItem("", "  ", lines[0], w, u.bold)
	if n > subjectLimit {
		out = append(out, wrapItem("", "  ", fmt.Sprintf("⚠ 件名が 72 文字を超えています（%d 文字）", n), w, func(s string) string { return u.fg(yellow, s) })...)
	}
	for _, l := range lines[1:] {
		if l == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapItem("  ", "    ", l, w, nil)...)
	}
	return out
}

// CommitScreen は、コミットの確認の画面（問いの行は除く）を出す。
// 色や枠で飾るが、動きは付けない（待っているあいだは静止させる）。
// 内容は切り詰めず、端末の幅で折り返す。
func (u *UI) CommitScreen(v CommitView) {
	// 1. 見出し
	hw := u.avail(7)
	if u.mode == Plain {
		hw = u.avail(6)
	}
	head := kv("ブランチ  ", EscapeLine(v.Branch), hw)
	head = append(head, kv("HEAD      ", EscapeLine(v.HeadShort)+" "+EscapeLine(v.HeadSubject), hw)...)
	if len(v.Pathspec) > 0 {
		var ps []string
		for _, p := range v.Pathspec {
			ps = append(ps, EscapeLine(p))
		}
		head = append(head, kv("対象      ", strings.Join(ps, " "), hw)...)
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
	u.section("◆", green, "刻むもの（コミットに含まれるファイル）",
		func(w int) []string { return u.fileRows(v.Files, w) }, 0)

	// 3. 確かめてほしいもの
	if len(v.Warnings) > 0 {
		u.section("⚠", red, "確かめてほしいもの", func(w int) []string {
			var lines []string
			for _, wr := range v.Warnings {
				lines = append(lines, wrapItem(u.fg(yellow, "▸ "), "  ", EscapeLine(wr.Path), w, nil)...)
				lines = append(lines, wrapItem("    ", "    ", EscapeLine(wr.Reason), w, u.dim)...)
			}
			return lines
		}, red)
	}

	// 4. 銘
	u.section("◆", purple, "銘（コミットメッセージ）",
		func(w int) []string { return u.messageRows(v.Message, w) }, 0)

	// 5. 試練
	u.section("◆", cyan, "試練（フック）", func(w int) []string {
		var hooks []string
		for _, h := range v.Hooks {
			mark := u.fg(green, "✔ ")
			if !h.OK {
				mark = u.fg(red, "✗ ")
			}
			hooks = append(hooks, wrapItem(mark, "  ", EscapeLine(h.Name), w, nil)...)
		}
		return append(hooks, wrapItem("", "", "ホストでは hooks を実行していません", w, u.dim)...)
	}, 0)
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
	// push 先は、末尾（ホストやリポジトリ名）が肝心なので、切り詰めずに折り返す
	w := u.avail(7)
	if u.mode == Plain {
		w = u.avail(6)
	}
	var lines []string
	lines = append(lines, kv("リモート      ", EscapeLine(v.Remote), w)...)
	lines = append(lines, kv("URL           ", EscapeLine(v.URL), w)...)
	lines = append(lines, kv("ブランチ      ", EscapeLine(v.Branch), w)...)
	lines = append(lines, kv("送る commit  ", fmt.Sprintf("%d 件", v.Ahead), w)...)
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
	w := u.avail(7)
	if u.mode == Plain {
		w = u.avail(6)
	}
	var esc []string
	for _, l := range lines {
		esc = append(esc, wrapItem("", "  ", EscapeLine(l), w, nil)...)
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
	u.Box(append(wrapItem(u.fg(green, "✔")+" ", "  ", title, w, u.bold), esc...)...)
}
