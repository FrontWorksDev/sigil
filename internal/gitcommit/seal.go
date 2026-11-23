package gitcommit

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrMoved は、確認のあいだにブランチが別の場所へ進んだとき。
var ErrMoved = errors.New("別の場所で刻印されたようです。もう一度実行してください")

// Seal は、確定した tree をコミットにして、ブランチを進める。
// ホストの git のフックは動かさない（SafeConfig で無効）。コンテナが書いたフックを
// ホストで実行させないため。コミットする tree は Snapshot.Tree そのもので、
// 作業ツリーや本物の index は読まない。
//
// message は末尾に改行を含まない（正規化済み）前提で、Seal が改行を 1 つ足す。
// stderr は commit-tree の標準エラーの流し先（端末。テストでは差し替える）。
//
// 最後の本物の index の reset が、update-ref の成功のあとに失敗したときは、
// コミットの id とエラーの両方を返す。コミットは既にできているので、呼び出し側は
// やり直さず、「コミットはできたが index の整理に失敗した」と伝えること。
//
// 残る競合: Snapshot のあとにブランチを切り替えられると、r.Branch は古いブランチのままなので、
// 古いブランチに刻印される（HEAD の指す先は見ない）。update-ref は r.Branch と旧値だけを
// 見るため、別のブランチを壊すことはないが、切り替えた先には刻印されない。
func Seal(g Git, r Repo, s Snapshot, message string, pathspec []string, stderr io.Writer) (string, error) {
	args := []string{"commit-tree", s.Tree}
	if r.Head != "" {
		args = append(args, "-p", r.Head)
	}
	args = append(args, "-F", "-")
	// 署名の問い合わせは端末に出す必要があるので、標準エラーだけつなぐ。
	out, err := g.Interactive(strings.NewReader(message+"\n"), stderr, args...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(out)

	// 旧値を渡して、確認のあいだにブランチが動いていたら失敗させる（compare-and-swap）。
	old := r.Head
	if old == "" {
		// 「まだ存在しない」ことを表す。SHA-1 / SHA-256 の違いは新しいコミットの長さでわかる。
		old = strings.Repeat("0", len(commit))
	}
	subject, _, _ := strings.Cut(message, "\n")
	if _, err := g.Out(nil, "update-ref", "-m", "commit (sigil): "+subject, r.Branch, commit, old); err != nil {
		// 本物の index には触らずに戻る。git の詳細（Escape 済み）は残す。
		return "", fmt.Errorf("%w（%s）", ErrMoved, err.Error())
	}

	// 刻印した分だけ本物の index を HEAD にそろえる。付けない reset は ORIG_HEAD を書くので、
	// 必ずパスを付ける。pathspec の外の add 済みの変更はそのまま残る。
	reset := append([]string{"reset", "-q", "--"}, effectivePathspec(pathspec)...)
	if _, err := g.Out(nil, reset...); err != nil {
		return commit, err
	}
	return commit, nil
}
