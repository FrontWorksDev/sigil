package main

import (
	"errors"
	"io/fs"
	"strings"
	"syscall"
	"testing"
)

func TestCheckAllGood(t *testing.T) {
	f := newFake()
	r := check(f.sys())
	assertLines(t, lines(r), []string{
		"■ 禁忌の扉",
		"✓ SSH 鍵 (~/.ssh) は見えない",
		"✓ AWS 設定 (~/.aws) は見えない",
		"✓ 1Password CLI 設定 は見えない",
		"✓ ホストの .gitconfig は見えない",
		"✓ ホストの .npmrc は見えない",
		"✓ Docker の認証情報 は見えない",
		"✓ Docker ソケット は見えない",
		"✓ SSH エージェントは転送されていない",
		"✓ ssh コマンドは入っていない",
		"■ 結界",
		"✓ .git は読み取り専用",
		"■ 権能の剥奪",
		"✓ 一般ユーザー (dev) で動いている",
		"✓ Linux capabilities はすべて外れている",
		"✓ no-new-privileges が有効",
		"■ 持ち込むもの",
		"  （なし）",
	})
	if _, fail, warns := r.counts(); fail != 0 || warns != 0 {
		t.Errorf("fail=%d warns=%d, want 0", fail, warns)
	}
	if len(f.rateCalls) != 0 {
		t.Errorf("トークンがないのに GitHub に問い合わせた")
	}
}

func TestSecretsVisible(t *testing.T) {
	f := newFake()
	for _, p := range []string{"/home/dev/.ssh", "/home/dev/.aws", "/home/dev/.config/op", "/home/dev/.gitconfig", "/home/dev/.npmrc", "/home/dev/.docker", "/var/run/docker.sock"} {
		f.lstat[p] = 0
	}
	f.env["SSH_AUTH_SOCK"] = "/tmp/agent"
	f.cmds["ssh"] = "/usr/bin/ssh"
	assertLines(t, sectionLines(check(f.sys()), "禁忌の扉"), []string{
		"✗ SSH 鍵 (~/.ssh) が見えている (/home/dev/.ssh)",
		"✗ AWS 設定 (~/.aws) が見えている (/home/dev/.aws)",
		"✗ 1Password CLI 設定 が見えている (/home/dev/.config/op)",
		"✗ ホストの .gitconfig が見えている (/home/dev/.gitconfig)",
		"✗ ホストの .npmrc が見えている (/home/dev/.npmrc)",
		"✗ Docker の認証情報 が見えている (/home/dev/.docker)",
		"✗ Docker ソケット が見えている (/var/run/docker.sock)",
		"✗ SSH エージェントが転送されている",
		"✗ ssh コマンドが入っている",
	})
}

func TestSecretsFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fake)
		want  string
	}{
		{"リンク先のないシンボリックリンクも見えている", func(f *fake) { f.lstat["/home/dev/.ssh"] = fs.ModeSymlink },
			"✗ SSH 鍵 (~/.ssh) が見えている (/home/dev/.ssh)"},
		{"権限のエラーは確かめられない", func(f *fake) {
			f.lstatErr["/home/dev/.ssh"] = &fs.PathError{Op: "lstat", Path: "/home/dev/.ssh", Err: syscall.EACCES}
		}, "✗ SSH 鍵 (~/.ssh) を確かめられない (/home/dev/.ssh)"},
		{"途中がファイルなら無い", func(f *fake) {
			f.lstatErr["/home/dev/.ssh"] = &fs.PathError{Op: "lstat", Path: "/home/dev/.ssh", Err: syscall.ENOTDIR}
		}, "✓ SSH 鍵 (~/.ssh) は見えない"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			got := sectionLines(check(f.sys()), "禁忌の扉")
			if got[0] != tt.want {
				t.Errorf("got %q, want %q", got[0], tt.want)
			}
		})
	}
}

func TestBarrier(t *testing.T) {
	erofs := func(p string) error { return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS} }
	tests := []struct {
		name  string
		setup func(f *fake)
		want  []string
	}{
		{"読み取り専用 (EROFS)", func(f *fake) {}, []string{"✓ .git は読み取り専用"}},
		{"読み取り専用 (EACCES)", func(f *fake) {
			f.writeErr["/work/app/.git"] = &fs.PathError{Op: "open", Err: syscall.EACCES}
		}, []string{"✓ .git は読み取り専用"}},
		{"読み取り専用 (EPERM)", func(f *fake) {
			f.writeErr["/work/app/.git"] = &fs.PathError{Op: "open", Err: syscall.EPERM}
		}, []string{"✓ .git は読み取り専用"}},
		{"書き込める", func(f *fake) { f.writeErr["/work/app/.git"] = nil },
			[]string{"✗ .git に書き込める（hooks を仕込まれる恐れ）"}},
		{"ほかのエラーは確かめられない", func(f *fake) {
			f.writeErr["/work/app/.git"] = &fs.PathError{Op: "open", Err: syscall.ENOSPC}
		}, []string{"✗ .git に書き込めるか確かめられない"}},
		{"既にある（EEXIST）も読み取り専用とはみなさない", func(f *fake) {
			f.writeErr["/work/app/.git"] = &fs.PathError{Op: "open", Err: syscall.EEXIST}
		}, []string{"✗ .git に書き込めるか確かめられない"}},
		{".git がない", func(f *fake) { delete(f.lstat, "/work/app/.git") },
			[]string{"✗ .git がない（ホスト側で git init / clone してから sigil を起動してください）"}},
		{".git がファイル（worktree）で読み取り専用", func(f *fake) { f.lstat["/work/app/.git"] = 0 },
			[]string{"✓ .git（worktree / submodule のファイル）は読み取り専用"}},
		{".git がファイルで書き込める", func(f *fake) {
			f.lstat["/work/app/.git"] = 0
			f.writeErr["/work/app/.git"] = nil
		}, []string{"✗ .git に書き込める（hooks を仕込まれる恐れ）"}},
		{".git がファイルで確かめられない", func(f *fake) {
			f.lstat["/work/app/.git"] = 0
			f.writeErr["/work/app/.git"] = &fs.PathError{Op: "open", Err: syscall.ENXIO}
		}, []string{"✗ .git に書き込めるか確かめられない"}},
		{".git がシンボリックリンク", func(f *fake) { f.lstat["/work/app/.git"] = fs.ModeSymlink },
			[]string{"✗ .git がフォルダでもファイルでもない（点検できない）"}},
		{".git が FIFO", func(f *fake) { f.lstat["/work/app/.git"] = fs.ModeNamedPipe },
			[]string{"✗ .git がフォルダでもファイルでもない（点検できない）"}},
		{".git を調べられない", func(f *fake) {
			f.lstatErr["/work/app/.git"] = &fs.PathError{Op: "lstat", Err: syscall.EACCES}
		}, []string{"✗ .git を確かめられない"}},
		{"SIGIL_PROJECT_DIR がなければ /workspace", func(f *fake) {
			delete(f.env, "SIGIL_PROJECT_DIR")
			f.lstat["/workspace/.git"] = fs.ModeDir
			f.writeErr["/workspace/.git"] = nil
		}, []string{"✗ .git に書き込める（hooks を仕込まれる恐れ）"}},
		{"SIGIL_PROJECT_DIR が空なら /workspace", func(f *fake) {
			f.env["SIGIL_PROJECT_DIR"] = ""
			f.lstat["/workspace/.git"] = fs.ModeDir
		}, []string{"✓ .git は読み取り専用"}},
		{"Neovim 設定は読み取り専用", func(f *fake) {
			f.statDirs["/home/dev/.config/nvim"] = true
			f.writeErr["/home/dev/.config/nvim"] = erofs("x")
		}, []string{"✓ .git は読み取り専用", "✓ Neovim 設定は読み取り専用"}},
		{"Neovim 設定に書き込める", func(f *fake) {
			f.statDirs["/home/dev/.config/nvim"] = true
			f.writeErr["/home/dev/.config/nvim"] = nil
		}, []string{"✓ .git は読み取り専用", "✗ Neovim 設定に書き込める"}},
		{"Neovim 設定を確かめられない", func(f *fake) {
			f.statDirs["/home/dev/.config/nvim"] = true
			f.writeErr["/home/dev/.config/nvim"] = errors.New("x")
		}, []string{"✓ .git は読み取り専用", "✗ Neovim 設定に書き込めるか確かめられない"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			assertLines(t, sectionLines(check(f.sys()), "結界"), tt.want)
		})
	}
}

func TestPrivileges(t *testing.T) {
	good := []string{"✓ Linux capabilities はすべて外れている", "✓ no-new-privileges が有効"}
	tests := []struct {
		name   string
		status *string
		setup  func(f *fake)
		want   []string
	}{
		{"root", nil, func(f *fake) { f.uid, f.uname = 0, "root" }, append([]string{"✗ root で動いている"}, good...)},
		{"capabilities が残っている", ptr("CapEff:\t00000000a80425fb\nNoNewPrivs:\t1\n"), nil, []string{
			"✓ 一般ユーザー (dev) で動いている",
			"✗ capabilities が残っている (00000000a80425fb)",
			"✓ no-new-privileges が有効",
		}},
		{"0 は桁数に関係なく 0", ptr("CapEff:\t0\nNoNewPrivs:\t1\n"), nil, append([]string{"✓ 一般ユーザー (dev) で動いている"}, good...)},
		{"16 進数でなければ確かめられない", ptr("CapEff:\tzz\nNoNewPrivs:\t1\n"), nil, []string{
			"✓ 一般ユーザー (dev) で動いている",
			"✗ capabilities を確かめられない",
			"✓ no-new-privileges が有効",
		}},
		{"no-new-privileges が無効", ptr("CapEff:\t0000000000000000\nNoNewPrivs:\t0\n"), nil, []string{
			"✓ 一般ユーザー (dev) で動いている",
			"✓ Linux capabilities はすべて外れている",
			"✗ no-new-privileges が無効",
		}},
		{"/proc が読めない", nil, func(f *fake) { delete(f.contents, "/proc/self/status") }, []string{
			"✓ 一般ユーザー (dev) で動いている",
			"✗ capabilities を確かめられない",
			"✗ no-new-privileges を確かめられない",
		}},
		{"よく似た名前の行に惑わされない", ptr("XCapEff:\t0\nCapEffX:\t0\nNoNewPrivsX:\t1\n"), nil, []string{
			"✓ 一般ユーザー (dev) で動いている",
			"✗ capabilities を確かめられない",
			"✗ no-new-privileges を確かめられない",
		}},
		{"ユーザー名の制御文字は消す", nil, func(f *fake) { f.uname = "de\x1b[2Jv" }, append([]string{"✓ 一般ユーザー (de[2Jv) で動いている"}, good...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			if tt.status != nil {
				f.contents["/proc/self/status"] = *tt.status
			}
			if tt.setup != nil {
				tt.setup(f)
			}
			assertLines(t, sectionLines(check(f.sys()), "権能の剥奪"), tt.want)
		})
	}
}

func ptr(s string) *string { return &s }

func TestEnvNames(t *testing.T) {
	f := newFake()
	for _, k := range []string{"LANG", "TERM", "HTTPS_PROXY", "no_proxy", "BASH_ENV", "PHP_VERSION", "NO_COLOR", "GIT_CONFIG_KEY_0"} {
		f.env[k] = "x"
	}
	f.env["ZZZ_SECRET"] = "s3cr3t"
	f.env["API_KEY"] = "multi\nline=value"
	f.env["GIT_CONFIG_KEY_1"] = "x"
	got := sectionLines(check(f.sys()), "持ち込むもの")
	assertLines(t, got, []string{"  API_KEY", "  GIT_CONFIG_KEY_1", "  ZZZ_SECRET"})
	for _, l := range got {
		if strings.Contains(l, "s3cr3t") || strings.Contains(l, "line") {
			t.Errorf("値が表示されている: %q", l)
		}
	}
}

func TestEnvAllowedPatterns(t *testing.T) {
	for _, n := range []string{"BASH_FUNC_x%%", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "_"} {
		if !envAllowed(n) {
			t.Errorf("%s は除外されるはず", n)
		}
	}
	for _, n := range []string{"GH_TOKEN", "XPATH", "PATHX", "PROXY", "proxy", "BASH", "SIGILX"} {
		if envAllowed(n) {
			t.Errorf("%s は表示されるはず", n)
		}
	}
}

func TestTools(t *testing.T) {
	f := newFake()
	f.cmds = map[string]string{
		"go":     "/usr/local/go/bin/go",
		"claude": "/home/dev/.local/bin/claude",
		"cargo":  "/home/dev/.cargo/bin/cargo",                // 共有ボリューム
		"gh":     "/home/dev/.local/bin/gh",                   // リンク先が共有ボリューム
		"bat":    "/usr/local/bin/bat",                        // リンク先もイメージの中
		"rg":     "relative/rg",                               // 相対パス
		"node":   "/home/dev/.local/bin/../../go/bin/node",    // .. で外に出る
		"fd":     "/home/dev/.npm-global/bin/fd",              // イメージの場所
		"delta":  "/usr/local/bin/../../../home/dev/go/delta", // .. で外に出る
	}
	f.links["/home/dev/.local/bin/gh"] = "/home/dev/go/bin/gh"
	f.links["/home/dev/.local/bin/claude"] = "/home/dev/.local/share/claude/versions/2.1.0"
	f.links["/usr/local/bin/bat"] = "/usr/bin/batcat"
	got := sectionLines(check(f.sys()), "ツール")
	if len(got) != len(toolNames) {
		t.Fatalf("ツールの行の数 = %d, want %d", len(got), len(toolNames))
	}
	for _, want := range []string{
		"T go go 1.0 false",
		"T claude claude 1.0 false",
		"T bat bat 1.0 false",
		"T fd fd 1.0 false",
		"T cargo /home/dev/.cargo/bin/cargo（イメージの外なので実行しない） true",
		"T gh /home/dev/.local/bin/gh（イメージの外なので実行しない） true",
		"T rg relative/rg（イメージの外なので実行しない） true",
		"T node /home/dev/.local/bin/../../go/bin/node（イメージの外なので実行しない） true",
		"T delta /usr/local/bin/../../../home/dev/go/delta（イメージの外なので実行しない） true",
		"T nvim 見つからない true",
	} {
		if !hasLine(got, want) {
			t.Errorf("%q がない:\n%s", want, joinLines(got))
		}
	}
	// 確かめた解決後のパスで実行する
	if !hasLine(f.versions, "/usr/bin/batcat") || hasLine(f.versions, "/usr/local/bin/bat") {
		t.Errorf("bat を解決後のパスで実行していない: %v", f.versions)
	}
	if !hasLine(f.versions, "/home/dev/.local/share/claude/versions/2.1.0") {
		t.Errorf("claude を解決後のパスで実行していない: %v", f.versions)
	}
	for _, p := range f.versions {
		if strings.Contains(p, "/go/") && p != "/usr/local/go/bin/go" || strings.Contains(p, ".cargo") || strings.Contains(p, "relative") {
			t.Errorf("イメージの外のコマンドを実行した: %s", p)
		}
	}
	for _, want := range []string{"python3", "uv", "gh", "wakatime-cli", "lua5.1", "luarocks", "lazygit", "bat", "delta"} {
		found := false
		for _, n := range toolNames {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("ツールの一覧に %s がない（設計書 11 章）", want)
		}
	}
}

func TestToolVersionSanitized(t *testing.T) {
	f := newFake()
	f.cmds = map[string]string{"go": "/usr/local/go/bin/go"}
	s := f.sys()
	s.version = func(string, string) string { return "go\x1b]52;c;ZXZpbA==\x07 1.27‮" }
	for _, l := range sectionLines(check(s), "ツール") {
		if strings.ContainsAny(l, "\x1b\x07‮") {
			t.Errorf("制御文字が残っている: %q", l)
		}
	}
}

func TestStatusField(t *testing.T) {
	st := "Name:\tx\nCapEff:\t0000000000000000\nNoNewPrivs:\t1\n"
	if v, ok := statusField(st, "CapEff"); !ok || v != "0000000000000000" {
		t.Errorf("CapEff = %q, %v", v, ok)
	}
	if v, ok := statusField("NoNewPrivs:1\n", "NoNewPrivs"); !ok || v != "1" {
		t.Errorf("空白なしの NoNewPrivs:1 を読めない: %q", v)
	}
	if _, ok := statusField("CapEffX:\t0\n", "CapEff"); ok {
		t.Error("よく似た名前の行を読んだ")
	}
}
