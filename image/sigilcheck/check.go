package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// kind は、点検の 1 行の種類。
type kind int

const (
	kindOK      kind = iota // ✓（数える）
	kindNG                  // ✗（数える。1 つでもあれば終了コード 1）
	kindWarn                // !（注意。隔離は保たれているが見直したほうがよいもの。失敗には数えない）
	kindNote                // 情報の行（環境変数の名前など）。数えない
	kindDimNote             // 薄く出す情報の行。数えない
	kindTool                // ツールの版。数えない
)

type item struct {
	kind kind
	text string
	tool string // kindTool のとき、コマンドの名前
	warn bool   // kindTool のとき、注意の色で出す（見つからない・実行しない）
}

type section struct {
	title string // 世界観の言葉
	sub   string // 普通の言葉での意味
	items []item
}

type report []section

func (r report) counts() (pass, fail, warns int) {
	for _, sec := range r {
		for _, it := range sec.items {
			switch it.kind {
			case kindOK:
				pass++
			case kindNG:
				fail++
			case kindWarn:
				warns++
			}
		}
	}
	return
}

func ok(s string) item   { return item{kind: kindOK, text: s} }
func ng(s string) item   { return item{kind: kindNG, text: s} }
func warn(s string) item { return item{kind: kindWarn, text: s} }

// sys は、外の世界とのやりとり。本物は main.go の realSys で組み立て、テストでは偽物を差し込む。
// 判定（check）は sys を通してしか外に触れない。
type sys struct {
	lookupEnv func(name string) (string, bool)
	environ   func() []string
	lstat     func(path string) (fs.FileMode, error) // シンボリックリンクをたどらない
	isDir     func(path string) bool                 // シンボリックリンクをたどる
	tryWrite  func(dir string) error                 // dir に一時ファイルを作って消す
	// tryOpenWrite は、ファイルを書き込み用に開いて、何も書かずに閉じる（作らない・切り詰めない）
	tryOpenWrite func(path string) error
	readFile     func(path string) ([]byte, error)
	lookPath     func(name string) (string, error) // コンテナの PATH で探す（実行はしない）
	resolve      func(path string) string          // シンボリックリンクを解決する（失敗ならそのまま）
	user         func() (uid int, name string)
	rateLimit    func(token string) (status int, header http.Header, err error)
	version      func(path, name string) string // path のコマンドの版（最初の 1 行）
	now          func() time.Time
}

func (s *sys) env(name string) string {
	v, _ := s.lookupEnv(name)
	return v
}

// ext は、外から来る文字列（パス、名前など）を表示に入れる前に消毒する。
func ext(s string) string { return clean(s, 200) }

func check(s *sys) report {
	r := report{secrets(s), barrier(s), privileges(s), envSection(s)}
	if sec, ok := githubSection(s); ok {
		r = append(r, sec)
	}
	return append(r, tools(s))
}

// notExist は、「無い」と分かるエラーか。途中がファイルなら（ENOTDIR）その先も無い。
func notExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

func secrets(s *sys) section {
	home := s.env("HOME")
	sec := section{title: "禁忌の扉", sub: "ホストの秘密情報が見えないこと"}
	// 「見えない」とするのは、無いと分かったときだけ（fail-closed）。
	// Lstat を使うので、リンク先のないシンボリックリンクも「見えている」になる（bash の -e は見逃していた）。
	absent := func(p, label string) {
		_, err := s.lstat(p)
		switch {
		case err == nil:
			sec.items = append(sec.items, ng(fmt.Sprintf("%s が見えている (%s)", label, ext(p))))
		case notExist(err):
			sec.items = append(sec.items, ok(label+" は見えない"))
		default:
			sec.items = append(sec.items, ng(fmt.Sprintf("%s を確かめられない (%s)", label, ext(p))))
		}
	}
	absent(home+"/.ssh", "SSH 鍵 (~/.ssh)")
	absent(home+"/.aws", "AWS 設定 (~/.aws)")
	absent(home+"/.config/op", "1Password CLI 設定")
	absent(home+"/.gitconfig", "ホストの .gitconfig")
	absent(home+"/.npmrc", "ホストの .npmrc")
	absent(home+"/.docker", "Docker の認証情報")
	absent("/var/run/docker.sock", "Docker ソケット")
	if s.env("SSH_AUTH_SOCK") != "" {
		sec.items = append(sec.items, ng("SSH エージェントが転送されている"))
	} else {
		sec.items = append(sec.items, ok("SSH エージェントは転送されていない"))
	}
	if _, err := s.lookPath("ssh"); err == nil {
		sec.items = append(sec.items, ng("ssh コマンドが入っている"))
	} else {
		sec.items = append(sec.items, ok("ssh コマンドは入っていない"))
	}
	return sec
}

// readOnly は、書き込みの試しが「読み取り専用だから失敗した」と分かるエラーか。
// それ以外のエラー（容量不足、同じ名前が先にある、など）では、書き込めないとは言えない。
func readOnly(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}

func barrier(s *sys) section {
	sec := section{title: "結界", sub: "ホストで実行されるファイルを書き換えられないこと"}
	proj := s.env("SIGIL_PROJECT_DIR")
	if proj == "" {
		proj = "/workspace"
	}
	git := proj + "/.git"
	mode, err := s.lstat(git)
	switch {
	case notExist(err):
		sec.items = append(sec.items, ng(".git がない（ホスト側で git init / clone してから sigil を起動してください）"))
	case err != nil:
		sec.items = append(sec.items, ng(".git を確かめられない"))
	case mode.IsRegular():
		// git worktree / submodule では .git が「gitdir: <場所>」を書いたファイルで、読み取り専用で
		// マウントされる。書き換えられると、ホストの git を別の gitdir（偽の hooks）に向けられるので、
		// 書き込み用に開けるかを試す（中身は変えない）。
		sec.items = append(sec.items, writeItem(s.tryOpenWrite(git),
			".git（worktree / submodule のファイル）は読み取り専用", ".git に書き込める（hooks を仕込まれる恐れ）", ".git に書き込めるか確かめられない"))
	case !mode.IsDir():
		// シンボリックリンクなどでは、書き込みの試しが正しく効かない
		sec.items = append(sec.items, ng(".git がフォルダでもファイルでもない（点検できない）"))
	default:
		sec.items = append(sec.items, writeItem(s.tryWrite(git),
			".git は読み取り専用", ".git に書き込める（hooks を仕込まれる恐れ）", ".git に書き込めるか確かめられない"))
	}
	if nvim := s.env("HOME") + "/.config/nvim"; s.isDir(nvim) {
		sec.items = append(sec.items, writeItem(s.tryWrite(nvim),
			"Neovim 設定は読み取り専用", "Neovim 設定に書き込める", "Neovim 設定に書き込めるか確かめられない"))
	}
	return sec
}

func writeItem(err error, ro, writable, unknown string) item {
	switch {
	case err == nil:
		return ng(writable)
	case readOnly(err):
		return ok(ro)
	default:
		return ng(unknown)
	}
}

// statusField は、/proc/self/status の「名前:<空白>値」の行から値を返す。
func statusField(status, name string) (string, bool) {
	for _, line := range strings.Split(status, "\n") {
		rest, found := strings.CutPrefix(line, name+":")
		if found {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

func privileges(s *sys) section {
	sec := section{title: "権能の剥奪", sub: "権限"}
	if uid, name := s.user(); uid == 0 {
		sec.items = append(sec.items, ng("root で動いている"))
	} else {
		sec.items = append(sec.items, ok(fmt.Sprintf("一般ユーザー (%s) で動いている", ext(name))))
	}
	// 読めない・行がないときは「確かめられない」として ✗ にする（fail-closed）
	b, err := s.readFile("/proc/self/status")
	status := ""
	if err == nil {
		status = string(b)
	}
	capeff, found := statusField(status, "CapEff")
	v, perr := strconv.ParseUint(capeff, 16, 64)
	switch {
	case !found || perr != nil:
		sec.items = append(sec.items, ng("capabilities を確かめられない"))
	case v == 0:
		sec.items = append(sec.items, ok("Linux capabilities はすべて外れている"))
	default:
		sec.items = append(sec.items, ng(fmt.Sprintf("capabilities が残っている (%s)", ext(capeff))))
	}
	switch nnp, found := statusField(status, "NoNewPrivs"); {
	case !found:
		sec.items = append(sec.items, ng("no-new-privileges を確かめられない"))
	case nnp == "1":
		sec.items = append(sec.items, ok("no-new-privileges が有効"))
	default:
		sec.items = append(sec.items, ng("no-new-privileges が無効"))
	}
	return sec
}

// 「持ち込むもの」に出さない環境変数。ホストから渡されたものだけを表示するため。
var (
	// imageEnv は、image/Dockerfile の ENV で設定する名前。ENV を足したらここにも足す
	// （source_test.go が Dockerfile と照らし合わせる）。
	imageEnv = []string{
		"LANG", "LC_ALL", "DEBIAN_FRONTEND",
		"PATH", "GOPATH", "CARGO_HOME", "RUSTUP_HOME", "NPM_CONFIG_PREFIX", "npm_config_cache",
		"CLAUDE_CONFIG_DIR", "DISABLE_AUTOUPDATER", "GH_NO_UPDATE_NOTIFIER", "EDITOR", "SIGIL",
	}
	// baseImageEnv は、土台のイメージ（php:cli-trixie）が設定する名前。
	baseImageEnv = []string{
		"PHPIZE_DEPS", "PHP_INI_DIR", "PHP_CFLAGS", "PHP_CPPFLAGS", "PHP_LDFLAGS",
		"GPG_KEYS", "PHP_VERSION", "PHP_URL", "PHP_ASC_URL", "PHP_SHA256",
	}
	// runtimeEnv は、docker・sigil・シェルが設定する名前。
	runtimeEnv = []string{
		"HOME", "HOSTNAME", "TERM", "COLORTERM", "PWD", "OLDPWD", "SHLVL", "_", "USER", "LOGNAME", "SHELL",
		"SIGIL_PROJECT_DIR", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		"PS1", "LS_COLORS", "MAIL", "NO_COLOR", "COLUMNS", "LINES",
	}
	allowedEnvPattern = regexp.MustCompile(`^(BASH_.*|.*_PROXY|.*_proxy)$`)
	allowedEnvSet     = func() map[string]bool {
		m := map[string]bool{}
		for _, l := range [][]string{imageEnv, baseImageEnv, runtimeEnv} {
			for _, n := range l {
				m[n] = true
			}
		}
		return m
	}()
)

func envAllowed(name string) bool {
	return allowedEnvSet[name] || allowedEnvPattern.MatchString(name)
}

func envSection(s *sys) section {
	sec := section{title: "持ち込むもの", sub: "渡されている環境変数（名前のみ）"}
	var names []string
	for _, kv := range s.environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name != "" && !envAllowed(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		sec.items = append(sec.items, item{kind: kindNote, text: ext(n)})
	}
	if len(names) == 0 {
		sec.items = append(sec.items, item{kind: kindNote, text: "（なし）"})
	}
	return sec
}

var toolNames = []string{
	"nvim", "node", "npm", "go", "cargo", "php", "composer", "python3", "uv", "claude", "gh",
	"wakatime-cli", "tree-sitter", "rg", "fd", "lua5.1", "luarocks", "lazygit", "bat", "delta",
}

// trustedTool は、コマンドの場所がイメージの中か。共有ボリューム（~/go/bin、~/.cargo/bin など）には
// ほかのプロジェクトのコードが置いたものがありうるので、実行しない（設計書 11 章、issue #7）。
// 書き込めるのが root だけの場所と、コンテナごとに作り直される場所だけを信頼する。
func trustedTool(path, home string) bool {
	prefixes := []string{"/usr/", "/bin/", "/sbin/", "/opt/"}
	if strings.HasPrefix(home, "/") && home != "/" {
		// ~/.local/share/claude は、Claude Code の公式のインストーラーが本体を置く場所（~/.local/bin/claude のリンク先）
		prefixes = append(prefixes, home+"/.local/bin/", home+"/.npm-global/bin/", home+"/.local/share/claude/")
	}
	if !filepath.IsAbs(path) {
		return false
	}
	p := filepath.Clean(path)
	for _, pre := range prefixes {
		if strings.HasPrefix(p, filepath.Clean(pre)+"/") {
			return true
		}
	}
	return false
}

func tools(s *sys) section {
	sec := section{title: "ツール", sub: "コンテナ内のコマンドと版"}
	home := s.env("HOME")
	for _, c := range toolNames {
		it := item{kind: kindTool, tool: c}
		path, err := s.lookPath(c)
		if err != nil {
			it.text, it.warn = "見つからない", true
			sec.items = append(sec.items, it)
			continue
		}
		// 確かめた解決後のパスをそのまま実行する。元のパスを実行すると、確かめたあとに途中のリンクを
		// 差し替えられたとき、別のものが動く
		resolved := s.resolve(path)
		if !trustedTool(path, home) || !trustedTool(resolved, home) {
			it.text, it.warn = ext(path)+"（イメージの外なので実行しない）", true
		} else {
			it.text = clean(s.version(resolved, c), 200)
		}
		sec.items = append(sec.items, it)
	}
	return sec
}
