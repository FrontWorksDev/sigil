package main

import (
	"strings"
	"testing"
)

func sampleReport() report {
	return report{
		{title: "禁忌の扉", sub: "ホストの秘密情報が見えないこと", items: []item{
			{kind: kindOK, text: "SSH 鍵 (~/.ssh) は見えない"},
			{kind: kindNG, text: "Docker ソケット が見えている (/var/run/docker.sock)"},
		}},
		{title: "持ち込むもの", sub: "渡されている環境変数（名前のみ）", items: []item{
			{kind: kindNote, text: "API_KEY"},
		}},
		{title: "外界の鍵", sub: "GitHub のトークン（値は表示しない）", items: []item{
			{kind: kindWarn, text: "GH_TOKEN に期限がない"},
			{kind: kindDimNote, text: "GITHUB_TOKEN は GH_TOKEN と同じ値"},
		}},
		{title: "ツール", sub: "コンテナ内のコマンドと版", items: []item{
			{kind: kindTool, tool: "go", text: "go version go1.27.4 linux/arm64"},
			{kind: kindTool, tool: "nvim", text: "見つからない", warn: true},
		}},
	}
}

func TestRenderPlain(t *testing.T) {
	var b strings.Builder
	failed := render(&b, sampleReport(), false)
	want := "\n" +
		"  ⟡ 結界の点検 （コンテナの隔離が効いているかの確認）\n" +
		"\n■ 禁忌の扉 ── ホストの秘密情報が見えないこと\n" +
		"  ✓ SSH 鍵 (~/.ssh) は見えない\n" +
		"  ✗ Docker ソケット が見えている (/var/run/docker.sock)\n" +
		"\n■ 持ち込むもの ── 渡されている環境変数（名前のみ）\n" +
		"    API_KEY\n" +
		"\n■ 外界の鍵 ── GitHub のトークン（値は表示しない）\n" +
		"  ! GH_TOKEN に期限がない\n" +
		"    GITHUB_TOKEN は GH_TOKEN と同じ値\n" +
		"\n■ ツール ── コンテナ内のコマンドと版\n" +
		"    go           go version go1.27.4 linux/arm64\n" +
		"    nvim         見つからない\n" +
		"\n" +
		"✗ 結界に綻びがあります ── 1 項目に問題あり（OK: 1、注意: 1）\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
	if !failed {
		t.Error("失敗があるのに failed = false")
	}
	if strings.Contains(b.String(), "\x1b") {
		t.Error("色なしなのにエスケープがある")
	}
}

func TestRenderColor(t *testing.T) {
	var b strings.Builder
	render(&b, sampleReport(), true)
	want := "\n" +
		"  \x1b[1m⟡ 結界の点検\x1b[0m \x1b[2m（コンテナの隔離が効いているかの確認）\x1b[0m\n" +
		"\n\x1b[38;5;141m■ 禁忌の扉\x1b[0m \x1b[2m── ホストの秘密情報が見えないこと\x1b[0m\n" +
		"  \x1b[38;5;114m✓\x1b[0m SSH 鍵 (~/.ssh) は見えない\n" +
		"  \x1b[38;5;203m✗\x1b[0m Docker ソケット が見えている (/var/run/docker.sock)\n" +
		"\n\x1b[38;5;141m■ 持ち込むもの\x1b[0m \x1b[2m── 渡されている環境変数（名前のみ）\x1b[0m\n" +
		"    API_KEY\n" +
		"\n\x1b[38;5;141m■ 外界の鍵\x1b[0m \x1b[2m── GitHub のトークン（値は表示しない）\x1b[0m\n" +
		"  \x1b[38;5;221m!\x1b[0m GH_TOKEN に期限がない\n" +
		"    \x1b[2mGITHUB_TOKEN は GH_TOKEN と同じ値\x1b[0m\n" +
		"\n\x1b[38;5;141m■ ツール\x1b[0m \x1b[2m── コンテナ内のコマンドと版\x1b[0m\n" +
		"    go           go version go1.27.4 linux/arm64\n" +
		"    nvim         \x1b[38;5;221m見つからない\x1b[0m\n" +
		"\n" +
		"\x1b[38;5;203m✗ 結界に綻びがあります\x1b[0m ── 1 項目に問題あり（OK: 1、注意: 1）\n"
	if b.String() != want {
		t.Errorf("got:\n%q\nwant:\n%q", b.String(), want)
	}
}

func TestRenderAllOK(t *testing.T) {
	r := report{{title: "a", sub: "b", items: []item{{kind: kindOK, text: "x"}, {kind: kindOK, text: "y"}}}}
	var b strings.Builder
	if render(&b, r, false) {
		t.Error("failed = true")
	}
	if !strings.HasSuffix(b.String(), "\n✔ 結界は完全です ── すべて OK (2 項目)\n") {
		t.Errorf("まとめの行が違う: %q", b.String())
	}
	r[0].items = append(r[0].items, item{kind: kindWarn, text: "z"})
	b.Reset()
	render(&b, r, true)
	if !strings.HasSuffix(b.String(), "\x1b[38;5;114m✔ 結界は完全です\x1b[0m \x1b[2m── すべて OK (2 項目、注意: 1)\x1b[0m\n") {
		t.Errorf("まとめの行が違う: %q", b.String())
	}
}

func TestRenderSanitizesItems(t *testing.T) {
	r := report{{title: "a", sub: "b", items: []item{
		{kind: kindNG, text: "x\x1b[2J\ny"},
		{kind: kindTool, tool: "t\x1b", text: "v\u009b1m"},
	}}}
	var b strings.Builder
	render(&b, r, false)
	if strings.ContainsAny(b.String(), "\x1b\u009b") || strings.Contains(b.String(), "x\ny") {
		t.Errorf("制御文字が残っている: %q", b.String())
	}
}

func TestRun(t *testing.T) {
	f := newFake()
	var b strings.Builder
	if code := run(&b, f.sys(), false); code != 0 {
		t.Errorf("隔離が効いているのに終了コード %d", code)
	}
	f.lstat["/home/dev/.ssh"] = 0
	b.Reset()
	if code := run(&b, f.sys(), false); code != 1 {
		t.Errorf("失敗があるのに終了コード %d", code)
	}
}
