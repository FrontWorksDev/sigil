package approval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"sigil/internal/config"
	"sigil/internal/project"
	"sigil/internal/secret"
	"sigil/internal/ui"
)

func TestSumMatchesShasum(t *testing.T) {
	content := []byte("env A\n")
	h := sha256.Sum256(content)
	if got := Sum(content); got != hex.EncodeToString(h[:]) || len(got) != 64 {
		t.Errorf("Sum = %q", got)
	}
}

func TestRecordModesAndContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sigil", "approved")
	s := Store{Dir: dir}
	sum := Sum([]byte("env A\n"))
	if err := s.Record("abcd1234", sum); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "abcd1234"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != sum+"\n" {
		t.Errorf("中身 = %q", b)
	}
	fi, _ := os.Stat(filepath.Join(dir, "abcd1234"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("ファイルのモード = %o", fi.Mode().Perm())
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("フォルダのモード = %o", di.Mode().Perm())
	}
}

func TestIsApproved(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	sum := Sum([]byte("env A\n"))
	if s.IsApproved("k", sum) {
		t.Error("記録がないのに承認済み")
	}
	// シェル版の記録（末尾改行あり）も、改行なしも受け付ける
	for _, content := range []string{sum + "\n", sum} {
		os.WriteFile(filepath.Join(dir, "k"), []byte(content), 0o600)
		if !s.IsApproved("k", sum) {
			t.Errorf("%q が承認済みにならない", content)
		}
	}
	if s.IsApproved("k", Sum([]byte("env B\n"))) {
		t.Error("内容が変わったのに承認済み")
	}
}

func TestFingerprint(t *testing.T) {
	sigil := []byte("env A\n")
	// シェル版の承認記録との互換はもうないので、.env がないときも同じ形式で計算する（特別扱いしない）
	if Fingerprint(sigil, nil) != Fingerprint(sigil, []byte{}) {
		t.Error(".env がないときだけ特別扱いしている")
	}
	if Fingerprint(sigil, nil) == Sum(sigil) {
		t.Error(".sigil だけの生のハッシュになっている（区切りのない形式）")
	}
	withEnv := Fingerprint(sigil, []byte("A=op://v/i/f\n"))
	if withEnv == Sum(sigil) {
		t.Error(".env がハッシュに含まれていない")
	}
	if withEnv == Fingerprint(sigil, []byte("A=op://v/other/f\n")) {
		t.Error(".env の参照先が変わってもハッシュが同じ")
	}
	if Fingerprint(nil, []byte("A=op://v/i/f\n")) == "" {
		t.Error(".sigil がなく .env だけのとき")
	}
}

func TestAsk(t *testing.T) {
	c := config.Config{Envs: []string{"DATABASE_URL"}, Ports: []config.Port{{Host: "8080", Container: "80"}}}
	refs := []secret.Entry{{Name: "DATABASE_URL", Ref: "op://dev/db/url", Scheme: secret.Op}, {Name: "LITERAL", Ref: ""}}
	cases := map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YES\n": true, "y": true,
		"n\n": false, "": false, "\n": false, "yes please\n": false, "Yes\n": false,
	}
	for in, want := range cases {
		var out bytes.Buffer
		if got := Ask(strings.NewReader(in), ui.New(&out, ui.Options{Mode: ui.Plain}), []string{"/p/.sigil", "/p/.env"}, c, refs, nil, nil); got != want {
			t.Errorf("Ask(%q) = %v, want %v", in, got, want)
		}
		s := out.String()
		for _, must := range []string{"/p/.sigil", "/p/.env", "DATABASE_URL", "op://dev/db/url", "LITERAL", "127.0.0.1:8080:80", "全体設定", "[y/N]",
			// 用語には、必ず実際の意味を添える
			"契約", "持ち込むもの（ホストの環境変数）", "門（ポート）", "禁書庫", "1Password"} {
			if !strings.Contains(s, must) {
				t.Errorf("表示に %q がない:\n%s", must, s)
			}
		}
	}
}

// 環境変数とポートの値の列がそろっていること（読みやすさのため。全角は 2 桁と数える）
func TestAskColumnsAligned(t *testing.T) {
	c := config.Config{Envs: []string{"DATABASE_URL"}, Ports: []config.Port{{Host: "8080", Container: "80"}}}
	var out bytes.Buffer
	Ask(strings.NewReader("n\n"), ui.New(&out, ui.Options{Mode: ui.Plain}), []string{"/p/.sigil"}, c, nil, nil, nil)
	col := map[string]int{}
	for _, l := range strings.Split(out.String(), "\n") {
		for _, v := range []string{"DATABASE_URL", "127.0.0.1:8080:80"} {
			if i := strings.Index(l, v); i >= 0 {
				w := 0
				for _, r := range l[:i] {
					if r >= 0x2E80 {
						w += 2
					} else {
						w++
					}
				}
				col[v] = w
			}
		}
	}
	if col["DATABASE_URL"] == 0 || col["DATABASE_URL"] != col["127.0.0.1:8080:80"] {
		t.Errorf("列がそろっていない: %v\n%s", col, out.String())
	}
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Color では枠と記号つきの画面になり、見出しと用語はそのまま読める
func TestAskColor(t *testing.T) {
	c := config.Config{Envs: []string{"DATABASE_URL"}}
	var out bytes.Buffer
	got := Ask(strings.NewReader("y\n"), ui.New(&out, ui.Options{Mode: ui.Color, Cols: 80, Rows: 24}), []string{"/p/.sigil"}, c, nil, nil, nil)
	if !got {
		t.Error("y で承認されない")
	}
	// 色の指定（ESC [ ... m）は、記号と文字の間にも入るので、取り除いてから見る
	s := ansi.ReplaceAllString(out.String(), "")
	// Color では「持ち込むもの」と意味の説明が別の列に分かれる（Plain の括弧書きにはならない）
	for _, must := range []string{"⟡ 契約の確認", "持ち込むもの", "ホストの環境変数", "DATABASE_URL"} {
		if !strings.Contains(s, must) {
			t.Errorf("表示に %q がない:\n%s", must, s)
		}
	}
}

// 提供元はスキームから決め、全体の設定に負けた名前には注記を添える
func TestAskSourceAndOverridden(t *testing.T) {
	refs := []secret.Entry{
		{Name: "A", Ref: "op://v/i/f", Scheme: secret.Op},
		{Name: "W", Ref: "keychain://wakatime", Scheme: secret.Keychain},
	}
	var out bytes.Buffer
	Ask(strings.NewReader("n\n"), ui.New(&out, ui.Options{Mode: ui.Plain}), []string{"/p/.env"}, config.Config{}, refs, map[string]bool{"W": true}, nil)
	var a, w string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "op://v/i/f") {
			a = l
		}
		if strings.Contains(l, "keychain://wakatime") {
			w = l
		}
	}
	if !strings.Contains(a, "1Password") || strings.Contains(a, "全体の設定が優先") {
		t.Errorf("1Password の行 = %q", a)
	}
	if !strings.Contains(w, "キーチェーン") || !strings.Contains(w, "全体の設定が優先されるため使わない") {
		t.Errorf("キーチェーンの行 = %q", w)
	}
}

func TestFingerprintMounts(t *testing.T) {
	sigil, env := []byte("ro ~/src/lib\n"), []byte("A=\"op://v/i/f\"\n")
	// ro がなければ今までと同じハッシュ（既存の承認記録を使えるように）
	if Fingerprint(sigil, env) != Fingerprint(sigil, env, nil...) {
		t.Error("マウントがないのにハッシュが変わる")
	}
	a := Fingerprint(sigil, env, project.Mount{Host: "/h/src/lib", Container: "/workspace/lib"})
	if a == Fingerprint(sigil, env) {
		t.Error("マウントがハッシュに含まれていない")
	}
	// .sigil が同じでも、シンボリックリンクの向きが変われば聞き直す
	if a == Fingerprint(sigil, env, project.Mount{Host: "/h/src/lib-v2", Container: "/workspace/lib"}) {
		t.Error("解決したパスが変わってもハッシュが同じ")
	}
	if a == Fingerprint(sigil, env, project.Mount{Host: "/h/src/lib", Container: "/workspace/lib2"}) {
		t.Error("マウント先が変わってもハッシュが同じ")
	}
	// 区切りをずらした別の組が同じにならない
	if Fingerprint(sigil, env, project.Mount{Host: "/a", Container: "/b"}, project.Mount{Host: "/c", Container: "/d"}) ==
		Fingerprint(sigil, env, project.Mount{Host: "/a", Container: "/b\x00/c"}, project.Mount{Host: "/d"}) {
		t.Error("区切りをずらした組が同じハッシュになる")
	}
}

func TestAskShowsMounts(t *testing.T) {
	var out bytes.Buffer
	Ask(strings.NewReader("n\n"), ui.New(&out, ui.Options{Mode: ui.Plain}), []string{"/p/.sigil"}, config.Config{}, nil, nil,
		[]project.Mount{{Host: "/h/src/lib", Container: "/workspace/lib"}})
	for _, must := range []string{"写本（読み取り専用のフォルダ）", "/h/src/lib → /workspace/lib", ".env なども"} {
		if !strings.Contains(out.String(), must) {
			t.Errorf("表示に %q がない:\n%s", must, out.String())
		}
	}
}
