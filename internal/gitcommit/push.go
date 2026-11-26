package gitcommit

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/FrontWorksDev/sigil/internal/ui"
)

// Upstream は、現在のブランチの push 先。
type Upstream struct {
	Remote string
	// URL は生の値。表示するときは呼び出し側が Escape する。
	URL      string
	MergeRef string
	// RemoteSha は手元の追跡 ref の指す commit。なければ 0 の並び。
	RemoteSha string
	Ahead     int
}

// configValue は設定値を返す。キーがなければ空文字（--default "" で、
// 「キーがない」と「git の失敗」を区別する。後者はエラーになる）。
func configValue(g Git, key string) (string, error) {
	out, err := g.Out(nil, "config", "--default", "", "--get", key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// FindUpstream は、ブランチの upstream を調べる。なければ false。
// remote が "." のとき（ローカルブランチを追う設定）は、push 先がないものとして扱う。
func FindUpstream(g Git, r Repo) (Upstream, bool, error) {
	name := strings.TrimPrefix(r.Branch, "refs/heads/")
	remote, err := configValue(g, "branch."+name+".remote")
	if err != nil {
		return Upstream{}, false, err
	}
	merge, err := configValue(g, "branch."+name+".merge")
	if err != nil {
		return Upstream{}, false, err
	}
	if remote == "" || merge == "" || remote == "." {
		return Upstream{}, false, nil
	}
	u := Upstream{Remote: remote, MergeRef: merge}

	url, err := g.Out(nil, "remote", "get-url", "--push", remote)
	if err != nil {
		return Upstream{}, false, err
	}
	u.URL = strings.TrimSpace(url)

	n := 40
	if len(r.Head) > 0 {
		n = len(r.Head)
	}
	u.RemoteSha = strings.Repeat("0", n)
	// 数えるのは r.Head（呼び出し側が刻印したコミットを入れる）まで。HEAD はそのあとに
	// 動いているかもしれず、push するのも r.Head なので、画面の件数をそろえる。
	tracking := "refs/remotes/" + remote + "/" + strings.TrimPrefix(merge, "refs/heads/")
	rev := []string{"rev-list", "--count", r.Head}
	if sha, err := g.Out(nil, "rev-parse", "-q", "--verify", tracking+"^{commit}"); err == nil {
		u.RemoteSha = strings.TrimSpace(sha)
		rev = []string{"rev-list", "--count", u.RemoteSha + ".." + r.Head}
	}
	if r.Head != "" {
		cnt, err := g.Out(nil, rev...)
		if err != nil {
			return Upstream{}, false, err
		}
		u.Ahead, err = strconv.Atoi(strings.TrimSpace(cnt))
		if err != nil {
			return Upstream{}, false, fmt.Errorf("rev-list の結果を読めませんでした")
		}
	}
	return u, true, nil
}

// PrePushInput は、pre-push フックの標準入力。
func (u Upstream) PrePushInput(r Repo, head string) string {
	return r.Branch + " " + head + " " + u.MergeRef + " " + u.RemoteSha + "\n"
}

// Push は upstream に push する。フックは --no-verify と SafeConfig の両方で止める
// （フックは、コンテナの中で先に実行している）。
// 出力は利用者が設定したリモートから来るもので、コンテナが作るものではないので、
// Out を通さずそのまま流す。SSH のパスフレーズの問い合わせは /dev/tty に出る。
// 標準エラーは念のため Escape する。Stdin はつながない。
//
// 送るのは commit（刻印したコミット）そのもの。HEAD を送ると、確認のあとに HEAD が動いたとき、
// 利用者が見ていないコミットまで送ってしまう。
// --recurse-submodules=no は、利用者の全体設定（push.recurseSubmodules）があっても、
// サブモジュールの中で git を動かさない（入れ子の設定はコンテナが書き換えられる）ため。
func Push(g Git, u Upstream, commit string, stdout, stderr io.Writer) error {
	args := append([]string{"-C", g.Root}, SafeConfig...)
	args = append(args, "push", "--no-verify", "--recurse-submodules=no", u.Remote, commit+":"+u.MergeRef)
	err := g.R.Run("git", args, RunOpts{Env: g.Env, Stdout: stdout, Stderr: ui.NewEscapeWriter(stderr)})
	if err != nil {
		return fmt.Errorf("git push に失敗しました: %s", ui.EscapeLine(err.Error()))
	}
	return nil
}

// RedactURL は、URL の形のリモート（scheme://userinfo@host...）から userinfo を丸ごと取り除く。
// remote get-url --push は利用者の全体設定の insteadOf を反映するので、
// https://user:TOKEN@host/... のような認証情報が入りうる。それを画面に出したり、
// コンテナのフックに渡したり（docker exec の引数として ps にも見える）しないため。
// scp の形（user@host:path）の user は秘密ではないので、そのまま残す。
// 読めないものは変えずに返す（panic しない）。
func RedactURL(u string) string {
	i := strings.Index(u, "://")
	if i <= 0 || !isScheme(u[:i]) {
		return u
	}
	rest := u[i+3:]
	auth := rest
	if end := strings.IndexAny(rest, "/?#"); end >= 0 {
		auth = rest[:end]
	}
	at := strings.LastIndex(auth, "@")
	if at < 0 {
		return u
	}
	return u[:i+3] + rest[at+1:]
}

// isScheme は、RFC 3986 の scheme（英字で始まり、英数字と + - . が続く）かを返す。
func isScheme(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return s != ""
}
