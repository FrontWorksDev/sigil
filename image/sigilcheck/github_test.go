package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

func ok200(exp string) rateResp { return rateResp{status: 200, header: expires(exp)} }

func TestTokenKinds(t *testing.T) {
	tests := []struct {
		name  string
		tok   string
		want  []string
		calls int
	}{
		{"fine-grained", "github_pat_abc", []string{"✓ GH_TOKEN は fine-grained PAT", "✓ GH_TOKEN の期限まであと 34 日（2027-01-01 UTC）"}, 1},
		{"classic は期限も見る", "ghp_abc", []string{"✗ GH_TOKEN は classic PAT（対象のリポジトリを絞れない）。fine-grained PAT にしてください", "✓ GH_TOKEN の期限まであと 34 日（2027-01-01 UTC）"}, 1},
		{"OAuth", "gho_abc", []string{"✗ GH_TOKEN は OAuth のトークン（gh auth login などで作られ、リポジトリを絞れない）。fine-grained PAT にしてください"}, 0},
		{"App ユーザー", "ghu_abc", []string{"! GH_TOKEN は GitHub App のトークン（届く範囲はアプリの設定しだい。fine-grained PAT を勧めます）"}, 0},
		{"App サーバー", "ghs_abc", []string{"! GH_TOKEN は GitHub App のトークン（届く範囲はアプリの設定しだい。fine-grained PAT を勧めます）"}, 0},
		{"不明", "abc", []string{"! GH_TOKEN のトークンの種類が分からない（fine-grained PAT を勧めます）"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			f.env["GH_TOKEN"] = tt.tok
			f.rate[tt.tok] = ok200("2027-01-01 00:00:00 UTC")
			assertLines(t, sectionLines(check(f.sys()), "外界の鍵"), tt.want)
			if len(f.rateCalls) != tt.calls {
				t.Errorf("問い合わせ = %d 回, want %d", len(f.rateCalls), tt.calls)
			}
		})
	}
}

func TestEnterpriseTokenOnlyKind(t *testing.T) {
	f := newFake()
	f.env["GH_ENTERPRISE_TOKEN"] = "github_pat_e"
	f.env["GITHUB_ENTERPRISE_TOKEN"] = "ghp_e"
	assertLines(t, sectionLines(check(f.sys()), "外界の鍵"), []string{
		"✓ GH_ENTERPRISE_TOKEN は fine-grained PAT",
		"✗ GITHUB_ENTERPRISE_TOKEN は classic PAT（対象のリポジトリを絞れない）。fine-grained PAT にしてください",
	})
	if len(f.rateCalls) != 0 {
		t.Error("GHES 向けのトークンで github.com に問い合わせた")
	}
}

func TestGitHubTokenSameAsGHToken(t *testing.T) {
	f := newFake()
	f.env["GH_TOKEN"] = "github_pat_same"
	f.env["GITHUB_TOKEN"] = "github_pat_same"
	f.rate["github_pat_same"] = ok200("2027-01-01 00:00:00 UTC")
	assertLines(t, sectionLines(check(f.sys()), "外界の鍵"), []string{
		"✓ GH_TOKEN は fine-grained PAT",
		"✓ GH_TOKEN の期限まであと 34 日（2027-01-01 UTC）",
		"~ GITHUB_TOKEN は GH_TOKEN と同じ値",
	})
	if len(f.rateCalls) != 1 {
		t.Errorf("問い合わせ = %d 回, want 1", len(f.rateCalls))
	}
}

func TestGitHubTokenDifferent(t *testing.T) {
	f := newFake()
	f.env["GH_TOKEN"] = "github_pat_a"
	f.env["GITHUB_TOKEN"] = "github_pat_b"
	f.rate["github_pat_a"] = ok200("2027-01-01 00:00:00 UTC")
	f.rate["github_pat_b"] = rateResp{status: 401}
	assertLines(t, sectionLines(check(f.sys()), "外界の鍵"), []string{
		"✓ GH_TOKEN は fine-grained PAT",
		"✓ GH_TOKEN の期限まであと 34 日（2027-01-01 UTC）",
		"✓ GITHUB_TOKEN は fine-grained PAT",
		"✗ GITHUB_TOKEN は GitHub に拒否された（無効か、期限が切れている）",
	})
	if strings.Join(f.rateCalls, ",") != "github_pat_a,github_pat_b" {
		t.Errorf("問い合わせたトークン = %v", f.rateCalls)
	}
}

func TestNoGhNeeded(t *testing.T) {
	// gh がなくても、期限を確かめる（net/http で直接問い合わせる）
	f := newFake()
	f.env["GH_TOKEN"] = "github_pat_x"
	f.rate["github_pat_x"] = ok200("2027-01-01 00:00:00 UTC")
	got := sectionLines(check(f.sys()), "外界の鍵")
	if !hasLine(got, "✓ GH_TOKEN の期限まであと 34 日（2027-01-01 UTC）") {
		t.Errorf("期限を確かめていない:\n%s", joinLines(got))
	}
}

func TestHostsYml(t *testing.T) {
	const p = "/home/dev/.config/gh/hosts.yml"
	tests := []struct {
		name  string
		setup func(f *fake)
		want  []string
	}{
		{"ログインあり", func(f *fake) {
			f.lstat[p] = 0
			f.contents[p] = "github.com:\n    oauth_token: gho_x\n"
		}, []string{"✗ gh auth login のトークンが ~/.config/gh/hosts.yml にある。gh auth logout してください"}},
		{"ログインなし", func(f *fake) {
			f.lstat[p] = 0
			f.contents[p] = "github.com:\n    git_protocol: https\n"
		}, []string{"✓ gh auth login のトークンはない"}},
		{"読めない", func(f *fake) {
			f.lstat[p] = 0
			f.readErr[p] = &fs.PathError{Op: "open", Err: syscall.EACCES}
		}, []string{"✗ ~/.config/gh/hosts.yml を確かめられない"}},
		{"調べられない", func(f *fake) {
			f.lstatErr[p] = &fs.PathError{Op: "lstat", Err: syscall.EACCES}
		}, []string{"✗ ~/.config/gh/hosts.yml を確かめられない"}},
		{"GH_CONFIG_DIR", func(f *fake) {
			f.env["GH_CONFIG_DIR"] = "/home/dev/ghc"
			f.lstat["/home/dev/ghc/hosts.yml"] = 0
			f.contents["/home/dev/ghc/hosts.yml"] = "oauth_token: gho_y\n"
		}, []string{"✗ gh auth login のトークンが /home/dev/ghc/hosts.yml にある。gh auth logout してください"}},
		{"GH_CONFIG_DIR が既定と同じなら 1 回", func(f *fake) {
			f.env["GH_CONFIG_DIR"] = "/home/dev/.config/gh"
			f.lstat[p] = 0
			f.contents[p] = ""
		}, []string{"✓ gh auth login のトークンはない"}},
		{"GH_CONFIG_DIR が書き方の違う既定の場所でも 1 回", func(f *fake) {
			f.env["GH_CONFIG_DIR"] = "/home/dev//.config/gh/."
			f.lstat[p] = 0
			f.contents[p] = ""
		}, []string{"✓ gh auth login のトークンはない"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			got := sectionLines(check(f.sys()), "外界の鍵")
			assertLines(t, got, tt.want)
			if strings.Contains(joinLines(got), "gho_") {
				t.Error("hosts.yml のトークンが表示された")
			}
		})
	}
}

func TestExpiry(t *testing.T) {
	tests := []struct {
		name string
		resp rateResp
		want string
	}{
		{"8〜90 日", ok200("2027-01-01 00:00:00 UTC"), "✓ T の期限まであと 34 日（2027-01-01 UTC）"},
		{"時差を数字で書く形", ok200("2027-01-01 09:00:00 +0900"), "✓ T の期限まであと 34 日（2027-01-01 UTC）"},
		{"ちょうど 8 日", ok200("2026-12-05 12:00:00 UTC"), "✓ T の期限まであと 8 日（2026-12-05 UTC）"},
		{"7 日以内", ok200("2026-12-05 11:59:59 UTC"), "! T の期限まであと 7 日（2026-12-05 UTC）。そろそろ作り直してください"},
		{"1 日未満", ok200("2026-11-27 13:00:00 UTC"), "! T の期限まであと 0 日（2026-11-27 UTC）。そろそろ作り直してください"},
		{"ちょうど 90 日", ok200("2027-02-25 12:00:00 UTC"), "✓ T の期限まであと 90 日（2027-02-25 UTC）"},
		{"90 日を超える", ok200("2027-02-26 12:00:00 UTC"), "! T の期限まであと 91 日（2027-02-26 UTC）。30〜90 日にすると、漏れたときの影響を抑えられます"},
		{"切れている", ok200("2026-11-27 12:00:00 UTC"), "✗ T の期限が切れている（2026-11-27 UTC）"},
		{"期限がない", rateResp{status: 200, header: http.Header{}}, "! T に期限がない。30〜90 日の期限を付けて作り直してください"},
		{"読めない", ok200("someday"), "! T の期限を読み取れない"},
		{"知らない時間帯の略号は読まない", ok200("2027-01-01 00:00:00 JST"), "! T の期限を読み取れない"},
		{"401", rateResp{status: 401}, "✗ T は GitHub に拒否された（無効か、期限が切れている）"},
		{"問い合わせられない", rateResp{err: errors.New("dial tcp: secret-in-error")}, "! GitHub に問い合わせられないので、T の期限を確かめられない"},
		{"その他の状態", rateResp{status: 503}, "! GitHub の応答が 503 なので、T の期限を確かめられない"},
		{"リダイレクト", rateResp{status: 302}, "! GitHub の応答が 302 なので、T の期限を確かめられない"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := itemLine(expiryItem("T", tt.resp.status, tt.resp.header, tt.resp.err, testNow))
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestExpiryNeverShowsRawHeader(t *testing.T) {
	// 読めなかった値も、読めた値も、そのままは表示しない（日付は作り直して出す）
	for _, v := range []string{"2027-01-01 00:00:00 UTC\x1b[31m", "evil\x1b]0;x\x07", "2027-01-01 00:00:00 UTC"} {
		it := expiryItem("T", 200, expires(v), nil, testNow)
		if strings.Contains(it.text, v) || strings.ContainsAny(it.text, "\x1b\x07") {
			t.Errorf("ヘッダーの値が表示された: %q", it.text)
		}
	}
}

func TestParseExpiry(t *testing.T) {
	want := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, v := range []string{"2027-01-01 00:00:00 UTC", " 2027-01-01 00:00:00 UTC ", "2027-01-01 00:00:00 GMT", "2027-01-01 09:00:00 +0900", "2026-12-31 15:00:00 -0900"} {
		got, ok := parseExpiry(v)
		if !ok || !got.Equal(want) {
			t.Errorf("parseExpiry(%q) = %v, %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "2027-01-01", "2027-01-01 00:00:00 PST", "2027-01-01T00:00:00Z", "2027-01-01 00:00:00 MST"} {
		if _, ok := parseExpiry(v); ok {
			t.Errorf("parseExpiry(%q) が成功した", v)
		}
	}
}

func TestTokenNeverDisplayed(t *testing.T) {
	f := newFake()
	const tok = "github_pat_SECRETVALUE"
	f.env["GH_TOKEN"] = tok
	f.rate[tok] = rateResp{err: fmt.Errorf("Get https://api.github.com: Authorization: Bearer %s", tok)}
	var b strings.Builder
	render(&b, check(f.sys()), false)
	if strings.Contains(b.String(), "SECRETVALUE") {
		t.Errorf("トークンが表示された:\n%s", b.String())
	}
}
