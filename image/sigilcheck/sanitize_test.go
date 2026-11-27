package main

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	tests := []struct{ in, want string }{
		{"plain テキスト", "plain テキスト"},
		{"a\x1b[31mb", "a[31mb"},
		{"osc\x1b]52;c;ZXZpbA==\x07end", "osc]52;c;ZXZpbA==end"},
		{"c1\u009b31m", "c131m"},
		{"del\x7f", "del"},
		{"tab\tnl\ncr\r", "tabnlcr"},
		{"bidi‮evil⁦x⁩‏", "bidievilx"},
		{"bad\xffutf8", "bad�utf8"},
	}
	for _, tt := range tests {
		if got := clean(tt.in, 0); got != tt.want {
			t.Errorf("clean(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCleanLimit(t *testing.T) {
	got := clean(strings.Repeat("あ", 300), 10)
	if got != strings.Repeat("あ", 10)+"…" {
		t.Errorf("長さの上限が効かない: %q", got)
	}
	if got := clean("short", 10); got != "short" {
		t.Errorf("短いものが変わった: %q", got)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("a b\nc\n"); got != "a b" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("\r\nx"); got != "" {
		t.Errorf("firstLine = %q", got)
	}
}
