package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/FrontWorksDev/sigil/internal/approval"
)

// ---- Task 9: コンテナの用意と exec ----

func (e *testEnv) lastExec() execCall {
	e.t.Helper()
	if len(e.execs) == 0 {
		e.t.Fatalf("Exec が呼ばれていない。stderr = %s", e.stderr.String())
	}
	return e.execs[len(e.execs)-1]
}

func (e *testEnv) runArgs() []string {
	e.t.Helper()
	runs := e.runner.find("docker run")
	if len(runs) != 1 {
		e.t.Fatalf("docker run の回数 = %d", len(runs))
	}
	return runs[0].args
}

func TestCreateThenExecBash(t *testing.T) {
	e := newEnv(t)
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	e.runArgs()
	x := e.lastExec()
	if x.argv0 != "/usr/local/bin/docker" {
		t.Errorf("argv0 = %q", x.argv0)
	}
	want := []string{"docker", "exec", "-i", "-e", "TERM=xterm-256color", "-e", "COLORTERM=truecolor",
		"-w", "/workspace/proj", "sigil-proj-" + e.key(), "bash", "-l"}
	if !reflect.DeepEqual(x.argv, want) {
		t.Errorf("argv =\n%v\nwant\n%v", x.argv, want)
	}
}

func TestExecTTY(t *testing.T) {
	e := newEnv(t)
	e.isTTY = true
	e.run()
	if got := e.lastExec().argv[2]; got != "-it" {
		t.Errorf("tty フラグ = %q", got)
	}
}

func TestExecInSubfolder(t *testing.T) {
	e := newEnv(t)
	e.cwd = filepath.Join(e.proj, "web", "src")
	mustMkdir(t, e.cwd)
	e.run()
	argv := e.lastExec().argv
	if i := slices.Index(argv, "-w"); argv[i+1] != "/workspace/proj/web/src" {
		t.Errorf("作業フォルダ = %q", argv[i+1])
	}
}

func TestCommandSelection(t *testing.T) {
	cases := []struct {
		args []string
		tail []string
	}{
		{[]string{"nvim", "."}, []string{"nvim", "."}},
		{[]string{"--", "stop"}, []string{"stop"}},
		{[]string{"--", "build", "x"}, []string{"build", "x"}},
		{[]string{"check"}, []string{"/usr/local/bin/sigil-check"}},
		{[]string{"--"}, []string{"bash", "-l"}},
	}
	for _, tc := range cases {
		e := newEnv(t)
		if code := e.run(tc.args...); code != 0 {
			t.Fatalf("%v: code = %d, %s", tc.args, code, e.stderr.String())
		}
		argv := e.lastExec().argv
		if got := argv[len(argv)-len(tc.tail):]; !reflect.DeepEqual(got, tc.tail) {
			t.Errorf("%v: 末尾 = %v", tc.args, got)
		}
		if len(e.runner.find("docker rm")) != 0 || len(e.runner.find("docker build")) != 0 {
			t.Errorf("%v: サブコマンドとして実行された", tc.args)
		}
	}
}

func TestExistingRunningIsReused(t *testing.T) {
	e := newEnv(t)
	e.container("running", "2")
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 || len(e.runner.find("docker start")) != 0 {
		t.Errorf("calls = %v", e.runner.calls)
	}
	e.lastExec()
}

func TestExistingStoppedIsStarted(t *testing.T) {
	e := newEnv(t)
	e.container("exited", "2")
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.runner.find("docker start sigil-proj-")) != 1 || len(e.runner.find("docker run")) != 0 {
		t.Errorf("calls = %v", e.runner.calls)
	}
	e.lastExec()
}

func TestExistingOldLayoutIsError(t *testing.T) {
	e := newEnv(t)
	e.container("running", "")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "古い sigil で作られています") || !strings.Contains(e.stderr.String(), "sigil stop") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	if len(e.execs) != 0 {
		t.Error("Exec を呼んだ")
	}
}

func TestMissingImage(t *testing.T) {
	e := newEnv(t)
	e.runner.on("docker image inspect", "", errors.New("no such image"))
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "sigil build") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 || len(e.execs) != 0 {
		t.Error("作成または Exec をした")
	}
}

func TestNoGitWarnsAndContinues(t *testing.T) {
	e := newEnv(t)
	os.Remove(filepath.Join(e.proj, ".git"))
	e.runner.on("git -C", "", errors.New("not a repo"))
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "git init / clone") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	for _, a := range e.runArgs() {
		if strings.Contains(a, ".git") {
			t.Errorf(".git をマウントしている: %s", a)
		}
	}
}

func TestNvimConfigMountedReadOnlyWhenPresent(t *testing.T) {
	e := newEnv(t)
	mustMkdir(t, filepath.Join(e.home, ".config", "nvim"))
	e.run()
	if !slices.Contains(e.runArgs(), e.home+"/.config/nvim:/home/dev/.config/nvim:ro") {
		t.Errorf("run = %v", e.runArgs())
	}
}

// ---- 承認 ----

func (e *testEnv) approvalFile() string {
	return filepath.Join(e.home, ".local", "state", "sigil", "approved", e.key())
}

func TestApprovalNoTTYAborts(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env FOO\n")
	e.vars["FOO"] = "bar"
	e.ttyErr = errors.New("device not configured")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker run")) != 0 || len(e.execs) != 0 {
		t.Error("中止したのに作成または Exec をした")
	}
}

func TestApprovalNoAbortsWithoutDocker(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env FOO\n")
	e.vars["FOO"] = "bar"
	e.tty = "n\n"
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "中止しました") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 || len(e.runner.find("docker image")) != 0 || len(e.execs) != 0 {
		t.Errorf("中止したのに docker を呼んだ: %v", e.runner.calls)
	}
	if _, err := os.Stat(e.approvalFile()); !os.IsNotExist(err) {
		t.Error("記録が書かれた")
	}
}

func TestApprovalYesCreatesAndRecords(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env FOO\nport 5173\n")
	e.vars["FOO"] = "bar"
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	args := e.runArgs()
	if !slices.Contains(args, "FOO") || !slices.Contains(args, "127.0.0.1:5173:5173") {
		t.Errorf("run = %v", args)
	}
	if _, err := os.Stat(e.approvalFile()); err != nil {
		t.Errorf("記録がない: %v", err)
	}
	// ここで値がどこにも出ていないこと
	for _, a := range args {
		if strings.Contains(a, "bar") {
			t.Errorf("値が argv に出ている: %s", a)
		}
	}
	if strings.Contains(e.stderr.String(), "bar") {
		t.Error("値が表示された")
	}

	// 2 回目（記録済み）は聞かない
	e2 := newEnvSharing(t, e)
	if code := e2.run(); code != 0 {
		t.Fatal(e2.stderr.String())
	}
	if e2.ttyOpened != 0 {
		t.Error("記録済みなのに聞いた")
	}

	// 内容が変わるとまた聞く
	e.write(".sigil", "env FOO\nenv OTHER\n")
	e3 := newEnvSharing(t, e)
	e3.tty = "n\n"
	if code := e3.run(); code != 1 || e3.ttyOpened != 1 {
		t.Errorf("内容が変わったのに聞かない: code=%d opened=%d", code, e3.ttyOpened)
	}
}

// newEnvSharing は、prev と同じホーム・プロジェクト・環境変数を使う新しい環境を作る。
func newEnvSharing(t *testing.T, prev *testEnv) *testEnv {
	e := newEnv(t)
	e.home, e.proj, e.cwd = prev.home, prev.proj, prev.cwd
	e.vars = prev.vars
	e.runner.on("git -C", e.proj+"\n", nil)
	return e
}

func TestGlobalConfigOnlyDoesNotAsk(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), "env GLOBAL_ONLY\n")
	e.vars["GLOBAL_ONLY"] = "x"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if e.ttyOpened != 0 {
		t.Error("全体設定なのに聞いた")
	}
	if !slices.Contains(e.runArgs(), "GLOBAL_ONLY") {
		t.Errorf("run = %v", e.runArgs())
	}
}

func TestConfigErrorStopsBeforeDocker(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env $(id)\n")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	want := "sigil: 契約の書を読み解けません\n       " + filepath.Join(e.proj, ".sigil") + ":1: 環境変数名が不正です: $(id)\n"
	if e.stderr.String() != want {
		t.Errorf("stderr = %q\nwant     %q", e.stderr.String(), want)
	}
	if len(e.runner.find("docker run")) != 0 || e.ttyOpened != 0 {
		t.Error("docker run または確認に進んだ")
	}
}

// ---- 環境変数 ----

func TestUnsetEnvIsNotPassedAndEmptyIs(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), "env MISSING\nenv EMPTY\n")
	e.vars["EMPTY"] = ""
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	args := e.runArgs()
	if slices.Contains(args, "MISSING") {
		t.Error("未設定の変数を渡した")
	}
	if !slices.Contains(args, "EMPTY") {
		t.Error("空文字の変数を渡していない")
	}
	if !strings.Contains(e.stderr.String(), "MISSING") {
		t.Errorf("注意が出ない: %q", e.stderr.String())
	}
}

func TestWakaTimeSetupOnlyWhenPassed(t *testing.T) {
	e := newEnv(t)
	e.run()
	if len(e.runner.find("docker exec")) != 0 {
		t.Error("WAKATIME_API_KEY がないのに設定した")
	}

	e = newEnv(t)
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), "env WAKATIME_API_KEY\n")
	e.vars["WAKATIME_API_KEY"] = "waka_secret"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if got := e.runner.find("docker exec sigil-proj-"); len(got) != 2 {
		t.Errorf("wakatime の exec = %v", got)
	}
	for _, c := range e.runner.calls {
		if strings.Contains(c.String(), "waka_secret") {
			t.Errorf("値が argv に出ている: %s", c)
		}
	}
}

// ---- op run ----

func TestOpRunReexecs(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env FOO\n")
	e.vars["FOO"] = "bar"
	e.tty = "y\n"
	// アプリの設定の行と、コメントの行が混ざっていても、op run に渡すのは確かめた参照だけ
	e.write(".env", "# app\nVITE_API_URL=http://localhost:3000\nFOO=op://vault/item/field\n")
	global := e.write(filepath.Join(e.home, ".config", "sigil", "secrets.env"), "W=op://v/i/f\n")

	if code := e.run("nvim", "."); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if len(e.execs) != 1 {
		t.Fatalf("Exec の回数 = %d", len(e.execs))
	}
	x := e.execs[0]
	if x.argv0 != "/opt/bin/op" || len(x.argv) != 9 {
		t.Fatalf("exec = %s %v", x.argv0, x.argv)
	}
	// プロジェクトの .env の代わりに、確かめた組だけで作った一時ファイルを渡す
	tmpEnv := strings.TrimPrefix(x.argv[3], "--env-file=")
	defer os.RemoveAll(filepath.Dir(tmpEnv))
	want := []string{"op", "run", "--no-masking", "--env-file=" + tmpEnv, "--env-file=" + global,
		"--", "/abs/dev", "nvim", "."}
	if !reflect.DeepEqual(x.argv, want) {
		t.Errorf("argv = %v", x.argv)
	}
	if tmpEnv == filepath.Join(e.proj, ".env") {
		t.Fatal("プロジェクトの .env をそのまま渡している")
	}
	b, err := os.ReadFile(tmpEnv)
	if err != nil || string(b) != "FOO=\"op://vault/item/field\"\n" {
		t.Errorf("一時ファイル = %q, %v", b, err)
	}
	if fi, _ := os.Stat(tmpEnv); fi.Mode().Perm() != 0o600 {
		t.Errorf("一時ファイルのモード = %o", fi.Mode().Perm())
	}
	if di, _ := os.Stat(filepath.Dir(tmpEnv)); di.Mode().Perm() != 0o700 {
		t.Errorf("一時フォルダのモード = %o", di.Mode().Perm())
	}
	if !slices.Contains(x.env, "SIGIL_INNER=1") {
		t.Error("SIGIL_INNER=1 がない")
	}
	if !slices.Contains(x.env, "SIGIL_OP_TMP="+filepath.Dir(tmpEnv)) {
		t.Error("一時フォルダを起動し直した側に伝えていない")
	}
	// 置き換えたあとはコンテナを作らない
	if len(e.runner.find("docker run")) != 0 || len(e.runner.find("docker image")) != 0 {
		t.Errorf("op run のあとに処理を続けた: %v", e.runner.calls)
	}
	// 確認は op run より前
	if !reflect.DeepEqual(e.order, []string{"tty", "exec"}) {
		t.Errorf("順序 = %v", e.order)
	}
}

func TestOpRunOnlyGlobalFile(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	global := e.write(filepath.Join(e.home, ".config", "sigil", "secrets.env"), "W=op://v/i/f\n")
	e.run()
	if got := e.execs[0].argv[3]; got != "--env-file="+global || e.execs[0].argv[4] != "--" {
		t.Errorf("argv = %v", e.execs[0].argv)
	}
}

func TestOpNotUsedWithoutOpRefs(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env PLAIN\n")
	e.write(".env", "PLAIN=1\n")
	e.vars["PLAIN"] = "1"
	e.tty = "y\n"
	e.run()
	if e.lastExec().argv0 == "/opt/bin/op" {
		t.Error("op:// がないのに op を使った")
	}
	e.runArgs()
}

func TestOpNotUsedWhenNotInstalled(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env X\n")
	e.write(".env", "X=op://v/i/f\n")
	e.tty = "y\n"
	e.run()
	if e.lastExec().argv0 != "/usr/local/bin/docker" {
		t.Error("op がないのに op を使おうとした")
	}
	e.runArgs()
	if !strings.Contains(e.stderr.String(), "op が見つからない") {
		t.Errorf("op がないことを知らせない: %q", e.stderr.String())
	}
}

// ---- .env で sigil 自身の環境を書き換えさせない（レビュー C1） ----

func TestEnvFileControlVarNeverReachesOp(t *testing.T) {
	for _, line := range []string{"DOCKER_HOST=tcp://attacker:2375", "PATH=/tmp/evil", "HOME=/tmp/x",
		"XDG_CONFIG_HOME=/tmp/x", "SIGIL_IMAGE=evil", "GIT_SSH_COMMAND=evil", "DYLD_INSERT_LIBRARIES=/x",
		"DOCKER_HOST=op://v/i/f"} {
		e := newEnv(t)
		e.opPath = "/opt/bin/op"
		// コメントの op:// で op run の条件を満たさせ、制御用の変数を紛れ込ませる攻撃
		e.write(".env", "# op://\n"+line+"\n")
		if code := e.run(); code != 0 {
			t.Errorf("%s: code = %d, %s", line, code, e.stderr.String())
		}
		// 制御用の名前は op run に渡らないので、op は使われず、確認も要らない
		if x := e.lastExec(); x.argv0 != "/usr/local/bin/docker" || e.ttyOpened != 0 {
			t.Errorf("%s: op を使ったか確認した: %v", line, x.argv)
		}
		if strings.Contains(e.stderr.String(), "attacker") || strings.Contains(e.stderr.String(), "evil") {
			t.Errorf("%s: 値が出た: %q", line, e.stderr.String())
		}
	}
}

func TestEnvAppConfigIsIgnored(t *testing.T) {
	// Vite や Next.js はコンテナ内で .env を直接読むので、アプリの設定が並ぶのは普通のこと
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".env", "VITE_API_URL=http://localhost:3000\nNODE_ENV=development\n")
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
	if e.ttyOpened != 0 || e.lastExec().argv0 != "/usr/local/bin/docker" {
		t.Error("アプリの設定だけの .env で、確認したか op を使った")
	}
	e.runArgs()
}

func TestEnvFileUnlistedRefIsSkipped(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=op://v/i/f\nSNEAKY=op://v/other/f\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "SNEAKY") {
		t.Errorf("列挙されていない参照を知らせない: %q", e.stderr.String())
	}
	if strings.Contains(e.stderr.String(), "op://v/other/f") {
		t.Error("使わない参照を確認の画面に出した")
	}
	tmpEnv := strings.TrimPrefix(e.execs[0].argv[3], "--env-file=")
	defer os.RemoveAll(filepath.Dir(tmpEnv))
	if b, _ := os.ReadFile(tmpEnv); string(b) != "FOO=\"op://v/i/f\"\n" {
		t.Errorf("一時ファイル = %q", b)
	}
}

func TestEnvUnsafeRefIsRejected(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=op://v/\x1b[2Jfake/f\n")
	e.tty = "y\n"
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if e.ttyOpened != 0 || len(e.execs) != 0 || strings.Contains(e.stderr.String(), "\x1b") {
		t.Errorf("制御文字を含む参照で先に進んだ: %q", e.stderr.String())
	}
}

func TestEditingAppConfigDoesNotAskAgain(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=op://v/i/f\nVITE_API_URL=http://a\n")
	e.tty = "y\n" // op は PATH にないので、承認だけが行われる
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	// アプリの設定の行を変えても、渡す参照が同じなら聞き直さない
	e.write(".env", "FOO=op://v/i/f\nVITE_API_URL=http://b\nNEW_FLAG=1\n")
	e2 := newEnvSharing(t, e)
	if code := e2.run(); code != 0 || e2.ttyOpened != 0 {
		t.Errorf("アプリの設定を変えただけで聞き直した: code=%d opened=%d %s", code, e2.ttyOpened, e2.stderr.String())
	}
}

func TestLegacyDevboxFileStops(t *testing.T) {
	// 改名前の .devbox だけがあると、黙って無視すれば渡すはずの環境変数が渡らず気づきにくいので、止めて知らせる
	e := newEnv(t)
	e.write(".devbox", "env FOO\n")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), ".devbox") || !strings.Contains(e.stderr.String(), ".sigil") {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 || e.ttyOpened != 0 {
		t.Error("作成か確認に進んだ")
	}

	// Jetify Devbox などが作る .devbox/ フォルダは、sigil の古い設定ではないので無視する
	e = newEnv(t)
	mustMkdir(t, filepath.Join(e.proj, ".devbox"))
	if code := e.run(); code != 0 {
		t.Fatalf(".devbox/ フォルダで止まった: code = %d, %s", code, e.stderr.String())
	}

	// .sigil があれば、残っている .devbox は気にしない
	e = newEnv(t)
	e.write(".devbox", "env FOO\n")
	e.write(".sigil", "port 5173\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
}

func TestLegacyGlobalConfigStops(t *testing.T) {
	// 改名前の全体設定だけがあると、WakaTime のキーなどが黙って渡らなくなるので、止めて知らせる
	e := newEnv(t)
	e.write(filepath.Join(e.home, ".config", "devbox", "devbox"), "env WAKATIME_API_KEY\n")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), filepath.Join(e.home, ".config", "devbox")) ||
		!strings.Contains(e.stderr.String(), filepath.Join(e.home, ".config", "sigil")) {
		t.Errorf("stderr = %q", e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 {
		t.Error("作成に進んだ")
	}

	// 新しい全体設定があれば、古いものが残っていても気にしない
	e = newEnv(t)
	e.write(filepath.Join(e.home, ".config", "devbox", "devbox"), "env OLD\n")
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), "env NEW\n")
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
}

func TestLegacyEnvVarsWarn(t *testing.T) {
	// 改名前の環境変数は読まないので、設定されていれば注意を出す（止めはしない）
	e := newEnv(t)
	e.vars["DEVBOX_IMAGE"] = "devbox:custom"
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
	if !strings.Contains(e.stderr.String(), "DEVBOX_IMAGE") || !strings.Contains(e.stderr.String(), "SIGIL_IMAGE") {
		t.Errorf("注意が出ない: %q", e.stderr.String())
	}

	// 新しい名前も設定されていれば、注意は出さない
	e = newEnv(t)
	e.vars["DEVBOX_CONTEXT"] = "/old"
	e.vars["SIGIL_CONTEXT"] = "/new"
	e.run("build")
	if strings.Contains(e.stderr.String(), "DEVBOX_CONTEXT") {
		t.Errorf("新しい名前があるのに注意した: %q", e.stderr.String())
	}
}

func TestInnerRemovesOpTempDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "sigil-op-")
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	e.vars["SIGIL_INNER"] = "1"
	e.vars["SIGIL_OP_TMP"] = dir
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		os.RemoveAll(dir)
		t.Error("一時フォルダを消していない")
	}

	// sigil-op- の一時フォルダ以外は、指定されても消さない
	keep := t.TempDir()
	e = newEnv(t)
	e.vars["SIGIL_INNER"] = "1"
	e.vars["SIGIL_OP_TMP"] = keep
	e.run()
	if _, err := os.Stat(keep); err != nil {
		t.Error("関係のないフォルダを消した")
	}
}

func TestGlobalSecretsControlVarIsRejected(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(filepath.Join(e.home, ".config", "sigil", "secrets.env"), "W=op://v/i/f\nDOCKER_HOST=tcp://x\n")
	if code := e.run(); code != 1 || len(e.execs) != 0 {
		t.Errorf("全体の secrets.env の制御用の変数を拒否しない: code=%d", code)
	}
}

// ---- .env も承認の対象（レビュー I3） ----

func TestEnvFileAloneRequiresApprovalWithRefs(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), "env GITHUB_TOKEN\n")
	e.write(".env", "GITHUB_TOKEN=op://Private/AWS root/password\n")
	e.tty = "n\n"
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if e.ttyOpened != 1 {
		t.Error(".env だけのときに確認しない")
	}
	if !strings.Contains(e.stderr.String(), "op://Private/AWS root/password") {
		t.Errorf("参照先が表示されない: %q", e.stderr.String())
	}
	if len(e.execs) != 0 {
		t.Error("中止したのに op を起動した")
	}
}

func TestChangingEnvRefAsksAgain(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=op://v/i/f\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	e.write(".env", "FOO=op://v/OTHER/f\n")
	e2 := newEnvSharing(t, e)
	e2.opPath = "/opt/bin/op"
	e2.tty = "n\n"
	if code := e2.run(); code != 1 || e2.ttyOpened != 1 {
		t.Errorf(".env の参照先が変わったのに聞かない: code=%d opened=%d", code, e2.ttyOpened)
	}
}

// ---- シンボリックリンクの設定（レビュー I4） ----

func TestSymlinkedSigilFileIsRejected(t *testing.T) {
	e := newEnv(t)
	secret := e.write(filepath.Join(e.home, ".netrc"), "machine github.com login x password hunter2\n")
	if err := os.Symlink(secret, filepath.Join(e.proj, ".sigil")); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(e.stderr.String(), "hunter2") {
		t.Errorf("リンク先の中身が出た: %q", e.stderr.String())
	}
	if len(e.runner.find("docker run")) != 0 {
		t.Error("作成に進んだ")
	}
}

// ---- docker が使えないとき（レビュー I5） ----

func TestDockerDownIsReported(t *testing.T) {
	for _, args := range [][]string{{"stop"}, {}} {
		e := newEnv(t)
		e.runner.on("docker ps -a --filter name=", "", errors.New("Cannot connect to the Docker daemon"))
		if code := e.run(args...); code != 1 {
			t.Errorf("%v: code = %d", args, code)
		}
		if !strings.Contains(e.stderr.String(), "Docker Desktop") {
			t.Errorf("%v: stderr = %q", args, e.stderr.String())
		}
		if strings.Contains(e.stderr.String(), "ありません") || e.ttyOpened != 0 || len(e.execs) != 0 {
			t.Errorf("%v: 誤った案内か、先に進んだ: %q", args, e.stderr.String())
		}
	}
}

// ---- cmux でペインを同時に開いたとき（レビュー M3） ----

func TestConcurrentCreateUsesContainerFromOtherPane(t *testing.T) {
	e := newEnv(t)
	created := false
	e.runner.onFunc("docker ps -a --filter name=", func() (string, error) {
		if created {
			return e.name() + "\trunning\t2\n", nil
		}
		return "", nil
	})
	// 自分が作ろうとした直前に、別のペインが同じ名前で作った
	e.runner.onFunc("docker run", func() (string, error) {
		created = true
		return "", errors.New("Conflict. The container name is already in use")
	})
	if code := e.run(); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if x := e.lastExec(); x.argv0 != "/usr/local/bin/docker" || x.argv[1] != "exec" {
		t.Errorf("既存のコンテナに入らない: %v", x.argv)
	}
}

func TestCreateFailureStillReported(t *testing.T) {
	e := newEnv(t)
	e.runner.on("docker run", "", errors.New("port is already allocated"))
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "作成できませんでした") || len(e.execs) != 0 {
		t.Errorf("stderr = %q", e.stderr.String())
	}
}

// ---- 起動し直した側（SIGIL_INNER） ----

func TestInnerRequiresApprovalRecord(t *testing.T) {
	// SIGIL_INNER を外から設定されても、承認を回避できない（レビュー M2）
	e := newEnv(t)
	e.vars["SIGIL_INNER"] = "1"
	e.vars["FOO"] = "x"
	e.write(".sigil", "env FOO\n")
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if e.ttyOpened != 0 || len(e.runner.find("docker run")) != 0 {
		t.Error("inner なのに確認したか、承認なしで作成した")
	}
}

func TestInnerSkipsApprovalAndOp(t *testing.T) {
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.vars["FOO"] = "from-op"
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=op://v/i/f\n")
	// 外側で承認済み
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	e = newEnvSharing(t, e)
	e.opPath = "/opt/bin/op"
	e.vars["SIGIL_INNER"] = "1"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if e.ttyOpened != 0 {
		t.Error("SIGIL_INNER なのに確認した")
	}
	if e.lastExec().argv0 != "/usr/local/bin/docker" {
		t.Error("SIGIL_INNER なのに op を使った")
	}
	if !slices.Contains(e.runArgs(), "FOO") {
		t.Error("FOO を渡していない")
	}
}

func TestExistingContainerSkipsConfigAndApproval(t *testing.T) {
	e := newEnv(t)
	e.container("running", "2")
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=op://v/i/f\n")
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if e.ttyOpened != 0 || e.lastExec().argv0 != "/usr/local/bin/docker" {
		t.Error("既存のコンテナなのに確認または op を使った")
	}
}

// ---- キーチェーンの参照も承認の対象（issue #25） ----

// globalConfig は全体設定を書く。
func (e *testEnv) globalConfig(content string) {
	e.write(filepath.Join(e.home, ".config", "sigil", "config"), content)
}

func TestApproveKeychainOnlyEnvAsks(t *testing.T) {
	// .sigil がなく、.env にキーチェーンの参照しかなくても、必ず確認する
	e := newEnv(t)
	e.globalConfig("env WAKATIME_API_KEY\n")
	e.write(".env", "WAKATIME_API_KEY=keychain://w\n")
	e.tty = "n\n"
	if code := e.run(); code != 1 {
		t.Fatalf("code = %d, %s", code, e.stderr.String())
	}
	if e.ttyOpened != 1 {
		t.Error("キーチェーンの参照だけの .env で確認しない")
	}
	if !strings.Contains(e.stderr.String(), "keychain://w") {
		t.Errorf("参照先が表示されない: %q", e.stderr.String())
	}
	if len(e.sec.calls) != 0 || len(e.runner.find("docker run")) != 0 || len(e.execs) != 0 {
		t.Errorf("中止したのに security か docker を呼んだ: sec=%v docker=%v", e.sec.calls, e.runner.calls)
	}
}

func TestApproveOpOnlyHashUnchanged(t *testing.T) {
	// 1Password の組だけのときは、改修前と同じハッシュになり、既存の承認記録がそのまま使える
	e := newEnv(t)
	sigil := "env A\n"
	e.write(".sigil", sigil)
	e.write(".env", "A=op://v/i/f\n")
	old := approval.Fingerprint([]byte(sigil), []byte("A=\"op://v/i/f\"\n"))
	store := approval.Store{Dir: filepath.Join(e.home, ".local", "state", "sigil", "approved")}
	if err := store.Record(e.key(), old); err != nil {
		t.Fatal(err)
	}
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	if e.ttyOpened != 0 {
		t.Error("改修前の承認記録があるのに聞いた")
	}
}

func TestApproveKeychainRefChangeAsksAgain(t *testing.T) {
	e := newEnv(t)
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=keychain://a\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	e.write(".env", "FOO=keychain://b\n")
	e2 := newEnvSharing(t, e)
	e2.tty = "n\n"
	if code := e2.run(); code != 1 || e2.ttyOpened != 1 {
		t.Errorf("キーチェーンの参照先が変わったのに聞かない: code=%d opened=%d", code, e2.ttyOpened)
	}
}

func TestApproveIgnoresGlobalSecretsChange(t *testing.T) {
	// 全体の secrets.env はホストのファイル。編集しても、プロジェクトの承認は変わらない
	e := newEnv(t)
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=keychain://a\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	// 同じ名前を全体で上書きしても（優先順位が変わっても）、聞き直さない
	e.write(filepath.Join(e.home, ".config", "sigil", "secrets.env"), "FOO=raw\nOTHER=keychain://x\n")
	e2 := newEnvSharing(t, e)
	if code := e2.run(); code != 0 || e2.ttyOpened != 0 {
		t.Errorf("secrets.env を変えただけで聞き直した: code=%d opened=%d %s", code, e2.ttyOpened, e2.stderr.String())
	}
}

func TestOpEnvExcludesOverridden(t *testing.T) {
	// 全体の secrets.env に負けたプロジェクトの組は、op run の env ファイルに入れない
	e := newEnv(t)
	e.opPath = "/opt/bin/op"
	e.write(".sigil", "env A\nenv B\n")
	e.write(".env", "A=op://p/i/f\nB=op://p/j/f\n")
	e.write(filepath.Join(e.home, ".config", "sigil", "secrets.env"), "A=keychain://w\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	x := e.lastExec()
	if x.argv0 != "/opt/bin/op" {
		t.Fatalf("op run に置き換えていない: %v", x.argv)
	}
	tmpEnv := strings.TrimPrefix(x.argv[3], "--env-file=")
	b, err := os.ReadFile(tmpEnv)
	if err != nil || string(b) != "B=\"op://p/j/f\"\n" {
		t.Errorf("一時ファイル = %q, %v", b, err)
	}
	// 承認の画面には、負けた組も、使わないことが分かるように出す
	out := e.stderr.String()
	if !strings.Contains(out, "op://p/i/f") || !strings.Contains(out, "全体の設定が優先されるため使わない") {
		t.Errorf("負けた組の表示がない:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "op://p/j/f") && strings.Contains(l, "全体の設定が優先") {
			t.Errorf("勝った組に注記がある: %q", l)
		}
	}
}

func TestEnvKeychainRefSelectionWarnings(t *testing.T) {
	// キーチェーンの参照も、op の参照と同じ規則・同じ文で選ぶ
	e := newEnv(t)
	e.write(".sigil", "env FOO\n")
	e.write(".env", "FOO=keychain://a\nSNEAKY=keychain://b\nPATH=keychain://c\n")
	e.tty = "y\n"
	if code := e.run(); code != 0 {
		t.Fatal(e.stderr.String())
	}
	out := e.stderr.String()
	for _, want := range []string{
		"注意: .env の SNEAKY は .sigil にも全体設定にも列挙されていないので、禁書庫から読み込みません",
		"注意: .env の PATH は渡せない名前なので、禁書庫から読み込みません",
		"keychain://a",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%q がない:\n%s", want, out)
		}
	}
	if strings.Contains(out, "keychain://b") || strings.Contains(out, "keychain://c") {
		t.Errorf("使わない参照を確認の画面に出した:\n%s", out)
	}
}
