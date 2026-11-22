package ui

import (
	"math"
	"strings"
)

const (
	circleW = 33
	circleH = 15
)

// 外周の文字。どちらも幅 1 の文字だけを使う（幅 2 だと輪が崩れる）。
var (
	runeGlyphs  = []rune("ᚠᚢᚦᚨᚱᚲᚷᚹᚺᚾᛁᛃᛇᛈᛉᛊᛏᛒᛖᛗᛚᛜᛞᛟ") // 古フサルク 24 文字
	greekGlyphs = []rune("ΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟΠΡΣΤΥΦΧΨΩ")
)

var (
	hues       = []int{93, 99, 105, 111, 117, 123, 159, 123, 117, 111, 105, 99} // 紫 → 水色 → 紫
	pulse      = []int{93, 99, 135, 141, 177, 141, 135, 99}
	coreFrames = []string{"✶", "✷", "✸", "✹", "✺", "✹", "✸", "✷"}
)

type cell struct {
	ch  string
	col int // 0 なら色なし
}

// drawCircle は、フレーム t の魔法陣を返す（circleH 行 × circleW 桁）。
// flash なら全体を白く光らせる（完了の瞬間）。
func drawCircle(t int, glyphs []rune, flash bool) [][]cell {
	g := make([][]cell, circleH)
	for y := range g {
		g[y] = make([]cell, circleW)
		for x := range g[y] {
			g[y][x] = cell{" ", 0}
		}
	}
	cx, cy := float64(circleW/2), float64(circleH/2)
	color := func(c int) int {
		if flash {
			return white
		}
		return c
	}
	put := func(a, rx, ry float64, ch string, col int) {
		x := int(math.Round(cx + rx*math.Cos(a)))
		y := int(math.Round(cy + ry*math.Sin(a)))
		if y >= 0 && y < circleH && x >= 0 && x < circleW {
			g[y][x] = cell{ch, color(col)}
		}
	}
	rot := float64(t) * 0.06

	// 外周: 文字の輪が時計回りに回り、色が流れる
	n := len(glyphs)
	for i := range n {
		a := 2*math.Pi*float64(i)/float64(n) + rot
		put(a, 15, 7, string(glyphs[i]), hues[(i+t/2)%len(hues)])
	}
	// 外周の内側の点線: 反時計回り
	for i := range 36 {
		a := 2*math.Pi*float64(i)/36 - rot*1.5
		ch := "·"
		if i%3 == 0 {
			ch = "∙"
		}
		put(a, 12, 5.6, ch, violet)
	}
	// 六芒星の頂点: ゆっくり回る
	for i := range 6 {
		a := 2*math.Pi*float64(i)/6 + rot*0.5
		put(a, 8, 3.8, "✦", cyan)
	}
	// 六芒星の辺（頂点を 1 つおきに結ぶ 2 つの三角形）
	for tri := range 2 {
		for k := range 3 {
			a1 := 2*math.Pi*float64(tri+2*k)/6 + rot*0.5
			a2 := 2*math.Pi*float64(tri+2*(k+1))/6 + rot*0.5
			x1, y1 := cx+8*math.Cos(a1), cy+3.8*math.Sin(a1)
			x2, y2 := cx+8*math.Cos(a2), cy+3.8*math.Sin(a2)
			for s := 1; s < 10; s++ {
				f := float64(s) / 10
				x := int(math.Round(x1 + (x2-x1)*f))
				y := int(math.Round(y1 + (y2-y1)*f))
				if g[y][x].ch == " " {
					g[y][x] = cell{"⋅", color(edge)}
				}
			}
		}
	}
	// 中心: 鼓動する紋章
	g[int(cy)][int(cx)] = cell{coreFrames[t%len(coreFrames)], color(pulse[t%len(pulse)])}
	return g
}

func (u *UI) renderRow(row []cell) string {
	var b strings.Builder
	for _, c := range row {
		if c.ch == " " {
			b.WriteString(c.ch)
		} else {
			b.WriteString(u.fg(c.col, c.ch))
		}
	}
	return b.String()
}
