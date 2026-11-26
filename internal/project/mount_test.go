package project

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FrontWorksDev/sigil/internal/config"
)

// mountEnv は、ResolveMounts のテスト用の一時フォルダ一式。
// base/home がホーム、base/home/src が ro_root、base/home/src/app がプロジェクト。
type mountEnv struct {
	base, home, src string
	proj            Project
}

func newMountEnv(t *testing.T) mountEnv {
	t.Helper()
	base := realTemp(t)
	home := filepath.Join(base, "home")
	src := filepath.Join(home, "src")
	for _, d := range []string{
		filepath.Join(src, "app"), filepath.Join(src, "lib"), filepath.Join(src, "other", "lib"),
		filepath.Join(home, ".ssh"), filepath.Join(home, ".config", "sigil"), filepath.Join(home, ".kube"),
	} {
		mustMkdir(t, d)
	}
	return mountEnv{base: base, home: home, src: src, proj: New(filepath.Join(src, "app"))}
}

func (e mountEnv) resolve(ro []string, roots ...string) ([]Mount, error) {
	var r, rr []config.ReadOnly
	for i, p := range ro {
		r = append(r, config.ReadOnly{Path: p, Line: i + 1})
	}
	for i, p := range roots {
		rr = append(rr, config.ReadOnly{Path: p, Line: i + 1})
	}
	return ResolveMounts(MountInput{
		SigilPath: filepath.Join(e.proj.Root, ".sigil"), ReadOnly: r,
		GlobalPath: "/g/config", Roots: rr,
		Project: e.proj, Home: e.home, Extra: []string{filepath.Join(e.home, ".config", "sigil")},
	})
}

func TestResolveMountsValid(t *testing.T) {
	e := newMountEnv(t)
	got, err := e.resolve([]string{"~/src/lib", filepath.Join(e.src, "other", "lib") + "/"}, "~/src")
	if err == nil {
		t.Fatalf("同じ名前（lib）が 2 つあるのにエラーにならない: %v", got)
	}
	got, err = e.resolve([]string{"~/src/lib", "~/src/other"}, "~/src")
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{
		{Host: filepath.Join(e.src, "lib"), Container: "/workspace/lib"},
		{Host: filepath.Join(e.src, "other"), Container: "/workspace/other"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveMountsNoneNeedsNoRoot(t *testing.T) {
	// ro がなければ、ro_root を確かめない（存在しない ro_root で、すべてのプロジェクトが止まらないように）
	e := newMountEnv(t)
	got, err := e.resolve(nil, "~/nope")
	if err != nil || got != nil {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestResolveMountsUsesWrittenName(t *testing.T) {
	// マウント先の名前は、シンボリックリンクの先ではなく、書かれた名前にする
	e := newMountEnv(t)
	if err := os.Symlink(filepath.Join(e.src, "lib"), filepath.Join(e.src, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := e.resolve([]string{"~/src/alias"}, "~/src")
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{{Host: filepath.Join(e.src, "lib"), Container: "/workspace/alias"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResolveMountsColonCommaName(t *testing.T) {
	e := newMountEnv(t)
	mustMkdir(t, filepath.Join(e.src, "a,b"))
	got, err := e.resolve([]string{"~/src/a,b"}, "~/src")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Container != "/workspace/a-b" {
		t.Errorf("Container = %q", got[0].Container)
	}
}

func TestResolveMountsRejects(t *testing.T) {
	e := newMountEnv(t)
	mustMkdir(t, filepath.Join(e.home, "srcx", "lib"))
	mustMkdir(t, filepath.Join(e.src, "a:b"))
	mustMkdir(t, filepath.Join(e.src, "app", "sub"))
	if err := os.WriteFile(filepath.Join(e.src, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"tossh": filepath.Join(e.home, ".ssh"),
		"out":   filepath.Join(e.home, ".kube"),
		"proj":  filepath.Join(e.src, "app"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(e.src, name)); err != nil {
			t.Fatal(err)
		}
	}
	// 大文字小文字だけ違うホーム（APFS では同じ場所）
	upperHome := filepath.Join(e.base, "HOME")

	cases := []struct {
		name  string
		ro    string
		roots []string
		want  string
	}{
		// 案内する全体設定は、実際に読むパス（XDG_CONFIG_HOME で変わる）
		{"ro_root がない", "~/src/lib", nil, ":1: ro でマウントしてよい範囲が決まっていません。全体設定（/g/config）に ro_root"},
		{"ro_root の外", "~/.kube", []string{"~/src"}, "ro_root の中にありません"},
		{"名前が前方一致するだけの別フォルダ", "~/srcx/lib", []string{"~/src"}, "ro_root の中にありません"},
		{"ro_root の外へのリンク", "~/src/out", []string{"~/src"}, "ro_root の中にありません"},
		{"相対パス", "src/lib", []string{"~/src"}, "/ か ~/"},
		{"~ だけ", "~", []string{"~/src"}, "/ か ~/"},
		{"..", "~/src/app/../lib", []string{"~/src"}, ".."},
		{"表示されない文字", "~/src/l\u202eib", []string{"~/src"}, "表示されない文字"},
		{"存在しない", "~/src/nope", []string{"~/src"}, "存在しません"},
		{"フォルダではない", "~/src/file", []string{"~/src"}, "フォルダではありません"},
		{"~/.ssh を指すリンク（ro_root の外）", "~/src/tossh", []string{"~/src"}, "ro_root の中にありません"},
		{"~/.ssh そのもの（ro_root の外）", "~/.ssh", []string{"~/src"}, "ro_root の中にありません"},
		// 大文字小文字を区別しないファイルシステムでは ro_root の外として、区別するものでは存在しないとして拒否される。
		// CheckSafe の大文字小文字の扱いは TestResolveMountCheckSafeDefenceInDepth で確かめる
		{"大文字小文字の違うホーム（ro_root の外か、存在しない）", upperHome, []string{"~/src"}, ""},
		{"全角空白", "~/src/a\u3000b", []string{"~/src"}, "ASCII 以外の空白（U+3000）"},
		{"NBSP", "~/src/a\u00a0b", []string{"~/src"}, "ASCII 以外の空白（U+00A0）"},
		{": を含む", "~/src/a:b", []string{"~/src"}, `":"`},
		{"プロジェクトそのもの", "~/src/app", []string{"~/src"}, "プロジェクト"},
		{"プロジェクトを指すリンク", "~/src/proj", []string{"~/src"}, "プロジェクト"},
		{"プロジェクトの中", "~/src/app/sub", []string{"~/src"}, "プロジェクト"},
		{"名前が ..（Clean で消えない形）", "/..", []string{"~/src"}, ".."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.resolve([]string{c.ro}, c.roots...)
			if err == nil {
				t.Fatalf("エラーにならない: %v", got)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q を含む", err, c.want)
			}
		})
	}
}

func TestResolveMountsErrorHasLine(t *testing.T) {
	e := newMountEnv(t)
	_, err := e.resolve([]string{"~/src/lib", "~/src/nope"}, "~/src")
	want := filepath.Join(e.proj.Root, ".sigil") + ":2: "
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %v, want 先頭が %q", err, want)
	}
}

func TestResolveMountsRejectsProjectNameCollision(t *testing.T) {
	// 別の場所にある、プロジェクトと同じ名前のフォルダ
	e := newMountEnv(t)
	mustMkdir(t, filepath.Join(e.src, "other", "app"))
	_, err := e.resolve([]string{"~/src/other/app"}, "~/src")
	if err == nil || !strings.Contains(err.Error(), "/workspace/app") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveMountsRejectsBadRoot(t *testing.T) {
	e := newMountEnv(t)
	cases := map[string]string{
		"ホーム":    "~/",
		"相対パス":   "src",
		"秘密情報":   "~/.config",
		"~/.ssh": "~/.ssh",
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.resolve([]string{"~/src/lib"}, root)
			if err == nil || !strings.HasPrefix(err.Error(), "/g/config:1: ") {
				t.Errorf("err = %v", err)
			}
		})
	}
	// 危険な場所なら、直すのは全体設定なので、プロジェクト向けの案内（実行する場所）を出さない
	_, err := e.resolve([]string{"~/src/lib"}, "~/")
	if err == nil || !strings.Contains(err.Error(), "ro_root には、他人のコードに見せてよいリポジトリの置き場") ||
		strings.Contains(err.Error(), "プロジェクトのフォルダで実行") {
		t.Errorf("ro_root ~/ → %v", err)
	}
}

// resolveMount の中の CheckSafe は、ro_root がすでに CheckSafe を通っているので、ResolveMounts からは
// 通常は届かない（多重の守り）。消されたり壊れたりしても気づけるよう、ro_root の判定を通る範囲
// （CheckSafe をかけていない base）を直接渡して確かめる。
func TestResolveMountCheckSafeDefenceInDepth(t *testing.T) {
	e := newMountEnv(t)
	in := MountInput{Project: e.proj, Extra: []string{filepath.Join(e.home, ".config", "sigil")}}
	cases := []struct{ name, path, want string }{
		{"ホーム", e.home, "全体はマウントできません"},
		{"ホームを含む", e.base, "ホームディレクトリを含む"},
		{"~/.ssh", filepath.Join(e.home, ".ssh"), "秘密情報を含む場所"},
		{"守る場所（全体設定のフォルダ）", filepath.Join(e.home, ".config", "sigil"), "秘密情報を含む場所"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveMount(c.path, e.home, e.home, []string{e.base}, []string{e.base, e.base}, in)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q を含む", err, c.want)
			}
		})
	}
	// 大文字小文字だけ違うホーム（大文字小文字を区別しないファイルシステムでだけ、同じ場所として確かめられる）
	upper := filepath.Join(e.base, "HOME")
	if _, err := os.Stat(upper); err == nil {
		_, err := resolveMount(upper, e.home, e.home, []string{e.base}, []string{e.base, e.base}, in)
		if err == nil || !strings.Contains(err.Error(), "全体はマウントできません") {
			t.Errorf("大文字小文字の違うホーム: err = %v", err)
		}
	}
}

func TestVerifyMounts(t *testing.T) {
	e := newMountEnv(t)
	ok := []Mount{{Host: filepath.Join(e.src, "other", "lib"), Container: "/workspace/lib"}}
	if err := VerifyMounts(ok, e.home); err != nil {
		t.Fatalf("変わっていないのにエラー: %v", err)
	}
	if err := VerifyMounts(nil, e.home); err != nil {
		t.Errorf("マウントなし → %v", err)
	}

	// 途中のフォルダをシンボリックリンクに差し替える（別のプロジェクトのコンテナが書き込める場所を想定）
	other := filepath.Join(e.src, "other")
	if err := os.Rename(other, filepath.Join(e.src, "other-moved")); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(e.home, ".ssh", "lib"))
	if err := os.Symlink(filepath.Join(e.home, ".ssh"), other); err != nil {
		t.Fatal(err)
	}
	err := VerifyMounts(ok, e.home)
	if !errors.Is(err, ErrMountChanged) {
		t.Errorf("差し替えたのに ErrMountChanged にならない: %v", err)
	}

	// 消えただけなら ErrMountChanged ではない（呼ぶ側は「書き換えられた」とは言わない）
	gone := []Mount{{Host: filepath.Join(e.src, "gone"), Container: "/workspace/gone"}}
	if err := VerifyMounts(gone, e.home); err == nil || errors.Is(err, ErrMountChanged) {
		t.Errorf("消えた → %v", err)
	}

	// フォルダがファイルに置き換えられた
	file := filepath.Join(e.src, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMounts([]Mount{{Host: file, Container: "/workspace/file"}}, e.home); !errors.Is(err, ErrMountChanged) {
		t.Errorf("ファイル → %v", err)
	}

	// 記録の形が不正（イメージから受け継いだラベルもありうるので、表示されない文字も拒否し、エラーに生で載せない）
	for _, m := range []Mount{
		{Host: "/x\x1b]52;c;AAAA\x07", Container: "/workspace/a"},
		{Host: e.src, Container: "/workspace/a\x1b[2J"},
		{Host: "rel/lib", Container: "/workspace/lib"},
		{Host: e.src + "/../src", Container: "/workspace/src"},
		{Host: e.src, Container: "/home/dev"},
	} {
		if err := VerifyMounts([]Mount{m}, e.home); err == nil || !strings.Contains(err.Error(), "記録が不正") || strings.ContainsRune(err.Error(), 0x1b) {
			t.Errorf("%v → %v", m, err)
		}
	}
}

func TestMountErrorsDoNotEchoLinkTargets(t *testing.T) {
	// リンクの先は、別のプロジェクトのコンテナなどが決められる。端末の制御文字をエラーに載せない
	e := newMountEnv(t)
	evil := filepath.Join(e.src, "evil\x1b]52;c;AAAA\x07") // ro_root の中（途中がファイルで ENOTDIR になる経路）
	if err := os.WriteFile(evil, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// ro の途中が、制御文字を含む名前のファイルへのリンク（ENOTDIR）
	if err := os.Symlink(evil, filepath.Join(e.src, "trap")); err != nil {
		t.Fatal(err)
	}
	_, err := e.resolve([]string{"~/src/trap/sub"}, "~/src")
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) || !strings.Contains(err.Error(), "フォルダではないもの") {
		t.Errorf("ENOTDIR → %q", err)
	}

	// ro_root の外を指すリンクの先も、制御文字をそのまま出さない
	if err := os.Symlink(filepath.Join(e.base, "out\x1b[2J"), filepath.Join(e.src, "trap2")); err != nil {
		t.Fatal(err)
	}
	_, err = e.resolve([]string{"~/src/trap2"}, "~/src")
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) || !strings.Contains(err.Error(), "ro_root の中にありません") {
		t.Errorf("外を指すリンク → %q", err)
	}

	// 停止中のコンテナの確かめで、差し替えた先が制御文字を含む
	lib := filepath.Join(e.src, "lib")
	ms := []Mount{{Host: lib, Container: "/workspace/lib"}}
	evilDir := filepath.Join(e.base, "dir\x1b[2J")
	mustMkdir(t, evilDir)
	if err := os.Remove(lib); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evilDir, lib); err != nil {
		t.Fatal(err)
	}
	err = VerifyMounts(ms, e.home)
	if !errors.Is(err, ErrMountChanged) || strings.ContainsRune(err.Error(), 0x1b) {
		t.Errorf("差し替え → %q", err)
	}

	// 循環するリンク
	if err := os.Remove(lib); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lib, lib); err != nil {
		t.Fatal(err)
	}
	// 循環するリンクも、たどらずに「変わった」とする
	if err := VerifyMounts(ms, e.home); !errors.Is(err, ErrMountChanged) {
		t.Errorf("循環 → %q", err)
	}

	// 差し替えた先（/net/<ホスト> など）には触れない。触れれば「存在しません」になる
	if err := os.Remove(lib); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent-sigil-test/x", lib); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMounts(ms, e.home); !errors.Is(err, ErrMountChanged) || strings.Contains(err.Error(), "存在しません") {
		t.Errorf("外を指すリンク → %q", err)
	}
}

func TestResolveMountsRejectsFolderHoldingSymlinkedConfig(t *testing.T) {
	// ~/.config が ro_root の中の dotfiles へのリンクなら、ro_root（~/src）そのものが秘密情報を含むので拒否する
	e := newMountEnv(t)
	dot := filepath.Join(e.src, "dotfiles")
	mustMkdir(t, filepath.Join(dot, "config"))
	cfg := filepath.Join(e.home, ".config")
	if err := os.RemoveAll(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dot, "config"), cfg); err != nil {
		t.Fatal(err)
	}
	_, err := e.resolve([]string{"~/src/dotfiles"}, "~/src")
	if err == nil || !strings.Contains(err.Error(), "秘密情報を含む場所") || !strings.Contains(err.Error(), "ro_root") {
		t.Errorf("err = %v", err)
	}
	// ro_root を狭くしても、dotfiles そのものは拒否する
	mustMkdir(t, filepath.Join(e.src, "repos"))
	if _, err := e.resolve([]string{"~/src/dotfiles"}, "~/src/repos"); err == nil {
		t.Error("dotfiles が通ってしまう")
	}
}

func TestResolveMountsSkipsMissingRoot(t *testing.T) {
	// 外付けのディスクの ro_root が外れていても、ほかの ro_root の中の ro は使える
	e := newMountEnv(t)
	got, err := e.resolve([]string{"~/src/lib"}, "/Volumes/nope/src", "~/src")
	if err != nil || len(got) != 1 {
		t.Fatalf("got = %v, err = %v", got, err)
	}
	// どの ro_root にも入らなければエラー。飛ばした ro_root も知らせる
	_, err = e.resolve([]string{"~/src/lib"}, "/Volumes/nope/src")
	if err == nil || !strings.Contains(err.Error(), "ro_root の中にありません") || !strings.Contains(err.Error(), "存在しない ro_root（/g/config:1）は飛ばしました") {
		t.Errorf("err = %v", err)
	}
	// 存在しない ro_root を飛ばしても、危険な ro_root はこれまでどおり止める
	if _, err := e.resolve([]string{"~/src/lib"}, "/Volumes/nope/src", "~/"); err == nil || !strings.Contains(err.Error(), "/g/config:2: ro_root") {
		t.Errorf("危険な ro_root → %v", err)
	}
}

func TestResolveMountsChecksRootsBeforeTouchingPath(t *testing.T) {
	// ro_root の外は、ファイルシステムに触れる前に拒否する（/net/<ホスト> の autofs などに触れさせない）。
	// 存在しないパスでも「存在しません」ではなく「ro_root の中にありません」になることで、順番を確かめる
	e := newMountEnv(t)
	for _, ro := range []string{"/net/attacker.example/x", "/nonexistent-sigil-test/lib", "~/nope"} {
		_, err := e.resolve([]string{ro}, "~/src")
		if err == nil || !strings.Contains(err.Error(), "ro_root の中にありません") || strings.Contains(err.Error(), "存在しません") {
			t.Errorf("%s → %v", ro, err)
		}
	}
	// 大文字小文字だけ違う書き方は、入口では通し、解決したパスで本来の確かめをする
	if _, err := os.Stat(filepath.Join(e.home, "SRC")); err == nil {
		if _, err := e.resolve([]string{"~/SRC/lib"}, "~/src"); err != nil {
			t.Errorf("~/SRC/lib → %v", err)
		}
	}
	// 前方一致するだけの別フォルダは、入口で拒否する
	if !lexWithin("/a/src", "/a/src") || !lexWithin("/a/src/x", "/a/src/") || lexWithin("/a/srcx", "/a/src") || lexWithin("/a", "/a/src") {
		t.Error("lexWithin の判定が違う")
	}
}

func TestCheckPrintableRejectsBlankLookingLetters(t *testing.T) {
	for _, r := range []rune{0x115F, 0x1160, 0x2800, 0x3164, 0xFFA0} {
		p := "/x/lib" + string(r) + "v2"
		if err := checkPrintable(p); err == nil || !strings.Contains(err.Error(), "空白に見える文字") {
			t.Errorf("U+%04X → %v", r, err)
		}
	}
}

func TestVerifyMountsRechecksSafe(t *testing.T) {
	// 作成のあとで ~/.config をそのフォルダの中にリンクしたら、起動しない
	e := newMountEnv(t)
	dot := filepath.Join(e.src, "dotfiles")
	mustMkdir(t, filepath.Join(dot, "config"))
	ms := []Mount{{Host: dot, Container: "/workspace/dotfiles"}}
	if err := VerifyMounts(ms, e.home); err != nil {
		t.Fatalf("リンクの前 → %v", err)
	}
	cfg := filepath.Join(e.home, ".config")
	if err := os.RemoveAll(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dot, "config"), cfg); err != nil {
		t.Fatal(err)
	}
	if err := VerifyMounts(ms, e.home); err == nil || !strings.Contains(err.Error(), "秘密情報を含む場所") {
		t.Errorf("リンクの後 → %v", err)
	}
	// 守る場所（extra）の中も拒否する
	extra := filepath.Join(e.src, "state")
	mustMkdir(t, extra)
	if err := VerifyMounts([]Mount{{Host: extra, Container: "/workspace/state"}}, e.home, extra); err == nil {
		t.Error("extra の中が通ってしまう")
	}
}

func TestResolveMountsRejectsProjectPathBeforeResolving(t *testing.T) {
	// プロジェクトの中のリンクは、解決する前に拒否する（リンクの先に触れない）
	e := newMountEnv(t)
	if err := os.Symlink("/nonexistent-sigil-test/x", filepath.Join(e.proj.Root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := e.resolve([]string{"~/src/app/link"}, "~/src")
	if err == nil || !strings.Contains(err.Error(), "プロジェクトの中") || strings.Contains(err.Error(), "存在しません") {
		t.Errorf("err = %v", err)
	}
}

func TestResolveMountsRejectsProjectViaLinkedRoot(t *testing.T) {
	// ro_root がシンボリックリンク（~/code -> ~/src）でも、プロジェクトの中のリンクを解決する前に拒否する
	e := newMountEnv(t)
	if err := os.Symlink(e.src, filepath.Join(e.home, "code")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent-sigil-test/x", filepath.Join(e.proj.Root, "link")); err != nil {
		t.Fatal(err)
	}
	_, err := e.resolve([]string{"~/code/app/link"}, "~/code")
	if err == nil || !strings.Contains(err.Error(), "プロジェクトの中") || strings.Contains(err.Error(), "存在しません") {
		t.Errorf("err = %v", err)
	}
	// ほかのフォルダは、これまでどおり通る
	if _, err := e.resolve([]string{"~/code/lib"}, "~/code"); err != nil {
		t.Errorf("~/code/lib → %v", err)
	}
	if r, rest, ok := rootStart("/a/raw/x/y", []string{"/r/res", "/a/raw"}); !ok || r != "/r/res" || len(rest) != 2 || rest[0] != "x" {
		t.Errorf("rootStart = %v %v %v", r, rest, ok)
	}
	if _, _, ok := rootStart("/a/rawx/y", []string{"/r/res", "/a/raw"}); ok {
		t.Error("前方一致するだけの別フォルダが ro_root の中とされた")
	}
	// 大文字小文字の変換でバイト長が変わる文字（ケルビン記号）でも、落ちない
	for _, c := range [][2]string{{"/a/k", "/a/\u212a"}, {"/a/\u212a", "/a/k"}, {"/a/k/x", "/a/\u212a"}, {"/a/\u212a/x", "/a/k"}} {
		rootStart(c[0], []string{"/r/res", c[1]})
	}
}

func TestWalkResolve(t *testing.T) {
	e := newMountEnv(t)
	link := func(target, at string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(e.src, at)); err != nil {
			t.Fatal(err)
		}
	}
	lib := filepath.Join(e.src, "lib")
	link("lib", "rel")           // 相対
	link("../src/lib", "updown") // いったん ro_root の外（ホーム）に上がって戻る
	link(lib, "abs")             // 絶対
	link("rel", "chain")         // リンクのリンク
	for _, ro := range []string{"~/src/rel", "~/src/updown", "~/src/abs", "~/src/chain"} {
		got, err := e.resolve([]string{ro}, "~/src")
		if err != nil || len(got) != 1 || got[0].Host != lib {
			t.Errorf("%s → %v, %v", ro, got, err)
		}
	}

	// ほかのリポジトリ（別のプロジェクトのコンテナが書ける）に置かれた、ro_root の外へのリンクには触れない
	link("/net/attacker.example/share", "net")
	link("../.ssh", "escape")
	link("../nope-outside/x", "escape2") // 外の存在しない場所。触れれば「存在しません」になる
	link("/x/../y", "dotdot")
	for ro, want := range map[string]string{
		"~/src/net":     "ro_root の中にありません",
		"~/src/escape":  "ro_root の中にありません",
		"~/src/escape2": "ro_root の中にありません",
		"~/src/dotdot":  `".." があるので`,
	} {
		_, err := e.resolve([]string{ro}, "~/src")
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "存在しません") {
			t.Errorf("%s → %v", ro, err)
		}
	}

	// 循環
	link("loop2", "loop1")
	link("loop1", "loop2")
	if _, err := e.resolve([]string{"~/src/loop1"}, "~/src"); err == nil || !strings.Contains(err.Error(), "循環") {
		t.Errorf("循環 → %v", err)
	}
}

func TestWalkResolveProjectLinkUnderOtherSpelling(t *testing.T) {
	// プロジェクトの中のリンクは、NFD/NFC や大文字小文字の違う書き方でもたどらない（ファイルの同一性で判定する）
	e := newMountEnv(t)
	nfc := filepath.Join(e.src, "café")
	mustMkdir(t, nfc)
	if err := os.Symlink("/nonexistent-sigil-test/x", filepath.Join(nfc, "link")); err != nil {
		t.Fatal(err)
	}
	in := MountInput{Project: New(nfc), Home: e.home}
	lexRoots := []string{e.src, e.src}
	for _, spelled := range []string{filepath.Join(e.src, "café"), filepath.Join(e.src, "CAFÉ")} {
		if _, err := os.Stat(spelled); err != nil {
			continue // 正規化や大文字小文字を区別するファイルシステムでは、別のフォルダなので確かめない
		}
		_, err := walkResolve(filepath.Join(spelled, "link"), lexRoots, in.Project.Root)
		if err == nil || !strings.Contains(err.Error(), "プロジェクトの中のシンボリックリンク") {
			t.Errorf("%q → %v", spelled, err)
		}
	}
}
