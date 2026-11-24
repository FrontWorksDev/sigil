package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sigil/internal/config"
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
		"存在しない":  "~/nope",
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
			_, err := resolveMount(c.path, e.home, e.home, []string{e.base}, in)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q を含む", err, c.want)
			}
		})
	}
	// 大文字小文字だけ違うホーム（大文字小文字を区別しないファイルシステムでだけ、同じ場所として確かめられる）
	upper := filepath.Join(e.base, "HOME")
	if _, err := os.Stat(upper); err == nil {
		_, err := resolveMount(upper, e.home, e.home, []string{e.base}, in)
		if err == nil || !strings.Contains(err.Error(), "全体はマウントできません") {
			t.Errorf("大文字小文字の違うホーム: err = %v", err)
		}
	}
}
