package ui

import (
	"bufio"
	"strings"
	"testing"
)

func sampleView() CommitView {
	return CommitView{
		Branch:      "main",
		HeadShort:   "abc1234",
		HeadSubject: "前のコミット",
		Pathspec:    []string{"src", "README.md"},
		Files: []FileRow{
			{Status: 'A', Path: "new.txt", Added: 12},
			{Status: 'M', Path: "a/b.go", Added: 3, Deleted: 1},
			{Status: 'D', Path: "old.txt", Deleted: 5},
			{Status: 'R', Path: "to.txt", OldPath: "from.txt"},
			{Status: 'M', Path: "logo.png", Binary: true, Size: 2048},
		},
		Warnings: []WarnRow{{Path: ".env", Reason: "秘密らしいファイルです"}},
		Message:  "件名です\n\n本文の 1 行目\n\t字下げ",
		Hooks:    []HookRow{{Name: "pre-commit", OK: true}, {Name: "commit-msg", OK: false}},
	}
}

// ownSGR は、sigil が付けた SGR だけを取り除く。
func ownSGR(s string) string { return stripSGR(s) }

func TestCommitScreenEscapes(t *testing.T) {
	u, buf := newTest(Color, 100, 40)
	v := sampleView()
	v.Files = []FileRow{{Status: 'A', Path: "a\x1b[2J.txt", OldPath: "o\x1b[2J", Added: 1}}
	v.Message = "x"
	v.Warnings = []WarnRow{{Path: "l\x1b[2J", Reason: "リンク先 ‮"}}
	v.Branch = "b\x1b[2J"
	v.HeadSubject = "s\x1b[2J"
	u.CommitScreen(v)
	out := ownSGR(buf.String())
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '‮') {
		t.Errorf("制御文字が残っている: %q", out)
	}
	if !strings.Contains(out, "^[[2J") || !strings.Contains(out, "<U+202E>") {
		t.Errorf("見える形になっていない: %q", out)
	}
}

func TestCommitScreenPlain(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	u.CommitScreen(sampleView())
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("Plain にエスケープがある: %q", out)
	}
	for _, want := range []string{"+ 追加", "~ 変更", "- 削除", "→ 名前の変更", "from.txt", "⚠", "ホストでは hooks を実行していません",
		"+12", "-1", "2.0KiB", "刻印の儀", "main", "abc1234", "pre-commit", "commit-msg", "件名です", "    本文の 1 行目"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\t") {
		t.Errorf("タブが残っている")
	}
}

func TestCommitScreenMono(t *testing.T) {
	u, buf := newTest(Mono, 100, 40)
	u.CommitScreen(sampleView())
	if strings.Contains(buf.String(), "\x1b[3") || strings.Contains(buf.String(), "\x1b[1m") {
		t.Errorf("Mono に色がある: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "╭") {
		t.Errorf("枠がない")
	}
}

func TestCommitScreenColor(t *testing.T) {
	u, buf := newTest(Color, 100, 40)
	u.CommitScreen(sampleView())
	out := buf.String()
	for _, want := range []string{"\x1b[38;5;114m+", "\x1b[38;5;221m~", "\x1b[38;5;203m-", "\x1b[38;5;87m→"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない", want)
		}
	}
}

// squash は、SGR・枠・空白を取り除いてつなげる（折り返しで字下げや枠が入っても、内容が全部あるかを調べるため）。
func squash(out string) string {
	var b strings.Builder
	for _, r := range stripSGR(out) {
		if r == ' ' || r == '\n' || r == '│' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func checkWidth(t *testing.T, mode Mode, out string, cols int) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if Width(l) > cols {
			t.Errorf("mode %d: 幅 %d > %d: %q", mode, Width(l), cols, l)
		}
	}
}

func TestCommitScreenNarrow(t *testing.T) {
	digits := strings.Repeat("0123456789", 12)                            // 120 桁の本文
	longPath := "dir/" + strings.Repeat("abcdefghij", 10) + "/secret.env" // 100 文字超のパス
	newPath := "new/" + strings.Repeat("klmnopqrst", 6) + "/renamed-target.go"
	link := "シンボリックリンク → " + strings.Repeat("target/", 15) + "end.txt"
	url := "ssh://git@example.com:2222/" + strings.Repeat("org/", 10) + "repo-name.git"
	for _, mode := range []Mode{Plain, Mono, Color} {
		u, buf := newTest(mode, 40, 40)
		v := sampleView()
		v.Files = append(v.Files,
			FileRow{Status: 'M', Path: longPath, Added: 100, Deleted: 20},
			FileRow{Status: 'R', OldPath: "old/" + strings.Repeat("uvwxyz", 8) + ".go", Path: newPath})
		v.Warnings = []WarnRow{{Path: "l/ink", Reason: link}}
		v.Message = "とても長い件名とても長い件名とても長い件名とても長い件名\n\n" + digits + "\n" + strings.Repeat(" ", 30) + "Signed-off-by: x"
		u.CommitScreen(v)
		out := buf.String()
		checkWidth(t, mode, out, 40)
		sq := squash(out)
		for _, want := range []string{digits, longPath, newPath, link, "とても長い件名とても長い件名とても長い件名とても長い件名", "Signed-off-by:x"} {
			if !strings.Contains(sq, squash(want)) {
				t.Errorf("mode %d: %q が欠けている:\n%s", mode, want, out)
			}
		}
		if strings.Contains(out, "…") {
			t.Errorf("mode %d: 判断の画面で切り詰めている\n%s", mode, out)
		}

		u, buf = newTest(mode, 40, 40)
		u.PushPrompt(PushView{Remote: "origin", URL: url, Branch: "feature/" + digits, Ahead: 3})
		checkWidth(t, mode, strings.TrimSuffix(buf.String(), "sigil: 外界へ解き放つ（push する）なら y を入力してください [y/N] "), 40)
		if sq := squash(buf.String()); !strings.Contains(sq, squash(url)) || !strings.Contains(sq, digits) {
			t.Errorf("mode %d: push 先が欠けている:\n%s", mode, buf.String())
		}
	}
}

func TestContractScreenNarrow(t *testing.T) {
	ref := "op://" + strings.Repeat("vault", 6) + "/" + strings.Repeat("item", 6) + "/field-name"
	for _, m := range []Mode{Color, Mono} {
		u, buf := newTest(m, 60, 40)
		u.ContractScreen(ContractView{Files: []string{".sigil", ".env"}, Envs: []string{"FOO"}, Refs: []ContractRef{{Name: "API_KEY", Ref: ref, Source: "1Password"}}})
		out := buf.String()
		if !strings.Contains(squash(out), ref) {
			t.Errorf("mode %d: 参照が欠けている:\n%s", m, out)
		}
		checkWidth(t, m, strings.TrimSuffix(out, "sigil: この契約を結ぶ（この設定で承認する）なら y を入力してください [y/N] "), 60)
	}
}

func TestCommitScreenLongSubject(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	v := sampleView()
	v.Message = strings.Repeat("あ", 73)
	v.Files = nil
	u.CommitScreen(v)
	if !strings.Contains(buf.String(), "72 文字を超えて") {
		t.Errorf("注意がない:\n%s", buf.String())
	}
	u, buf = newTest(Plain, 0, 0)
	v.Message = strings.Repeat("あ", 72)
	u.CommitScreen(v)
	if strings.Contains(buf.String(), "72 文字を超えて") {
		t.Errorf("72 文字ちょうどで注意が出た")
	}
}

func TestCommitScreenNoPathspecNoWarnings(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	v := sampleView()
	v.Pathspec, v.Warnings = nil, nil
	u.CommitScreen(v)
	if strings.Contains(buf.String(), "⚠") || strings.Contains(buf.String(), "対象") {
		t.Errorf("あるときだけの項目が出ている:\n%s", buf.String())
	}
}

func TestPrompts(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	u.CommitPrompt()
	if buf.String() != "sigil: この内容で刻印する（コミットする）なら y、差分を見るなら d を入力してください [y/N/d] " {
		t.Errorf("CommitPrompt = %q", buf.String())
	}
	u, buf = newTest(Color, 100, 40)
	u.PushPrompt(PushView{Remote: "origin", URL: "git@x:a/b\x1b[2J.git", Branch: "main", Ahead: 2})
	out := ownSGR(buf.String())
	if strings.ContainsRune(out, '\x1b') || !strings.Contains(out, "^[[2J") || !strings.Contains(out, "origin") {
		t.Errorf("PushPrompt = %q", out)
	}
	if !strings.HasSuffix(out, "sigil: 外界へ解き放つ（push する）なら y を入力してください [y/N] ") {
		t.Errorf("問いの行が末尾にない: %q", out)
	}
	u, buf = newTest(Plain, 0, 0)
	u.PushPrompt(PushView{Remote: "origin", URL: "u", Branch: "main", Ahead: 2})
	if !strings.Contains(buf.String(), "2") || !strings.HasSuffix(buf.String(), "[y/N] ") {
		t.Errorf("Plain PushPrompt = %q", buf.String())
	}
}

func TestSealed(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	u.Sealed("刻印しました", "main abc1234", "件名\x1b[2J")
	if want := "sigil: ✔ 刻印しました\n"; !strings.HasPrefix(buf.String(), want) {
		t.Errorf("Plain = %q", buf.String())
	}
	if strings.ContainsRune(buf.String(), '\x1b') || !strings.Contains(buf.String(), "件名^[[2J") {
		t.Errorf("Plain = %q", buf.String())
	}
	for _, size := range [][2]int{{100, 40}, {40, 10}} {
		u, buf = newTest(Color, size[0], size[1])
		u.Sealed("刻印しました", "main abc1234", "件名\x1b[2J")
		out := ownSGR(buf.String())
		if strings.ContainsRune(out, '\x1b') && !strings.Contains(buf.String(), "\x1b[?25") {
			t.Errorf("生のエスケープ: %q", out)
		}
		if !strings.Contains(out, "刻印しました") || !strings.Contains(out, "abc1234") || !strings.Contains(out, "╭") || !strings.Contains(out, "^[[2J") {
			t.Errorf("Color %v = %q", size, out)
		}
	}
}

func TestDiffPage(t *testing.T) {
	u, _ := newTest(Color, 100, 40)
	out := u.DiffPage("diff --git a/x b/x\nindex 1..2\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n+a\n-b\n x\x1b\n")
	if !strings.Contains(out, "\x1b[38;5;114m+a") || !strings.Contains(out, "\x1b[38;5;203m-b") || !strings.Contains(out, "\x1b[38;5;87m@@ -1 +1 @@") {
		t.Errorf("色がない: %q", out)
	}
	if !strings.Contains(out, "x^[") || strings.ContainsRune(stripSGR(out), '\x1b') {
		t.Errorf("エスケープが残っている: %q", out)
	}
	if !strings.Contains(out, "─") || strings.Contains(out, "diff --git") {
		t.Errorf("境目の罫線になっていない: %q", out)
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("末尾の改行 = %q", out[len(out)-3:])
	}
	// +++ / --- は追加・削除の行ではない
	if strings.Contains(out, "\x1b[38;5;114m+++") || strings.Contains(out, "\x1b[38;5;203m---") {
		t.Errorf("ファイル名の行に色が付いた")
	}
	for _, m := range []Mode{Mono, Plain} {
		u, _ := newTest(m, 100, 40)
		if strings.ContainsRune(u.DiffPage("+a\n-b\n"), '\x1b') {
			t.Errorf("mode %d に色", m)
		}
	}
}

// Plain の承認の画面の文言。スクリプトや記憶に頼る人のため、変えるときはここも意図して変える。
const askWithRefs = "sigil: 契約の確認 ── .sigil と .env の内容が新しいか、前回の承認から変わっています。\n" +
	"     このプロジェクトの領域（コンテナ）に、次のものを渡します:\n" +
	"       持ち込むもの（ホストの環境変数）  FOO\n" +
	"       持ち込むもの（ホストの環境変数）  BAR\n" +
	"       門（ポート）                      127.0.0.1:5173:5173\n" +
	"     禁書庫から召喚するもの（.env で、1Password・キーチェーンから値を読み込む名前と参照先）:\n" +
	"       API_KEY                  1Password    op://v/i/f\n" +
	"       WAKA                     キーチェーン keychain://wakatime  （全体の設定が優先されるため使わない）\n" +
	"       PLAIN                                 （.env に直接書かれた値）\n" +
	"     このほか、全体設定（~/.config/sigil/config）の分も渡されます。\n" +
	"sigil: この契約を結ぶ（この設定で承認する）なら y を入力してください [y/N] "

const askEmpty = "sigil: 契約の確認 ── .sigil の内容が新しいか、前回の承認から変わっています。\n" +
	"     このプロジェクトの領域（コンテナ）に、次のものを渡します:\n" +
	"       （なし）\n" +
	"     このほか、全体設定（~/.config/sigil/config）の分も渡されます。\n" +
	"sigil: この契約を結ぶ（この設定で承認する）なら y を入力してください [y/N] "

func TestContractScreenPlainGolden(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	u.ContractScreen(ContractView{
		Files: []string{".sigil", ".env"},
		Envs:  []string{"FOO", "BAR"},
		Ports: []string{"127.0.0.1:5173:5173"},
		Refs: []ContractRef{{Name: "API_KEY", Ref: "op://v/i/f", Source: "1Password"},
			{Name: "WAKA", Ref: "keychain://wakatime", Source: "キーチェーン", Overridden: true}, {Name: "PLAIN"}},
	})
	if buf.String() != askWithRefs {
		t.Errorf("差がある:\n got: %q\nwant: %q", buf.String(), askWithRefs)
	}
	u, buf = newTest(Plain, 0, 0)
	u.ContractScreen(ContractView{Files: []string{".sigil"}})
	if buf.String() != askEmpty {
		t.Errorf("差がある:\n got: %q\nwant: %q", buf.String(), askEmpty)
	}
}

func TestContractScreenColor(t *testing.T) {
	for _, m := range []Mode{Color, Mono} {
		u, buf := newTest(m, 80, 40)
		u.ContractScreen(ContractView{
			Files: []string{".sigil", ".env"}, Envs: []string{"FOO"}, Ports: []string{"127.0.0.1:5173:5173"},
			Refs: []ContractRef{{Name: "API_KEY", Ref: "op://v/i/f", Source: "1Password"},
				{Name: "WAKA", Ref: "keychain://wakatime", Source: "キーチェーン", Overridden: true}, {Name: "PLAIN"}},
		})
		out := ownSGR(buf.String())
		for _, want := range []string{"⟡ 契約の確認", "持ち込むもの", "ホストの環境変数", "門", "ポート", "禁書庫", "FOO", "127.0.0.1:5173:5173",
			"op://v/i/f", "（.env に直接書かれた値）", "全体設定（~/.config/sigil/config）",
			"1Password", "キーチェーン", "keychain://wakatime", "全体の設定が優先されるため使わない",
			"1Password・キーチェーンから値を読み込む"} {
			if !strings.Contains(out, want) {
				t.Errorf("mode %d: %q がない:\n%s", m, want, out)
			}
		}
		if !strings.HasSuffix(out, "sigil: この契約を結ぶ（この設定で承認する）なら y を入力してください [y/N] ") {
			t.Errorf("問いの行が末尾にない")
		}
		for _, l := range strings.Split(out, "\n") {
			if Width(l) > 79 {
				t.Errorf("幅 %d: %q", Width(l), l)
			}
		}
	}
}

func TestIsYes(t *testing.T) {
	for s, want := range map[string]bool{"y": true, "Y": true, "yes": true, "YES": true, "ye": false, "n": false, "": false, " y": false, "Yes": false} {
		if IsYes(s) != want {
			t.Errorf("IsYes(%q) = %v", s, !want)
		}
	}
}

func TestReadAnswerSharesReader(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("d\ny\r\n"))
	if got := ReadAnswer(r); got != "d" {
		t.Errorf("1 回目 = %q", got)
	}
	if got := ReadAnswer(r); got != "y" {
		t.Errorf("2 回目 = %q", got)
	}
	if got := ReadAnswer(r); got != "" {
		t.Errorf("EOF = %q", got)
	}
	if got := ReadAnswer(strings.NewReader("yes")); got != "yes" {
		t.Errorf("改行なし = %q", got)
	}
}

const askWithMounts = "sigil: 契約の確認 ── .sigil の内容か、写本の場所が、新しいか前回の承認から変わっています。\n" +
	"     このプロジェクトの領域（コンテナ）に、次のものを渡します:\n" +
	"       持ち込むもの（ホストの環境変数）  FOO\n" +
	"       写本（読み取り専用のフォルダ）    /h/src/lib → /workspace/lib\n" +
	"     写本の中の .env なども、コンテナから読めます。\n" +
	"     このほか、全体設定（~/.config/sigil/config）の分も渡されます。\n" +
	"sigil: この契約を結ぶ（この設定で承認する）なら y を入力してください [y/N] "

// 読み取り専用のフォルダ（20 章）は、ホスト側の解決したパスとマウント先を見せ、中も読めることを添える
func TestContractScreenPlainMounts(t *testing.T) {
	u, buf := newTest(Plain, 0, 0)
	u.ContractScreen(ContractView{
		Files:  []string{".sigil"},
		Envs:   []string{"FOO"},
		Mounts: []ContractMount{{Host: "/h/src/lib", Container: "/workspace/lib"}},
	})
	if buf.String() != askWithMounts {
		t.Errorf("差がある:\n got: %q\nwant: %q", buf.String(), askWithMounts)
	}
	// 写本だけでも「（なし）」にはならない
	u, buf = newTest(Plain, 0, 0)
	u.ContractScreen(ContractView{Files: []string{".sigil"}, Mounts: []ContractMount{{Host: "/h/a", Container: "/workspace/a"}}})
	if strings.Contains(buf.String(), "（なし）") {
		t.Errorf("写本があるのに（なし）:\n%s", buf.String())
	}
}

func TestContractScreenColorMounts(t *testing.T) {
	for _, m := range []Mode{Color, Mono} {
		u, buf := newTest(m, 60, 40)
		long := "/h/" + strings.Repeat("deep/", 15) + "lib"
		u.ContractScreen(ContractView{Files: []string{".sigil"}, Mounts: []ContractMount{{Host: long, Container: "/workspace/lib"}}})
		out := ownSGR(buf.String())
		joined := strings.NewReplacer("\n", "", "│", "", " ", "").Replace(out)
		for _, want := range []string{"写本", "写本の場所が", "読み取り専用のフォルダ", "/workspace/lib", "写本の中の.envなども、コンテナから読めます。",
			// 長いパスも切り詰めず、折り返して全部見せる
			strings.ReplaceAll(long, " ", "")} {
			if !strings.Contains(joined, want) {
				t.Errorf("mode %d: %q がない:\n%s", m, want, out)
			}
		}
		for _, l := range strings.Split(out, "\n") {
			// 問いの行は固定の文で、枠の外なので除く
			if Width(l) > 59 && !strings.HasPrefix(l, "sigil: この契約を結ぶ") {
				t.Errorf("幅 %d: %q", Width(l), l)
			}
		}
	}
}
