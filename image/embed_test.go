package sigilimage

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestWriteContext(t *testing.T) {
	dir := t.TempDir()
	if err := WriteContext(dir); err != nil {
		t.Fatal(err)
	}

	df, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if string(df) != string(src) {
		t.Error("書き出した Dockerfile が埋め込み元と一致しない")
	}
	// sigil-check をコンパイルしてイメージに組み込む行がある（ベースイメージ名には依存しない）
	for _, s := range []string{
		"FROM go AS sigil-check-build",
		"COPY sigilcheck/ ./",
		"GOTOOLCHAIN=local GOPROXY=off go build",
		"COPY --chmod=755 --from=sigil-check-build /out/sigil-check /usr/local/bin/sigil-check",
	} {
		if !strings.Contains(string(df), s) {
			t.Errorf("Dockerfile に %q がない", s)
		}
	}

	// 書き出したファイルの一覧: Dockerfile と、sigilcheck/ の _test.go 以外の .go と go.mod
	var got []string
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := filepath.Glob("sigilcheck/*.go")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Dockerfile", "sigilcheck/go.mod"}
	for _, s := range srcs {
		if !strings.HasSuffix(s, "_test.go") {
			want = append(want, filepath.ToSlash(s))
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("書き出されたファイル = %v, want %v", got, want)
	}
	for _, name := range want {
		if name == "Dockerfile" || name == "sigilcheck/go.mod" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		orig, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != string(orig) {
			t.Errorf("%s が埋め込み元と一致しない", name)
		}
	}

	mod, err := os.ReadFile(filepath.Join(dir, "sigilcheck", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mod) != "module sigilcheck\n\ngo "+sigilcheckGoVersion+"\n" {
		t.Errorf("go.mod = %q", mod)
	}
}

func TestWriteContextErrors(t *testing.T) {
	// 書き出し先がない
	if err := WriteContext(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Error("ないフォルダに書き出せた")
	}
	// sigilcheck/ が先にある（同じ場所に 2 回書き出すと、古いものに混ざらないよう失敗する）
	dir := t.TempDir()
	if err := WriteContext(dir); err != nil {
		t.Fatal(err)
	}
	if err := WriteContext(dir); err == nil {
		t.Error("sigilcheck/ があるのに書き出せた")
	}
}

// sigilcheck/go.mod の go の行は、ルートの go.mod と同じ版にする（設計書 11 章）。
func TestSigilcheckGoVersionMatchesRoot(t *testing.T) {
	b, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go (\S+)$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("ルートの go.mod に go の行がない")
	}
	if string(m[1]) != sigilcheckGoVersion {
		t.Errorf("sigilcheckGoVersion = %s, ルートの go.mod は %s", sigilcheckGoVersion, m[1])
	}
}

// 書き出したビルドコンテキストの sigilcheck/ だけで、イメージと同じ指定（Linux、cgo なし、
// 外から何も取らない）でコンパイルできる。書き出し忘れたファイルや、Linux で通らないコードを見つける。
func TestWrittenContextCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("go build を実行する")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go がない")
	}
	dir := t.TempDir()
	if err := WriteContext(dir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(gobin, "build", "-trimpath", "-buildvcs=false", "-o", filepath.Join(t.TempDir(), "sigil-check"), ".")
	cmd.Dir = filepath.Join(dir, "sigilcheck")
	cmd.Env = append(os.Environ(),
		"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0",
		"GOTOOLCHAIN=local", "GOPROXY=off", "GOFLAGS=", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("書き出したソースをコンパイルできない: %v\n%s", err, out)
	}
}

// ARG SIGIL_REFRESH は、文字列が変わらないまま中身が新しくなる手順（stable / latest の
// ダウンロード、install.sh など）より前に置く。後ろに動かすと、それより前の手順が
// docker のキャッシュで使い回され、sigil build でツールが更新されなくなる（issue #16）。
func TestDockerfileRefreshBeforeTools(t *testing.T) {
	src, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	df := string(src)
	refresh := strings.Index(df, "\nARG SIGIL_REFRESH\n")
	if refresh < 0 {
		t.Fatal("ARG SIGIL_REFRESH がない")
	}
	// 遅い手順や大きなレイヤー（キャッシュのままにするもの）は前
	for _, s := range []string{
		"RUN docker-php-ext-install",
		"COPY --from=go /usr/local/go",
		"COPY --from=rust /usr/local/rustup",
		"useradd --uid",
	} {
		if i := strings.Index(df, s); i < 0 {
			t.Errorf("%q が Dockerfile にない（テストを直すこと）", s)
		} else if i > refresh {
			t.Errorf("%q が ARG SIGIL_REFRESH より後ろにある（毎回作り直される）", s)
		}
	}
	// 更新したい手順は後ろ
	for _, s := range []string{
		"releases/download/${NVIM_VERSION}",
		"npm install -g tree-sitter-cli",
		"uv python install",
		"wakatime-cli/releases/latest",
		"lazygit/releases/latest",
		"cli/cli/releases/latest",
		"https://claude.ai/install.sh",
	} {
		i := strings.Index(df, s)
		if i < 0 {
			t.Errorf("%q が Dockerfile にない（テストを直すこと）", s)
		} else if i < refresh {
			t.Errorf("%q が ARG SIGIL_REFRESH より前にある", s)
		}
	}
}
