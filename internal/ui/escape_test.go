package ui

import "testing"

func TestEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\x1b[2Jb", "a^[[2Jb"},
		{"\x7f", "^?"},
		{"\u0085", "<U+0085>"},
		{"x\u202ey", "x<U+202E>y"},
		{"a\u200bb", "a<U+200B>b"},
		{"日本語 🎉\tz\n", "日本語 🎉\tz\n"},
	}
	for _, c := range cases {
		if got := Escape(c.in); got != c.want {
			t.Errorf("Escape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEscapeLine(t *testing.T) {
	if got, want := EscapeLine("a\nb\tc"), "a^Jb^Ic"; got != want {
		t.Errorf("EscapeLine = %q, want %q", got, want)
	}
}
