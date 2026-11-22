package ui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestDetectMode(t *testing.T) {
	tests := []struct {
		name string
		term bool
		vars map[string]string
		want Mode
	}{
		{"端末", true, nil, Color},
		{"パイプやリダイレクト", false, nil, Plain},
		{"端末でなければ NO_COLOR でも Plain", false, map[string]string{"NO_COLOR": "1"}, Plain},
		{"NO_COLOR", true, map[string]string{"NO_COLOR": "1"}, Mono},
		{"空の NO_COLOR は無視", true, map[string]string{"NO_COLOR": ""}, Color},
		{"SIGIL_PLAIN=1", true, map[string]string{"SIGIL_PLAIN": "1"}, Plain},
		{"SIGIL_PLAIN=0 は無視", true, map[string]string{"SIGIL_PLAIN": "0"}, Color},
		{"空の SIGIL_PLAIN は無視", true, map[string]string{"SIGIL_PLAIN": ""}, Color},
		{"SIGIL_PLAIN は NO_COLOR より優先", true, map[string]string{"SIGIL_PLAIN": "1", "NO_COLOR": "1"}, Plain},
		{"TERM=dumb", true, map[string]string{"TERM": "dumb"}, Plain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectMode(tt.term, env(tt.vars)); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// fired は、すぐに受け取れるチャネルを返す（テストで待たずに進めるため）。
func fired(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- time.Time{}
	return c
}

func newTest(mode Mode, cols, rows int) (*UI, *bytes.Buffer) {
	var buf bytes.Buffer
	u := New(&buf, Options{Mode: mode, Cols: cols, Rows: rows, After: fired})
	u.notify = func(chan<- os.Signal) {}
	u.stop = func(chan<- os.Signal) {}
	u.raise = func(os.Signal) {}
	return u, &buf
}

var stages = []Stage{
	{Label: "契約を読み解く", Note: ".sigil / .env", State: Done},
	{Label: "禁書庫から召喚する", Note: "1Password", State: Skipped},
	{Label: "領域を展開する", Note: "sigil-proj-0123456789abcdef-very-long-name"},
}

func TestPlainWritesNothing(t *testing.T) {
	u, buf := newTest(Plain, 120, 40)
	u.Banner()
	u.Enter("proj")
	u.Entering()
	u.Box("x")
	u.Restore()
	var got io.Writer
	err := u.Ritual("領域展開", "proj", stages, func(w io.Writer) error {
		got = w
		io.WriteString(w, "docker の警告\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Plain では、裏の処理に本物の標準エラーがそのまま渡り、演出は何も書かない
	if got != io.Writer(buf) {
		t.Errorf("Plain の work には、本物の標準エラーを渡すこと")
	}
	if buf.String() != "docker の警告\n" {
		t.Errorf("Plain の出力 = %q", buf.String())
	}
}

func TestMonoHasNoSGR(t *testing.T) {
	u, buf := newTest(Mono, 120, 40)
	u.Banner()
	u.Ritual("領域展開", "proj", stages, func(io.Writer) error { return nil })
	u.Box("領域 "+u.Bold("proj")+" は展開されました", u.Dim("門"))
	if regexpSGR(buf.String()) {
		t.Errorf("Mono で色などの SGR が出ている: %q", buf.String())
	}
	// 動き（カーソルの移動）はある
	if !strings.Contains(buf.String(), "\x1b[?25l") {
		t.Errorf("Mono でもカーソルを隠して描くこと")
	}
}

// regexpSGR は、リセット（\x1b[0m）以外の SGR があるかを返す。
func regexpSGR(s string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], "\x1b[")
		if j < 0 {
			return false
		}
		i += j + 2
		k := strings.IndexAny(s[i:], "ABCDHJKlhm")
		if k >= 0 && s[i+k] == 'm' && s[i:i+k] != "0" {
			return true
		}
	}
}

func TestRitualLinesFitWidth(t *testing.T) {
	for _, cols := range []int{75, 80, 120} {
		u, buf := newTest(Color, cols, 40)
		if err := u.Ritual("領域展開", "とても長いプロジェクトの名前とても長いプロジェクトの名前", stages, func(io.Writer) error { return nil }); err != nil {
			t.Fatal(err)
		}
		for l := range strings.SplitSeq(buf.String(), "\n") {
			l = stripCursor(l)
			if w := Width(l); w >= cols {
				t.Errorf("幅 %d で、%d 桁の行がある（折り返して描き直しがずれる）: %q", cols, w, l)
			}
		}
	}
}

// stripCursor は、行頭のカーソル操作（\r、\x1b[2K、\x1b[nA など）を取り除く。
func stripCursor(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			continue
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '?' || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] != 'm' {
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestRitualEndsRestoredAndShowsBufferedStderr(t *testing.T) {
	u, buf := newTest(Color, 120, 40)
	err := u.Ritual("領域展開", "proj", stages, func(w io.Writer) error {
		io.WriteString(w, "WARNING: docker から\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	r := strings.LastIndex(out, RestoreSeq)
	d := strings.Index(out, "WARNING: docker から")
	if r < 0 || d < 0 || d < r {
		t.Errorf("端末を元に戻してから、ためていた標準エラーを出すこと: %q", out)
	}
	if !strings.Contains(out, "✔") {
		t.Errorf("済んだ印がない")
	}
}

func TestRitualFailure(t *testing.T) {
	u, buf := newTest(Color, 120, 40)
	want := errors.New("docker run が失敗")
	err := u.Ritual("領域展開", "proj", stages, func(w io.Writer) error {
		io.WriteString(w, "Error response from daemon\n")
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "✗") || !strings.HasSuffix(out, RestoreSeq+"Error response from daemon\n") {
		t.Errorf("失敗の印、端末の復元、docker の標準エラーの順で出すこと: %q", out)
	}
}

func TestNarrowFallsBackToSpinner(t *testing.T) {
	for _, size := range [][2]int{{74, 40}, {120, 10}} {
		u, buf := newTest(Color, size[0], size[1])
		u.Ritual("領域展開", "proj", stages, func(io.Writer) error { return nil })
		if strings.Contains(buf.String(), "\x1b[15A") || strings.Contains(buf.String(), "経過") {
			t.Errorf("%v で魔法陣を描いている", size)
		}
		if !strings.Contains(buf.String(), "領域を展開する") {
			t.Errorf("%v でスピナーの行がない: %q", size, buf.String())
		}
	}
}

func TestSpin(t *testing.T) {
	u, buf := newTest(Color, 80, 24)
	if err := u.Spin("領域を再展開する", func(w io.Writer) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(buf.String(), "領域を再展開する\n"+RestoreSeq) {
		t.Errorf("出力 = %q", buf.String())
	}
}

func TestSignalRestoresAndRaises(t *testing.T) {
	u, buf := newTest(Color, 120, 40)
	sigc := make(chan chan<- os.Signal, 1)
	u.notify = func(c chan<- os.Signal) { sigc <- c }
	// 時間は進めず、シグナルが来るまで待たせる
	u.after = func(time.Duration) <-chan time.Time { return nil }
	var raised os.Signal
	u.raise = func(s os.Signal) {
		if !strings.HasSuffix(buf.String(), RestoreSeq) {
			t.Errorf("送り直す前に、端末を元に戻すこと")
		}
		raised = s
	}
	release := make(chan struct{})
	defer close(release)
	go func() { (<-sigc) <- syscall.SIGINT }()
	err := u.Ritual("領域展開", "proj", stages, func(io.Writer) error { <-release; return nil })
	if !errors.Is(err, ErrInterrupted) || raised != syscall.SIGINT {
		t.Errorf("err = %v, raised = %v", err, raised)
	}
}

func TestSanitizeAndFit(t *testing.T) {
	if got := Sanitize("a\x1b]0;x\x07b\u009bc"); got != "a]0;xbc" {
		t.Errorf("Sanitize = %q", got)
	}
	u, _ := newTest(Color, 80, 24)
	if got := u.fit("領域を展開する", 7); got != "領域を…" || Width(got) > 7 {
		t.Errorf("fit = %q", got)
	}
	if got := u.fit("abc", 3); got != "abc" {
		t.Errorf("fit = %q", got)
	}
	if Width(u.Bold("領域")+"ᚠ") != 5 {
		t.Errorf("Width は SGR を数えず、全角を 2、ルーン文字を 1 とする")
	}
}

func TestEnterAndGreek(t *testing.T) {
	var buf bytes.Buffer
	u := New(&buf, Options{Mode: Mono, Cols: 80, Rows: 24, Greek: true, After: fired})
	u.Enter("my\x1bproj")
	if buf.String() != "  ⟡ 領域 myproj に入ります\n" {
		t.Errorf("Enter = %q", buf.String())
	}
	if u.glyphs[0] != 'Α' {
		t.Errorf("Greek のとき外周はギリシャ文字")
	}
}

func TestRitualWithProgress(t *testing.T) {
	u, buf := newTest(Mono, 120, 40)
	st := []Stage{{Label: "素材を集める"}, {Label: "紋章を鋳造する"}, {Label: "紋章を刻む", Note: "sigil:latest"}}
	p := &Progress{}
	err := u.RitualWithProgress("紋章の鋳造", "sigil:latest", st, p, func(io.Writer) error {
		p.Set(1, "[2/19] RUN apt-get update", 2.0/19)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"✔ 素材を集める", "紋章を鋳造する [2/19] RUN apt-get update", "▰", "100%"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が出ていない", want)
		}
	}
}

func TestSpinWithProgressOnNarrow(t *testing.T) {
	u, buf := newTest(Mono, 60, 24)
	st := []Stage{{Label: "素材を集める"}, {Label: "紋章を鋳造する"}}
	p := &Progress{}
	p.Set(1, "[3/19] COPY", 0.1)
	if err := u.RitualWithProgress("紋章の鋳造", "", st, p, func(io.Writer) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "✔ 紋章を鋳造する  [3/19] COPY\n") {
		t.Errorf("狭い端末では、回っている段階と添え書きを 1 行で出すこと: %q", buf.String())
	}
}

func TestRealms(t *testing.T) {
	u, buf := newTest(Mono, 120, 40)
	u.Realms([]Realm{
		{Name: "sigil-a-1", State: "running", Status: "Up 2 hours", Project: "/src/a"},
		{Name: "sigil-bb-2", State: "exited", Status: "Exited (0) 3 days ago", Project: "/src/b\x1b[31m"},
	})
	out := buf.String()
	for _, want := range []string{
		"  ● sigil-a-1   展開中  Up 2 hours             /src/a\n",
		"  ○ sigil-bb-2  停止中  Exited (0) 3 days ago  /src/b[31m\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない: %q", want, out)
		}
	}
	u2, buf2 := newTest(Mono, 120, 40)
	u2.Realms(nil)
	if !strings.Contains(buf2.String(), "展開中の領域はありません") {
		t.Errorf("空のとき = %q", buf2.String())
	}
	u3, buf3 := newTest(Plain, 120, 40)
	u3.Realms([]Realm{{Name: "x"}})
	if buf3.Len() != 0 {
		t.Errorf("Plain で書いている")
	}
}
