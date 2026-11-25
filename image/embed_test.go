package sigilimage

import (
	"os"
	"path/filepath"
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
	// sigil-check をイメージに組み込む行がある（ベースイメージ名には依存しない）
	if !strings.Contains(string(df), "COPY sigil-check /usr/local/bin/sigil-check") {
		t.Error("Dockerfile の中身が想定と違う")
	}

	st, err := os.Stat(filepath.Join(dir, "sigil-check"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("sigil-check のモード = %o, want 755", st.Mode().Perm())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "Dockerfile,sigil-check" {
		t.Errorf("書き出されたファイル = %v, want [Dockerfile sigil-check]", names)
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
	// 遅い手順（キャッシュのままにするもの）は前
	if i := strings.Index(df, "RUN docker-php-ext-install"); i < 0 || i > refresh {
		t.Error("PHP 拡張のビルドが ARG SIGIL_REFRESH より後ろにある")
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
