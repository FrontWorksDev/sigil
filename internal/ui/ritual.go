package ui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
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

// Progress は、裏の処理から段階の進み具合を伝えるためのもの（sigil build など）。
// 裏の goroutine が書き、描画の goroutine が読むので、ロックで守る。
type Progress struct {
	mu     sync.Mutex
	set    bool
	active int     // 回っている段階の番号（これより前は済んだものとして描く）
	note   string  // 回っている段階の添え書き（空なら Stage.Note）
	ratio  float64 // 0〜1。負ならバーを出さない
}

// Set は、回っている段階と添え書き、進み具合（0〜1、負ならバーなし）を設定する。
func (p *Progress) Set(active int, note string, ratio float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.set, p.active, p.note, p.ratio = true, active, note, ratio
}

func (p *Progress) get() (set bool, active int, note string, ratio float64) {
	if p == nil {
		return false, 0, "", -1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.set, p.active, p.note, p.ratio
}

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

// view は、ある時点の段階の一覧（Progress を反映したもの）。
type view struct {
	stages []Stage
	active int
	ratio  float64
}

func current(stages []Stage, p *Progress) view {
	v := view{stages: append([]Stage(nil), stages...), active: len(stages), ratio: -1}
	for i, s := range v.stages {
		if s.State == Pending {
			v.active = i
			break
		}
	}
	if set, active, note, ratio := p.get(); set {
		v.active, v.ratio = min(active, len(stages)), ratio
		for i := 0; i < v.active; i++ {
			if v.stages[i].State == Pending {
				v.stages[i].State = Done
			}
		}
		if v.active < len(stages) && note != "" {
			v.stages[v.active].Note = note
		}
	}
	return v
}

func (v view) label() string {
	if v.active >= len(v.stages) {
		return ""
	}
	s := v.stages[v.active]
	if s.Note == "" {
		return s.Label
	}
	return s.Label + "  " + s.Note
}

// Ritual は、work を裏で動かしながら、左に魔法陣、右に段階の一覧を描き続ける。
// 待ち時間は足さない（work が終わるまでが儀式の長さ）。
// 端末が狭ければ 1 行のスピナーに、Plain なら work を呼ぶだけにする。
func (u *UI) Ritual(title, subtitle string, stages []Stage, work Work) error {
	return u.RitualWithProgress(title, subtitle, stages, nil, work)
}

// RitualWithProgress は Ritual と同じだが、work が p で段階を進められる。
func (u *UI) RitualWithProgress(title, subtitle string, stages []Stage, p *Progress, work Work) error {
	if u.mode == Plain {
		return work(u.w)
	}
	if !u.roomy() {
		return u.spin(func() string {
			if l := current(stages, p).label(); l != "" {
				return l
			}
			return title
		}, work)
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
		u.drawRitual(title, subtitle, current(stages, p), frame, u.now().Sub(start), flash, failed, first)
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
			// 本物のシグナルなら raise で終わる。戻ってきたとき（テストなど）も、裏の処理が
			// 終わるまで待ってから戻る。待たずに戻ると、呼んだ側が docker run の途中で先へ進み、
			// work が書く変数を同時に読むことになる。
			<-done
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
func (u *UI) drawRitual(title, subtitle string, v view, frame int, el time.Duration, flash, failed, first bool) {
	grid := drawCircle(frame, u.glyphs, flash)
	pw := u.cols - 2 - circleW - 3 - 1 // 一覧に使える幅
	panel := make([]string, circleH)
	t := u.fit(title, pw)
	if sub := u.fit(Sanitize(subtitle), pw-Width(t)-5); sub != "" {
		t = u.bold(t) + "  " + u.dim("── "+sub)
	} else {
		t = u.bold(t)
	}
	panel[2] = t
	panel[3] = u.dim(strings.Repeat("─", min(34, pw)))
	row := 5
	for i, s := range v.stages {
		if row >= circleH-2 {
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
		case i == v.active && failed:
			line = u.fg(red, "✗ ") + u.bold(label) + " " + u.dim(note)
		case i == v.active:
			line = u.fg(purple, coreFrames[frame%len(coreFrames)]+" ") + u.bold(label) + " " + u.dim(note)
		default:
			line = u.dim("· " + label)
		}
		panel[row] = line
		row++
	}
	if v.ratio >= 0 && row+1 < circleH-2 {
		ratio := math.Min(v.ratio, 1)
		if flash {
			ratio = 1
		}
		w := min(26, pw-6)
		filled := int(ratio * float64(w))
		panel[row+1] = u.fg(violet, strings.Repeat("▰", filled)) + u.dim(strings.Repeat("▱", w-filled)) +
			u.dim(fmt.Sprintf(" %3.0f%%", ratio*100))
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
	return u.spin(func() string { return label }, work)
}

// spin は Spin の本体。label は描くたびに呼ぶ（Progress で添え書きが変わるため）。
func (u *UI) spin(label func() string, work Work) error {
	fitted := func() string { return u.fit(Sanitize(label()), u.avail(12)) }

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
		fmt.Fprintf(u.w, "\r\x1b[2K  %s %s %s", u.fg(hues[i%len(hues)], g), fitted(),
			u.dim(fmt.Sprintf("%.1fs", u.now().Sub(start).Seconds())))
		select {
		case err = <-done:
			break loop
		case s := <-sig:
			u.Restore()
			u.raise(s)
			<-done // Ritual と同じ理由で、裏の処理を待つ
			return ErrInterrupted
		case <-u.after(spinInterval):
		}
	}
	mark := u.fg(green, "✔")
	if err != nil {
		mark = u.fg(red, "✗")
	}
	fmt.Fprintf(u.w, "\r\x1b[2K  %s %s\n", mark, fitted())
	u.Restore()
	u.w.Write(buf.Bytes())
	return err
}
