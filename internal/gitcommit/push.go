package gitcommit

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"sigil/internal/ui"
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
	tracking := "refs/remotes/" + remote + "/" + strings.TrimPrefix(merge, "refs/heads/")
	rev := []string{"rev-list", "--count", "HEAD"}
	if sha, err := g.Out(nil, "rev-parse", "-q", "--verify", tracking+"^{commit}"); err == nil {
		u.RemoteSha = strings.TrimSpace(sha)
		rev = []string{"rev-list", "--count", u.RemoteSha + "..HEAD"}
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
func Push(g Git, u Upstream, stdout, stderr io.Writer) error {
	args := append([]string{"-C", g.Root}, SafeConfig...)
	args = append(args, "push", "--no-verify", u.Remote, "HEAD:"+u.MergeRef)
	err := g.R.Run("git", args, RunOpts{Env: g.Env, Stdout: stdout, Stderr: ui.NewEscapeWriter(stderr)})
	if err != nil {
		return fmt.Errorf("git push に失敗しました: %s", ui.EscapeLine(err.Error()))
	}
	return nil
}
