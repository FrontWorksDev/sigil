package secret

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseEnv(t *testing.T) {
	in := strings.Join([]string{
		"# comment",
		"",
		"DATABASE_URL=op://dev/db/url",
		`export STRIPE_KEY="op://dev/stripe/secret"`,
		"PLAIN=literal-secret-value",
		"  SPACED = op://dev/x/y  ",
		"CRLF=op://dev/c/d\r",
	}, "\n")
	got, err := ParseEnv("/p/.env", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Name: "DATABASE_URL", Ref: "op://dev/db/url", Scheme: Op},
		{Name: "STRIPE_KEY", Ref: "op://dev/stripe/secret", Scheme: Op},
		{Name: "PLAIN", Ref: "", Scheme: None}, // 参照ではない値は持たない
		{Name: "SPACED", Ref: "op://dev/x/y", Scheme: Op},
		{Name: "CRLF", Ref: "op://dev/c/d", Scheme: Op},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseEnv =\n%v\nwant\n%v", got, want)
	}
}

func TestParseEnvOpUnchanged(t *testing.T) {
	got, err := ParseEnv("/p/.env", []byte("A=op://v/i/f\n"))
	if err != nil || len(got) != 1 || got[0].Scheme != Op || got[0].Ref != "op://v/i/f" {
		t.Errorf("ParseEnv = %v, %v", got, err)
	}
}

func TestParseEnvKeychain(t *testing.T) {
	in := "A=keychain://wakatime\nexport B=\"keychain://gh.work\"\nC='keychain://x_y-1'\n"
	got, err := ParseEnv("/p/.env", []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %v", len(got), got)
	}
	for _, e := range got {
		if e.Scheme != Keychain {
			t.Errorf("%s: Scheme = %v, want Keychain", e.Name, e.Scheme)
		}
	}
	if got[0].Service() != "wakatime" || got[1].Service() != "gh.work" || got[2].Service() != "x_y-1" {
		t.Errorf("Service = %q %q %q", got[0].Service(), got[1].Service(), got[2].Service())
	}
	if got[1].Ref != "keychain://gh.work" {
		t.Errorf("Ref = %q", got[1].Ref)
	}
}

func TestParseEnvKeychainRejects(t *testing.T) {
	for _, svc := range []string{
		"-oops", "a/b", "a b", "", "a\x1bb", "a\u202eb",
		strings.Repeat("a", 129), "x  # comment",
	} {
		_, err := ParseEnv("/p/.env", []byte("FOO=keychain://"+svc+"\n"))
		if err == nil {
			t.Errorf("%q を受け付けた", svc)
			continue
		}
		if err.Error() != "/p/.env:1: FOO のキーチェーンの参照に使えない文字があります" {
			t.Errorf("%q: err = %q", svc, err.Error())
		}
	}
	// 128 文字ちょうどは使える
	if _, err := ParseEnv("/p/.env", []byte("FOO=keychain://"+strings.Repeat("a", 128)+"\n")); err != nil {
		t.Errorf("128 文字: %v", err)
	}
}

func TestParseEnvRejectsUnsafeRef(t *testing.T) {
	// 確認の画面を書き換える端末の制御文字や、op run の解釈を変えうる文字を含む参照は拒否する
	for _, val := range []string{
		"op://v/\x1b[2Ji/f",
		"op://v/i\x07/f",
		"op://v/i/f\x7f",
		"op://v/i\u009b/f",
		`op://v/i"x/f`,
		`op://v/i\x/f`,
		"op://${VAULT}/i/f",
		"op://v/i/f\rEVIL=op://a/b/c",
		// 表示を並べ替えたり、見えなかったりする Unicode の書式文字・区切り文字
		"op://v/\u202efdrowssap/f", // RIGHT-TO-LEFT OVERRIDE
		"op://v/i\u200b/f",         // ZERO WIDTH SPACE
		"op://v/i\u2028/f",         // LINE SEPARATOR
		"op://v/i\ufeff/f",         // 途中の BOM
		"op://v/i\u2066x\u2069/f",  // 双方向の分離
	} {
		_, err := ParseEnv("/p/.env", []byte("FOO="+val+"\n"))
		if err == nil {
			t.Errorf("%q を受け付けた", val)
			continue
		}
		if err.Error() != "/p/.env:1: FOO の 1Password 参照に使えない文字があります" {
			t.Errorf("%q: err = %q", val, err.Error())
		}
	}
	// 日本語の項目名や空白（全角の空白を含む）は使える
	for _, ref := range []string{"op://個人/AWS ルート/password", "op://個人/開発　用/パスワード"} {
		got, err := ParseEnv("/p/.env", []byte("FOO="+ref+"\n"))
		if err != nil || got[0].Ref != ref {
			t.Errorf("%q = %v, %v", ref, got, err)
		}
	}
}

func TestRenderEnv(t *testing.T) {
	got := string(RenderEnv([]Entry{
		{Name: "A", Ref: "op://v/i/f", Scheme: Op},
		{Name: "B", Ref: "op://個人/AWS ルート/password", Scheme: Op},
	}))
	want := "A=\"op://v/i/f\"\nB=\"op://個人/AWS ルート/password\"\n"
	if got != want {
		t.Errorf("RenderEnv =\n%q\nwant\n%q", got, want)
	}
	if RenderEnv(nil) != nil {
		t.Error("空なら nil")
	}
}

func TestRenderEnvMixed(t *testing.T) {
	got := string(RenderEnv([]Entry{
		{Name: "A", Ref: "op://v/i/f", Scheme: Op},
		{Name: "B", Ref: "keychain://w", Scheme: Keychain},
	}))
	want := "A=\"op://v/i/f\"\nB=\"keychain://w\"\n"
	if got != want {
		t.Errorf("RenderEnv =\n%q\nwant\n%q", got, want)
	}
}

func TestParseEnvStripsBOM(t *testing.T) {
	got, err := ParseEnv("/p/.env", []byte("\xef\xbb\xbfA=op://v/i/f\n"))
	if err != nil || len(got) != 1 || got[0].Name != "A" {
		t.Errorf("ParseEnv = %v, %v", got, err)
	}
}

func TestParseEnvErrorsDoNotLeakValues(t *testing.T) {
	cases := map[string]string{
		"no-equals-secret-value\n": "/p/.env:1: KEY=VALUE の形ではありません",
		"1BAD=secret-value\n":      "/p/.env:1: 環境変数名が不正です",
		"A=1\n$(id)=secret\n":      "/p/.env:2: 環境変数名が不正です",
	}
	for in, want := range cases {
		_, err := ParseEnv("/p/.env", []byte(in))
		if err == nil || err.Error() != want {
			t.Errorf("ParseEnv(%q) = %v, want %q", in, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("値がエラーに出た: %v", err)
		}
	}
}
