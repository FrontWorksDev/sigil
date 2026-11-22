package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// StageState は段階の状態。
type StageState int

const (
	// Pending はまだ済んでいない段階。最初の Pending が、work の実行中に回る段階になる。
	Pending StageState = iota
	// Done は済んだ段階。
	Done
	// Skipped は行わなかった段階（op run を使わないときの「禁書庫から召喚する」など）。
	Skipped
)

// Stage は儀式の一段階。
type Stage struct {
	Label string
	Note  string // 添え書き（.sigil / .env、cap-drop=ALL など）
	State StageState
}

// Work は、演出のあいだ裏で動かす処理。stderr には docker などの標準エラーを渡すこと。
// 演出中はバッファで、描画を終えてから表示する（描画中の画面が崩れないように）。
// Plain のときは、本物の標準エラーがそのまま渡る。
type Work func(stderr io.Writer) error

// ErrInterrupted は、描画中に SIGINT / SIGTERM を受けたときのエラー。
// 本物のシグナルなら、その前にプロセスが終わる。
var ErrInterrupted = errors.New("中断しました")

const (
	frameInterval = 70 * time.Millisecond
	flashInterval = 90 * time.Millisecond
	spinInterval  = 60 * time.Millisecond
	// 魔法陣と段階の一覧を並べるのに要る幅（字下げ 2 + 魔法陣 33 + 間 3 + 一覧 36 + 右端 1）
	minRitualCols = 75
)

// roomy は、魔法陣を描ける大きさかを返す。
// 高さが足りないと、描き直すときにカーソルを戻しきれず、画面が流れてしまう。
func (u *UI) roomy() bool {
	return u.cols >= minRitualCols && u.rows >= circleH+2
}

// Ritual は、work を裏で動かしながら、左に魔法陣、右に段階の一覧を描き続ける。
// 待ち時間は足さない（work が終わるまでが儀式の長さ）。
// 端末が狭ければ 1 行のスピナーに、Plain なら work を呼ぶだけにする。
func (u *UI) Ritual(title, subtitle string, stages []Stage, work Work) error {
	if u.mode == Plain {
		return work(u.w)
	}
	active := len(stages)
	for i, s := range stages {
		if s.State == Pending {
			active = i
			break
		}
	}
	if !u.roomy() {
		label := title
		if active < len(stages) {
			label = stages[active].Label + "  " + stages[active].Note
		}
		return u.Spin(label, work)
	}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- work(&buf) }()

	sig := make(chan os.Signal, 1)
	u.notify(sig)
	defer u.stop(sig)

	io.WriteString(u.w, hideCursor)
	start := u.now()
	frame := 0
	first := true
	draw := func(flash bool, failed bool) {
		u.drawRitual(title, subtitle, stages, active, frame, u.now().Sub(start), flash, failed, first)
		first = false
	}

	var err error
loop:
	for {
		draw(false, false)
		select {
		case err = <-done:
			break loop
		case s := <-sig:
			u.Restore()
			u.raise(s)
			return ErrInterrupted
		case <-u.after(frameInterval):
		}
		frame++
	}
	if err != nil {
		draw(false, true)
		u.Restore()
		u.w.Write(buf.Bytes())
		return err
	}
	// 完了の瞬間に、魔法陣が白く明滅する
	for range 3 {
		draw(true, false)
		<-u.after(flashInterval)
		draw(false, false)
		frame++
		<-u.after(flashInterval)
	}
	draw(true, false)
	u.Restore()
	io.WriteString(u.w, "\n")
	u.w.Write(buf.Bytes())
	return nil
}

// drawRitual は魔法陣と段階の一覧を 1 フレーム描く。2 回目からは、カーソルを戻して上書きする。
func (u *UI) drawRitual(title, subtitle string, stages []Stage, active, frame int, el time.Duration, flash, failed, first bool) {
	grid := drawCircle(frame, u.glyphs, flash)
	pw := u.cols - 2 - circleW - 3 - 1 // 一覧に使える幅
	panel := make([]string, circleH)
	t := u.fit(title, pw)
	if sub := u.fit(subtitle, pw-Width(t)-5); sub != "" {
		t = u.bold(t) + "  " + u.dim("── "+sub)
	} else {
		t = u.bold(t)
	}
	panel[2] = t
	panel[3] = u.dim(strings.Repeat("─", min(34, pw)))
	for i, s := range stages {
		if 5+i >= circleH-2 {
			break
		}
		label := u.fit(Sanitize(s.Label), pw-2)
		note := u.fit(Sanitize(s.Note), pw-2-Width(label)-1)
		var line string
		switch {
		case s.State == Skipped:
			line = u.dim("－ " + label + " （なし）")
		case s.State == Done || flash: // 明滅するのは、すべて済んだとき
			line = u.fg(green, "✔ ") + label
		case i == active && failed:
			line = u.fg(red, "✗ ") + u.bold(label) + " " + u.dim(note)
		case i == active:
			line = u.fg(purple, coreFrames[frame%len(coreFrames)]+" ") + u.bold(label) + " " + u.dim(note)
		default:
			line = u.dim("· " + label)
		}
		panel[5+i] = line
	}
	panel[circleH-2] = u.dim(fmt.Sprintf("経過 %.1fs", el.Seconds()))

	var b strings.Builder
	if !first {
		fmt.Fprintf(&b, "\x1b[%dA", circleH)
	}
	for y := range circleH {
		b.WriteString("\r\x1b[2K  " + u.renderRow(grid[y]) + "   " + panel[y] + "\n")
	}
	io.WriteString(u.w, b.String())
}

// Spin は、work を裏で動かしながら 1 行のスピナーを描く。終わったら ✔ か ✗ の 1 行を残す。
// Plain なら work を呼ぶだけにする。
func (u *UI) Spin(label string, work Work) error {
	if u.mode == Plain {
		return work(u.w)
	}
	label = u.fit(Sanitize(label), u.cols-12)

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- work(&buf) }()

	sig := make(chan os.Signal, 1)
	u.notify(sig)
	defer u.stop(sig)

	io.WriteString(u.w, hideCursor)
	start := u.now()
	var err error
loop:
	for i := 0; ; i++ {
		g := string(u.glyphs[i%len(u.glyphs)])
		fmt.Fprintf(u.w, "\r\x1b[2K  %s %s %s", u.fg(hues[i%len(hues)], g), label,
			u.dim(fmt.Sprintf("%.1fs", u.now().Sub(start).Seconds())))
		select {
		case err = <-done:
			break loop
		case s := <-sig:
			u.Restore()
			u.raise(s)
			return ErrInterrupted
		case <-u.after(spinInterval):
		}
	}
	mark := u.fg(green, "✔")
	if err != nil {
		mark = u.fg(red, "✗")
	}
	fmt.Fprintf(u.w, "\r\x1b[2K  %s %s\n", mark, label)
	u.Restore()
	u.w.Write(buf.Bytes())
	return err
}
