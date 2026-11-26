package docker

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/FrontWorksDev/sigil/internal/config"
	"github.com/FrontWorksDev/sigil/internal/project"
)

func sampleSpec() RunSpec {
	return RunSpec{
		Name:          "sigil-proj-33bf5961",
		ProjectRoot:   "/Users/k/src/proj",
		ContainerPath: "/workspace/proj",
		Home:          "/Users/k",
		Image:         "sigil:latest",
		HasGit:        true,
		HasNvimConfig: true,
		Envs:          []string{"DATABASE_URL", "WAKATIME_API_KEY"},
		Ports:         []config.Port{{Host: "5173", Container: "5173"}, {Host: "8080", Container: "80"}},
	}
}

// flagValues は args から flag の直後の値をすべて集める。
func flagValues(args []string, flag string) []string {
	var out []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			out = append(out, args[i+1])
		}
	}
	return out
}

func TestRunArgsIsolation(t *testing.T) {
	args := RunArgs(sampleSpec())
	if args[0] != "run" || args[1] != "-d" {
		t.Errorf("先頭 = %v", args[:2])
	}
	for _, want := range []string{"--init", "--cap-drop=ALL", "--security-opt=no-new-privileges"} {
		if !slices.Contains(args, want) {
			t.Errorf("%s がない", want)
		}
	}
	labels := flagValues(args, "--label")
	for _, want := range []string{"sigil.project=/Users/k/src/proj", "sigil.layout=2"} {
		if !slices.Contains(labels, want) {
			t.Errorf("ラベル %s がない: %v", want, labels)
		}
	}
	if got := flagValues(args, "--name"); !reflect.DeepEqual(got, []string{"sigil-proj-33bf5961"}) {
		t.Errorf("--name = %v", got)
	}
	if got := flagValues(args, "--hostname"); !reflect.DeepEqual(got, []string{"sigil"}) {
		t.Errorf("--hostname = %v", got)
	}
}

func TestRunArgsMounts(t *testing.T) {
	s := sampleSpec()
	vols := flagValues(RunArgs(s), "-v")
	want := []string{
		"/Users/k/src/proj:/workspace/proj",
		"/Users/k/src/proj/.git:/workspace/proj/.git:ro",
		"/Users/k/.config/nvim:/home/dev/.config/nvim:ro",
		"sigil-nvim-data:/home/dev/.local/share/nvim",
		"sigil-nvim-state:/home/dev/.local/state/nvim",
		"sigil-claude:/home/dev/.claude",
		"sigil-cache:/home/dev/.cache",
		"sigil-go:/home/dev/go",
		"sigil-cargo:/home/dev/.cargo",
	}
	if !reflect.DeepEqual(vols, want) {
		t.Errorf("-v =\n%v\nwant\n%v", vols, want)
	}

	s.HasGit, s.HasNvimConfig = false, false
	for _, v := range flagValues(RunArgs(s), "-v") {
		if strings.Contains(v, ".git") || strings.Contains(v, ".config/nvim") {
			t.Errorf("ないはずのマウント: %s", v)
		}
	}
}

// gh のキャッシュは非公開のリポジトリのデータを含むので、共有の sigil-cache ではなく、
// コンテナごとの tmpfs に置く（issue #12）。
func TestRunArgsGhCacheIsPerContainer(t *testing.T) {
	got := flagValues(RunArgs(sampleSpec()), "--tmpfs")
	want := []string{"/home/dev/.cache/gh:rw,noexec,nosuid,nodev"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("--tmpfs = %v, want %v", got, want)
	}
}

func TestRunArgsNoSecretMounts(t *testing.T) {
	s := sampleSpec()
	for _, v := range flagValues(RunArgs(s), "-v") {
		src, _, _ := strings.Cut(v, ":")
		switch {
		case src == s.Home, src == s.Home+"/":
			t.Errorf("ホームをマウントしている: %s", v)
		case strings.Contains(src, ".ssh"), strings.Contains(src, ".aws"), strings.Contains(src, "docker.sock"):
			t.Errorf("秘密情報をマウントしている: %s", v)
		case src == s.ProjectRoot, src == s.ProjectRoot+"/.git", src == s.Home+"/.config/nvim":
		case strings.HasPrefix(src, "sigil-") && !strings.Contains(src, "/"):
		default:
			t.Errorf("想定外のマウント: %s", v)
		}
	}
}

func TestRunArgsNoEnvValues(t *testing.T) {
	envs := flagValues(RunArgs(sampleSpec()), "-e")
	for _, e := range envs {
		if strings.Contains(e, "=") && !strings.HasPrefix(e, "SIGIL_") && !strings.HasPrefix(e, "GIT_CONFIG_") {
			t.Errorf("値付きの -e: %s", e)
		}
	}
	for _, want := range []string{
		"SIGIL_PROJECT_DIR=/workspace/proj",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=/workspace/proj",
		"DATABASE_URL",
		"WAKATIME_API_KEY",
	} {
		if !slices.Contains(envs, want) {
			t.Errorf("-e %s がない", want)
		}
	}
}

func TestRunArgsPorts(t *testing.T) {
	ports := flagValues(RunArgs(sampleSpec()), "-p")
	want := []string{"127.0.0.1:5173:5173", "127.0.0.1:8080:80"}
	if !reflect.DeepEqual(ports, want) {
		t.Errorf("-p = %v", ports)
	}
	for _, p := range ports {
		if !strings.HasPrefix(p, "127.0.0.1:") {
			t.Errorf("127.0.0.1 以外に公開: %s", p)
		}
	}
}

func TestRunArgsEndsWithWorkdirAndImage(t *testing.T) {
	args := RunArgs(sampleSpec())
	tail := args[len(args)-3:]
	if !reflect.DeepEqual(tail, []string{"-w", "/workspace/proj", "sigil:latest"}) {
		t.Errorf("末尾 = %v", tail)
	}
}

func TestExecArgs(t *testing.T) {
	got := ExecArgs("c", "/workspace/p/web", false, nil, []string{"nvim", "."})
	want := []string{"exec", "-i", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor",
		"-w", "/workspace/p/web", "c", "nvim", "."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tty なし = %v", got)
	}
	if got := ExecArgs("c", "/w", true, nil, []string{"bash", "-l"}); got[1] != "-it" {
		t.Errorf("tty あり = %v", got)
	}
}

func TestBuildArgs(t *testing.T) {
	// --pull と SIGIL_REFRESH は、利用者の引数（上書きできるように）より前に置く
	got := BuildArgs("sigil:dev-test", 501, 20, "r1", []string{"--no-cache"}, "/tmp/c")
	want := []string{"build", "--pull", "--build-arg", "USER_UID=501", "--build-arg", "USER_GID=20",
		"--build-arg", "SIGIL_REFRESH=r1", "-t", "sigil:dev-test", "--no-cache", "/tmp/c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildArgs = %v", got)
	}
}

func TestLsArgs(t *testing.T) {
	want := []string{"ps", "-a", "--filter", "label=sigil.project",
		"--format", "table {{.Names}}\t{{.Status}}\t{{.Label \"sigil.project\"}}"}
	if got := LsArgs(); !reflect.DeepEqual(got, want) {
		t.Errorf("LsArgs = %v", got)
	}
}

func TestExecArgsExtraEnv(t *testing.T) {
	got := ExecArgs("c", "/w", false, []string{"NO_COLOR=1"}, []string{"sigil-check"})
	want := []string{"exec", "-i", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor",
		"-e", "NO_COLOR=1", "-w", "/w", "c", "sigil-check"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v", got)
	}
}

func TestParseLs(t *testing.T) {
	out := "sigil-a-1\trunning\tUp 2 hours\t/src/a\nbroken line\nsigil-b-2\texited\tExited (0) 3 days ago\t/src/b\n"
	rows := ParseLs(out)
	if len(rows) != 2 || rows[0] != (LsRow{"sigil-a-1", "running", "Up 2 hours", "/src/a"}) || rows[1].State != "exited" {
		t.Errorf("rows = %v", rows)
	}
}

func TestRunArgsReadOnlyMounts(t *testing.T) {
	s := sampleSpec()
	s.ReadOnly = []project.Mount{
		{Host: "/Users/k/src/lib", Container: "/workspace/lib"},
		{Host: "/opt/src/other", Container: "/workspace/other"},
	}
	vols := flagValues(RunArgs(s), "-v")
	var ro []string
	for _, v := range vols {
		if strings.HasPrefix(v, "/Users/k/src/lib") || strings.HasPrefix(v, "/opt/") {
			ro = append(ro, v)
		}
	}
	// 解決したパスを読み取り専用で、書かれた順に
	want := []string{"/Users/k/src/lib:/workspace/lib:ro", "/opt/src/other:/workspace/other:ro"}
	if !reflect.DeepEqual(ro, want) {
		t.Errorf("ro = %v, want %v", ro, want)
	}
	// ro がなければ何も足さない
	if got := len(flagValues(RunArgs(sampleSpec()), "-v")); got != len(vols)-2 {
		t.Errorf("ro なしの -v の数 = %d", got)
	}

	// 承認したマウントをラベルに残す（停止中のコンテナを起動する前に比べるため）
	var label string
	for _, l := range flagValues(RunArgs(s), "--label") {
		if v, ok := strings.CutPrefix(l, ReadOnlyLabel+"="); ok {
			label = v
		}
	}
	// ラベルの形は、既存のコンテナに残るので固定する
	if want := `[{"host":"/Users/k/src/lib","container":"/workspace/lib"},{"host":"/opt/src/other","container":"/workspace/other"}]`; label != want {
		t.Errorf("ラベル = %s, want %s", label, want)
	}
	got, err := DecodeReadOnly(label)
	if err != nil || !reflect.DeepEqual(got, s.ReadOnly) {
		t.Errorf("ラベル %q → %v, %v", label, got, err)
	}
	// ro がなくても空のラベルを付け、イメージから受け継いだ sigil.ro を上書きする
	if !slices.Contains(flagValues(RunArgs(sampleSpec()), "--label"), ReadOnlyLabel+"=[]") {
		t.Errorf("ro なしのラベル = %v", flagValues(RunArgs(sampleSpec()), "--label"))
	}
	if ms, err := DecodeReadOnly("[]"); err != nil || len(ms) != 0 {
		t.Errorf("[] → %v, %v", ms, err)
	}
}
