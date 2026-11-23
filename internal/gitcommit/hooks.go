package gitcommit

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"sigil/internal/ui"
)

// フックの名前。
const (
	PreCommit        = "pre-commit"
	PrepareCommitMsg = "prepare-commit-msg"
	CommitMsg        = "commit-msg"
	PrePush          = "pre-push"
)

var hookNames = []string{PreCommit, PrepareCommitMsg, CommitMsg, PrePush}

// Hooks は、リポジトリにあるフック。
type Hooks struct {
	Dir     string
	Present map[string]bool
}

// Any は、names のどれかがあるかを返す。
func (h Hooks) Any(names ...string) bool {
	for _, n := range names {
		if h.Present[n] {
			return true
		}
	}
	return false
}

// FindHooks は、実行できるフックを探す。
// フックはプロジェクトのコードなのでホストでは動かさず、コンテナの中で動かす。
// コンテナにはプロジェクトのフォルダしか見えないので、その外にあるフックは動かせない。
// 動かせないものを黙って飛ばすと、検査が抜けたままコミットされるため、拒否する。
func FindHooks(r Repo) (Hooks, error) {
	h := Hooks{Dir: r.HooksDir, Present: map[string]bool{}}
	for _, n := range hookNames {
		fi, err := os.Stat(filepath.Join(r.HooksDir, n))
		if err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			h.Present[n] = true
		}
	}
	if len(h.Present) > 0 && !lexWithin(r.Root, r.HooksDir) {
		// フックの場所は .git/config（利用者の設定）から来るが、制御文字で画面を操作されないよう Escape する。
		return h, refuse(fmt.Sprintf("フックがプロジェクトの外（%s）にあり、領域から実行できません。手動でコミットしてください", ui.EscapeLine(r.HooksDir)))
	}
	return h, nil
}

// hookScript は、コンテナの中でフックを動かす固定のシェルスクリプト。
// フックのパスや pathspec は位置引数で受け取り、この文字列には埋め込まない
// （ファイル名などにシェルの記号があっても、コマンドとして解釈されないようにするため）。
// コンテナでは .git が読み取り専用なので、index とオブジェクトの書き込み先を一時フォルダに向ける。
// 一時 index は、コミットがあるときだけ本物の index を写す。最初のコミットでは空から始める
// （ホスト側のスナップショットと同じ）。
// 引数なしの git reset は ORIG_HEAD と reflog を .git に書いて失敗するので、パスを付けた形にする。
// 知らない mode は失敗で終わらせる（検査を黙って飛ばさない）。
// common は CDPATH に左右されない git の絶対パス指定で求める。
// フックの標準出力は標準エラーへ回す。msg モードでは標準出力がメッセージそのものになるため。
// pre-commit の pathspec は AddPathspec（銘のファイルを除く指定を含まない）。add のあとで、
// ホストの dropMessageArgs と同じく、銘のファイルを一時 index から戻す。除く指定を add に
// 渡すと、銘のファイルが .gitignore 済みのとき git add が失敗するため。
// 埋め込む messagePathspec は sigil の定数で、利用者やコンテナの入力ではない。
const hookScript = `set -eu
mode="$1"; hook="$2"; shift 2
gitdir="$(git rev-parse --absolute-git-dir)"
common="$(git rev-parse --path-format=absolute --git-common-dir)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/objects"
export GIT_INDEX_FILE="$tmp/index" GIT_OBJECT_DIRECTORY="$tmp/objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$common/objects"
has_head=
if git rev-parse -q --verify HEAD >/dev/null; then
  has_head=1
  cp "$gitdir/index" "$tmp/index" 2>/dev/null || true
  git reset -q HEAD -- :/
fi
case "$mode" in
pre-commit)
  git add -A -- "$@"
  if [ -n "$has_head" ]; then
    git reset -q HEAD -- '` + messagePathspec + `'
  else
    git rm --cached -q --ignore-unmatch -- '` + messagePathspec + `'
  fi
  "$hook" </dev/null >&2 ;;
msg)
  cat >"$tmp/MSG"
  "$hook" "$tmp/MSG" "$@" </dev/null >&2
  cat "$tmp/MSG" ;;
pre-push)
  "$hook" "$@" >&2 ;;
*)
  echo "sigil: unknown mode" >&2; exit 2 ;;
esac
`

// HookExecArgs は、フックをコンテナで動かす docker exec の引数を作る。
// フックのパスは hostRoot を containerRoot に読み替える。hooksDir が hostRoot の外なら、
// FindHooks が先に拒否しているはずなので、プログラムの誤りとしてエラーを返す。
func HookExecArgs(container, containerRoot, hostRoot, hooksDir, name string, extra []string) ([]string, error) {
	rel, err := filepath.Rel(hostRoot, filepath.Join(hooksDir, name))
	if err != nil || !lexWithin(hostRoot, hooksDir) {
		return nil, fmt.Errorf("フックの場所 %s がプロジェクトの外にあります", hooksDir)
	}
	mode := "msg"
	switch name {
	case PreCommit:
		mode = "pre-commit"
	case PrePush:
		mode = "pre-push"
	}
	inContainer := path.Join(containerRoot, filepath.ToSlash(rel))
	args := []string{"exec", "-i", "-w", containerRoot, container, "sh", "-c", hookScript, "sh", mode, inContainer}
	return append(args, extra...), nil
}

// RunHook は docker を実行する。
func RunHook(r Runner, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return r.Run("docker", args, RunOpts{Stdin: stdin, Stdout: stdout, Stderr: stderr})
}
