package app

import (
	"os"
	"runtime/debug"
	"strings"
	"testing"
)

// ---- issue #6: --version ----

func TestVersionWorksOutsideProjectWithoutDocker(t *testing.T) {
	for _, arg := range []string{"--version", "-V"} {
		e := newEnv(t)
		e.cwd = "/"
		e.dockerErr = os.ErrNotExist
		if code := e.run(arg); code != 0 {
			t.Errorf("%s: code = %d, stderr = %s", arg, code, e.stderr.String())
		}
		out := e.stdout.String()
		if !strings.HasPrefix(out, "sigil ") || !strings.HasSuffix(out, ")\n") || strings.Count(out, "\n") != 1 {
			t.Errorf("%s: 1 行の版の表示にならない: %q", arg, out)
		}
		if e.stderr.Len() != 0 {
			t.Errorf("%s: 標準エラーに出した: %q", arg, e.stderr.String())
		}
		if len(e.runner.calls) != 0 {
			t.Errorf("%s: 外部コマンドを呼んだ: %v", arg, e.runner.calls)
		}
	}
}

// サブコマンドの version は用意しない（コンテナ内の version を -- なしで実行できるように）
func TestVersionSubcommandRunsInContainer(t *testing.T) {
	e := newEnv(t)
	if code := e.run("version"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if strings.HasPrefix(e.stdout.String(), "sigil ") {
		t.Errorf("sigil の版を表示した: %q", e.stdout.String())
	}
	if len(e.runner.calls) == 0 {
		t.Errorf("コンテナで実行していない")
	}
}

func vcsInfo(rev, modified string) *debug.BuildInfo {
	bi := &debug.BuildInfo{}
	if rev != "" {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.revision", Value: rev})
	}
	if modified != "" {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: "vcs.modified", Value: modified})
	}
	return bi
}

func TestVersionString(t *testing.T) {
	const rev = "e9dcf5c0123456789abcdef0123456789abcdef0"
	tests := []struct {
		name     string
		embedded string
		bi       *debug.BuildInfo
		ok       bool
		want     string
	}{
		{"埋め込みあり", "v0.1.0", vcsInfo(rev, "false"), true,
			"sigil v0.1.0 (commit e9dcf5c, go1.27.4, darwin/arm64)\n"},
		{"埋め込みなしは dev", "", vcsInfo(rev, "false"), true,
			"sigil dev (commit e9dcf5c, go1.27.4, darwin/arm64)\n"},
		{"手元に変更があれば -dirty", "v0.1.0-dirty", vcsInfo(rev, "true"), true,
			"sigil v0.1.0-dirty (commit e9dcf5c-dirty, go1.27.4, darwin/arm64)\n"},
		{"VCS の情報がなければコミットを省く", "", vcsInfo("", ""), true,
			"sigil dev (go1.27.4, darwin/arm64)\n"},
		{"ビルド情報が読めなくても表示する", "v0.1.0", nil, false,
			"sigil v0.1.0 (go1.27.4, darwin/arm64)\n"},
		{"短いリビジョンはそのまま", "", vcsInfo("abc", "false"), true,
			"sigil dev (commit abc, go1.27.4, darwin/arm64)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := versionString(tt.embedded, tt.bi, tt.ok, "go1.27.4", "darwin", "arm64")
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
