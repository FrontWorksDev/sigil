// Package ui は、sigil の演出（魔法陣、スピナー、バナー、枠）を標準エラーに描く。
//
// 演出は標準エラーが端末のときだけ行う。パイプやリダイレクトでは何も描かず、
// 今までどおりの素のテキストを保つ（Plain）。標準出力には決して書かない
// （sigil -- go test ./... | ... のような使い方を壊さないため）。
//
// 外部モジュールは使わず、標準ライブラリと ANSI エスケープだけで書いている
// （このコードはホストで動くため）。
package ui

import (
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Mode は表示のしかた。
type Mode int

const (
	// Plain は演出なし。今までどおりの素のテキスト。
	Plain Mode = iota
	// Mono は動きはあるが、色・太字などの SGR を出さない（NO_COLOR）。
	Mono
	// Color はすべての演出。
	Color
)

const (
	// PlainEnv が空でも "0" でもなければ、演出を止める。
	PlainEnv = "SIGIL_PLAIN"
	// GlyphsEnv が "greek" なら、魔法陣の外周をルーン文字ではなくギリシャ文字にする。
	GlyphsEnv = "SIGIL_GLYPHS"
)

// DetectMode は、標準エラーが端末か、環境変数は何かから、表示モードを決める。
func DetectMode(stderrIsTerminal bool, lookup func(string) (string, bool)) Mode {
	if !stderrIsTerminal {
		return Plain
	}
	if v, ok := lookup(PlainEnv); ok && v != "" && v != "0" {
		return Plain
	}
	if v, _ := lookup("TERM"); v == "dumb" {
		return Plain
	}
	// https://no-color.org/ に従い、空でない値のときだけ色を止める
	if v, ok := lookup("NO_COLOR"); ok && v != "" {
		return Mono
	}
	return Color
}

// RestoreSeq は、端末を元に戻すエスケープ（行の消去、SGR のリセット、カーソルの表示）。
const RestoreSeq = "\r\x1b[2K\x1b[0m\x1b[?25h"

const hideCursor = "\x1b[?25l"

// Options は New に渡す設定。
type Options struct {
	Mode       Mode
	Cols, Rows int  // 端末の大きさ。分からなければ 0
	Greek      bool // 外周をギリシャ文字にする
	// After は time.After の代わり（テストで待たずに進めるため）。nil なら time.After。
	After func(time.Duration) <-chan time.Time
}

// UI は標準エラーへの描画を受け持つ。
// 描画はすべて呼んだ側の goroutine で行い、裏で動かす処理には書かせない（画面が崩れないように）。
type UI struct {
	w          io.Writer
	mode       Mode
	cols, rows int
	glyphs     []rune
	after      func(time.Duration) <-chan time.Time
	now        func() time.Time

	// シグナルの扱い（テストで差し替える）
	notify func(chan<- os.Signal)
	stop   func(chan<- os.Signal)
	raise  func(os.Signal)
}

// New は UI を作る。w は標準エラー。
func New(w io.Writer, o Options) *UI {
	u := &UI{
		w:      w,
		mode:   o.Mode,
		cols:   o.Cols,
		rows:   o.Rows,
		glyphs: runeGlyphs,
		after:  o.After,
		now:    time.Now,
		notify: func(c chan<- os.Signal) { signal.Notify(c, syscall.SIGINT, syscall.SIGTERM) },
		stop:   func(c chan<- os.Signal) { signal.Stop(c) },
		raise:  raiseDefault,
	}
	if o.Greek {
		u.glyphs = greekGlyphs
	}
	if u.after == nil {
		u.after = time.After
	}
	return u
}

// raiseDefault は、シグナルの扱いを既定に戻してから自分に送り直す。
// 端末を元に戻したあと、Ctrl-C で終わるという本来の動作にするため。
func raiseDefault(s os.Signal) {
	sig, ok := s.(syscall.Signal)
	if !ok {
		return
	}
	signal.Reset(sig)
	syscall.Kill(syscall.Getpid(), sig)
}

// Fancy は演出をするか（Plain でないか）を返す。
func (u *UI) Fancy() bool { return u.mode != Plain }

// Restore は端末を元に戻す。docker exec / op run に置き換える直前に必ず呼ぶこと。
// Plain のときは何も書かない（素のテキストにエスケープを混ぜないため）。
func (u *UI) Restore() {
	if u.mode == Plain {
		return
	}
	io.WriteString(u.w, RestoreSeq)
}

// Enter は、既存のコンテナに入るときの 1 行を出す。待ち時間は足さない。
func (u *UI) Enter(name string) {
	if u.mode == Plain {
		return
	}
	u.line(u.fg(purple, "⟡") + " 領域 " + u.bold(u.fit(Sanitize(name), u.cols-16)) + " に入ります")
}

// Success は、済んだことを知らせる 1 行を出す。msg は Bold などで飾ってよいが、
// 外から来た文字列は Sanitize してから渡すこと。
func (u *UI) Success(msg string) {
	if u.mode == Plain {
		return
	}
	io.WriteString(u.w, "\n")
	u.line(u.fg(green, "✔") + " " + msg)
	io.WriteString(u.w, "\n")
}

// Entering は、作ったばかりのコンテナに入るときの 1 行を出す。
func (u *UI) Entering() {
	if u.mode == Plain {
		return
	}
	u.line(u.fg(purple, "⟡") + " 領域へ入ります…")
	io.WriteString(u.w, "\n")
}

func (u *UI) line(s string) {
	io.WriteString(u.w, "  "+s+"\n")
}

// ---- バナーと枠 ----

var banner = []string{
	"███████╗██╗ ██████╗ ██╗██╗     ",
	"██╔════╝██║██╔════╝ ██║██║     ",
	"███████╗██║██║  ███╗██║██║     ",
	"╚════██║██║██║   ██║██║██║     ",
	"███████║██║╚██████╔╝██║███████╗",
	"╚══════╝╚═╝ ╚═════╝ ╚═╝╚══════╝",
}

// 紫 → 水色のグラデーション（256 色）
var gradient = []int{93, 99, 105, 111, 117, 123}

// Banner は SIGIL のバナーを出す。端末が狭ければ出さない。
// 一行ずつ間をあけて出すことはしない（起動を遅くしないため）。
func (u *UI) Banner() {
	if u.mode == Plain || u.cols < 40 {
		return
	}
	io.WriteString(u.w, "\n")
	for i, l := range banner {
		u.line(u.fg(gradient[i], l))
	}
	u.line(u.dim("      ── seal your code, guard your keys ──"))
	io.WriteString(u.w, "\n")
}

// Box は lines を枠で囲んで出す。lines は Bold などで飾ってよいが、
// 外から来た文字列は Sanitize してから渡すこと。
func (u *UI) Box(lines ...string) {
	if u.mode == Plain {
		return
	}
	w := 0
	for _, l := range lines {
		w = max(w, Width(l))
	}
	u.line(u.fg(violet, "╭"+strings.Repeat("─", w+2)+"╮"))
	for _, l := range lines {
		u.line(u.fg(violet, "│") + " " + l + strings.Repeat(" ", w-Width(l)) + " " + u.fg(violet, "│"))
	}
	u.line(u.fg(violet, "╰"+strings.Repeat("─", w+2)+"╯"))
}

// ---- 文字の飾り ----

// 256 色の番号
const (
	purple = 141
	violet = 99
	cyan   = 87
	green  = 114
	red    = 203
	yellow = 221
	gray   = 245
	white  = 231
	edge   = 61
)

func (u *UI) fg(n int, s string) string {
	if u.mode != Color || n == 0 {
		return s
	}
	return "\x1b[38;5;" + itoa(n) + "m" + s + "\x1b[0m"
}

func (u *UI) sgr(code, s string) string {
	if u.mode != Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (u *UI) bold(s string) string { return u.sgr("1", s) }
func (u *UI) dim(s string) string  { return u.sgr("2", s) }

// Bold / Dim / Accent は、Box に渡す行を飾るためのもの。
func (u *UI) Bold(s string) string   { return u.bold(s) }
func (u *UI) Dim(s string) string    { return u.dim(s) }
func (u *UI) Accent(s string) string { return u.fg(cyan, s) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ---- 文字列の幅 ----

// Sanitize は、制御文字（C0、DEL、C1）を取り除く。
// フォルダ名などに埋め込まれたエスケープで、端末を操作されないようにするため。
func Sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// Width は、端末で表示したときの幅を返す。SGR のエスケープは数えない。
// 東アジアの全角の文字を 2、それ以外を 1 とする（ルーン文字や罫線は 1）。
func Width(s string) int {
	n := 0
	for _, r := range stripSGR(s) {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F, r >= 0xFF00 && r <= 0xFF60, r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	}
	return 1
}

// stripSGR は "\x1b[...m" の形のエスケープを取り除く。
func stripSGR(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fit は、飾りのない s を幅 w に収める。はみ出すときは末尾を … にする。
// 端末の右端を越えると折り返され、カーソルを戻して描き直す位置がずれるため。
func (u *UI) fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if Width(s) <= w {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		rw := runeWidth(r)
		if n+rw > w-1 {
			break
		}
		b.WriteRune(r)
		n += rw
	}
	return b.String() + "…"
}
