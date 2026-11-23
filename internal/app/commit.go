package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"sigil/internal/gitcommit"
	"sigil/internal/project"
	"sigil/internal/ui"
)

// commitUsage は sigil commit の引数が誤っているときのエラー。
var commitUsage = errors.New("使い方: sigil commit [-- <path>...]")

// commitArgs は sigil commit の後ろの引数から pathspec を取り出す。
// 空ならすべて（nil）。-- の後ろは、端末の前の利用者が打ったものなので信じてよいが、
// git と同じく今いるフォルダ（cwd、リンクを解いたもの）からのパスとして読み、最上位からのパスに直す
// （ホストの git は -C <root> で呼ぶため）。
// コンテナが指定したファイルの一覧は使わない（.github/workflows などを紛れ込ませられないように）。
func commitArgs(args []string, root, cwd string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if args[0] != "--" {
		return nil, commitUsage
	}
	var out []string
	for _, a := range args[1:] {
		// :(top) や :! などの magic は、銘のファイルを外す指定や、今いるフォルダからという決まりを
		// すり抜けられるので受け付けない。
		if strings.HasPrefix(a, ":") {
			return nil, loref("刻むものを指定できません", "%s のような : で始まる指定（pathspec の magic）は使えません。今いるフォルダからのパスで指定してください", ui.EscapeLine(a))
		}
		p := a
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(root, filepath.Clean(p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, loref("刻むものを指定できません", "%s はプロジェクトの外を指しています。%s（パスは今いるフォルダから）", ui.EscapeLine(a), commitUsage)
		}
		rel = path.Clean(filepath.ToSlash(rel))
		// 銘のファイルは必ずコミットから外すので、名指しされても入れられない。黙って無視すると
		// 「刻むものがありません」などの紛らわしい結果になるので、はっきり断る。
		// 大文字と小文字を区別しないファイルシステム（macOS の既定）では同じファイルなので、区別せずに比べる。
		if strings.EqualFold(rel, gitcommit.MessageFile) {
			return nil, loref("刻むものを指定できません", "%s は銘のファイルで、コミットには含めません。ほかのパスを指定してください", ui.EscapeLine(a))
		}
		out = append(out, rel)
	}
	return out, nil
}

// commitCmd は sigil commit（設計書 17 章）。コンテナが用意した銘で、利用者の確認のうえ、
// ホストの git でコミットし、push するかを別に確かめる。
//
// 作業ツリーの中はすべてコンテナが書き換えられるので、
//   - ホストではフックを実行しない（コンテナで実行する）
//   - 先に tree を確定して見せ、その tree をコミットする（確認のあいだの変更を混ぜない）
//   - コンテナが書いた文字列は、画面に出す前に Escape する（ui が行う）
func commitCmd(d Deps, u *ui.UI, p project.Project, cwd string, args []string) error {
	pathspec, err := commitArgs(args, p.Root, cwd)
	if err != nil {
		return err
	}
	// 1. 前提。確認の答えを端末から聞くので、端末でなければ始めない。
	if !d.IsTerminal() {
		return loref("刻印できません", "端末から実行してください（確認の画面を出して、答えを聞くため）")
	}
	g := gitcommit.Git{R: d.Cmd, Root: p.Root}
	// 2. 拒否する条件（作業ツリーの中から読まれる設定、.git の形、進行中の操作、LFS、フックの場所）
	r, err := gitcommit.Inspect(g)
	if err != nil {
		return commitErr(err)
	}
	hooks, err := gitcommit.FindHooks(r)
	if err != nil {
		return commitErr(err)
	}
	// 3. 銘。コミットするのは、ここで整形して画面に出した内容で、ファイルは読み直さない。
	msgPath := filepath.Join(r.Root, gitcommit.MessageFile)
	msg, err := gitcommit.ReadMessage(msgPath)
	if err != nil {
		if errors.Is(err, gitcommit.ErrNoMessage) {
			return commitErr(err)
		}
		return lore("銘を読めません", err)
	}
	if _, err := g.Out(nil, "ls-files", "--error-unmatch", "--", gitcommit.MessageFile); err == nil {
		info(d, "注意: %s が git で追跡されています。コミットには含めませんが、刻印のあとに消すので、作業ツリーには追跡されたファイルの削除が残ります。git rm --cached してから .gitignore に入れてください", gitcommit.MessageFile)
	}

	h := hookRunner{d: d, p: p, r: r, hooks: hooks}
	if hooks.Any(gitcommit.PreCommit, gitcommit.PrepareCommitMsg, gitcommit.CommitMsg, gitcommit.PrePush) {
		if err := h.ready(); err != nil {
			return err
		}
	}

	// 4〜6. pre-commit → tree の確定 → prepare-commit-msg / commit-msg
	s, msg, ran, err := h.prepare(u, g, pathspec, msg)
	if err != nil {
		return err
	}
	defer s.Cleanup()

	// 7. 確認の画面。確認と push の答えは、同じ tty の同じ bufio.Reader から読む
	// （呼ぶたびに包み直すと、先読みした次の行を捨ててしまうため）。
	view, err := commitView(g, r, s, pathspec, msg, ran)
	if err != nil {
		return err
	}
	tty, err := d.OpenTTY()
	if err != nil {
		return loref("刻印できません", "確認の答えを聞けない環境です。対話できるターミナルで実行してください")
	}
	defer tty.Close()
	br := bufio.NewReader(tty)
	if err := confirmCommit(d, u, g, s, view, br); err != nil {
		return err
	}

	// 8. 刻印。署名の問い合わせ（pinentry など）が端末に出ることがあるので、描画しない。
	commit, err := gitcommit.Seal(g, r, s, msg, pathspec, d.Stderr)
	if commit == "" {
		return commitErr(err)
	}
	// commit があってエラーもあるときは、コミットはできていて、本物の index の整理だけが失敗した。
	// やり直すと二重にコミットされるので、やり直しは勧めず、先へ進む。
	indexErr := err

	// 9. 銘のファイルを消す（次のコミットで、同じ銘を使い回さないように）
	if err := os.Remove(msgPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		info(d, "注意: %s を消せませんでした（%s）。次のコミットの前に書き直してください", gitcommit.MessageFile, ui.EscapeLine(err.Error()))
	}
	subject, _, _ := strings.Cut(msg, "\n")
	u.Sealed("刻印しました",
		"ブランチ  "+strings.TrimPrefix(r.Branch, "refs/heads/"),
		"コミット  "+shortHash(g, commit),
		"銘        "+subject)
	if indexErr != nil {
		info(d, "注意: コミットはできましたが、index を HEAD にそろえられませんでした。git status で確かめてください（%s）", indexErr)
	}

	// 10. push。件数・pre-push の入力・push するものは、すべて刻印したコミット（r.Head）にそろえる。
	// HEAD はこのあとも動きうるので読まない（利用者が見ていないコミットを送らないように）。
	r.Head = commit
	return pushFlow(d, u, g, r, h, br)
}

// confirmCommit は確認の画面を出して答えを聞く。d なら全差分をページャーで見せて、画面から聞き直す。
// y / Y / yes / YES 以外（読めなかったときも）は中止。
func confirmCommit(d Deps, u *ui.UI, g gitcommit.Git, s gitcommit.Snapshot, v ui.CommitView, br *bufio.Reader) error {
	for {
		u.CommitScreen(v)
		u.CommitPrompt()
		ans := ui.ReadAnswer(br)
		if ans == "d" || ans == "D" {
			// textconv と外部の diff は、コンテナが書いた内容をホストのプログラムで処理することになるので止める。
			// 見せるのは確定した tree の差分で、作業ツリーではない。
			diff, err := g.Out(nil, "diff", "--no-color", "--no-ext-diff", "--no-textconv", s.Base, s.Tree)
			if err != nil {
				return lore("刻印を取りやめました", err)
			}
			if err := d.Pager(diffContent(d, u, diff)); err != nil {
				info(d, "注意: 差分を表示できませんでした（%s）", ui.EscapeLine(err.Error()))
			}
			continue
		}
		if !ui.IsYes(ans) {
			return lore("刻印を取りやめました", errors.New("中止しました"))
		}
		return nil
	}
}

// diffContent は、ページャーに渡す差分の文字列を作る。ホストに delta があり、色を付ける
// モード（Plain / Mono でない）なら delta で整形し、なければ、delta が失敗したら、組み込みの
// ui.DiffPage に戻す。
//
// 差分はコンテナが書いた内容（信頼できない）なので、次の 3 つで守る。
//  1. delta に渡す前に、行ごとに ui.Escape をかける。delta は生の制御文字を見ず、
//     出力に残るエスケープは delta 自身の SGR と `ESC[K`（行末まで消去）だけになる（less -R に渡してよい）。
//  2. --no-gitconfig で、利用者の git config の [delta] を読ませない。hyperlinks が有効だと、
//     コンテナが決めたファイル名が OSC 8 のリンクになり、macOS で .command などを
//     クリックすると実行されうるため。hyperlinks は有効にしない。
//     DELTA_FEATURES は --no-gitconfig でも効き、`hyperlinks` や `+hyperlinks` で OSC 8 が戻ってしまう。
//     sigil は読まない変数なので、子プロセスにだけ空で渡して、利用者の値を上書きする（deniedEnvs の対象外）。
//     ほかに機能を有効にする環境変数は delta --help にない（DELTA_PAGER は --paging=never で無関係）。
//  3. 標準入力は tty を引き継がず、sigil が渡す文字列だけ。足す環境変数は 2 で述べた空の `DELTA_FEATURES=` だけ。
//  4. delta が成功しても、出力を deltaOutputSafe で確かめる。less -R は OSC 8 を生で通すので、
//     将来の delta が別の方法でハイパーリンクを出しても、SGR と `ESC[K` 以外なら捨てて組み込みの表示に戻す（多層防御）。
//
// --paging=never は、ページャーを sigil が起動するため（delta が less を起動しないように）。
// --line-numbers は読みやすさのため（利用者の選択）。ほかの引数は足さない。
func diffContent(d Deps, u *ui.UI, diff string) string {
	if !u.Colored() {
		return u.DiffPage(diff)
	}
	deltaPath, err := d.LookPath("delta")
	if err != nil {
		return u.DiffPage(diff)
	}
	var out, errOut bytes.Buffer
	err = d.Cmd.Run(deltaPath, []string{"--no-gitconfig", "--paging=never", "--line-numbers"}, gitcommit.RunOpts{
		Env:    []string{"DELTA_FEATURES="},
		Stdin:  strings.NewReader(ui.Escape(diff)),
		Stdout: &out,
		Stderr: &errOut,
	})
	if err != nil {
		info(d, "注意: delta で差分を整形できませんでした（%s）。組み込みの表示を使います", ui.EscapeLine(err.Error()))
		return u.DiffPage(diff)
	}
	if !deltaOutputSafe(out.String()) {
		info(d, "注意: delta の出力に想定外の制御シーケンスが含まれていたので、組み込みの表示を使います")
		return u.DiffPage(diff)
	}
	return out.String()
}

// deltaOutputSafe は、delta の出力が SGR（ESC [ ... m）と EL（ESC [ ... K）のほかに、
// 端末へ効く制御を含まないかを確かめる。ESC は後ろが `[` の CSI だけ、BEL・C1 制御
// （U+0080〜U+009F）・不正な UTF-8 は不可。less -R は OSC 8 などを生で通すため、
// 入力を Escape した上でも、delta 側の想定外の出力をここで止める。
func deltaOutputSafe(s string) bool {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n <= 1:
			return false
		case r == '\a' || (r >= 0x80 && r <= 0x9f):
			return false
		case r == '\x1b':
			j := i + 1
			if j >= len(s) || s[j] != '[' {
				return false
			}
			j++
			// パラメータ（0x30-0x3F）と中間（0x20-0x2F）のあと、最終バイトは m か K だけ
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
				j++
			}
			if j >= len(s) || (s[j] != 'm' && s[j] != 'K') {
				return false
			}
			n = j + 1 - i
		}
		i += n
	}
	return true
}

// pushFlow は、upstream があれば push 先を見せて聞き、承認されたら pre-push を経て push する。
// ここまで来たらコミットはできているので、断られても失敗しても、コミットは残す。
func pushFlow(d Deps, u *ui.UI, g gitcommit.Git, r gitcommit.Repo, h hookRunner, br *bufio.Reader) error {
	up, ok, err := gitcommit.FindUpstream(g, r)
	if err != nil {
		return lore("解き放てませんでした", fmt.Errorf("push 先を調べられませんでした。コミットは残っています（%w）", err))
	}
	if !ok {
		// push 先は推測しない（違うリモートへ送らないように）
		info(d, "upstream がないので解き放ちません。git push -u <remote> %s を手動で実行してください",
			ui.EscapeLine(strings.TrimPrefix(r.Branch, "refs/heads/")))
		return nil
	}
	// URL に入りうる認証情報（insteadOf で足されたトークンなど）は、画面にもコンテナにも出さない。
	// 本物の push はリモートの名前で行うので、取り除いた URL は表示とフックの引数にだけ使う。
	url := gitcommit.RedactURL(up.URL)
	u.PushPrompt(ui.PushView{Remote: up.Remote, URL: url, Branch: up.MergeRef, Ahead: up.Ahead})
	if !ui.IsYes(ui.ReadAnswer(br)) {
		info(d, "解き放ちませんでした。コミットは残っています")
		return nil
	}
	if h.hooks.Present[gitcommit.PrePush] {
		stages := []ui.Stage{{Label: "試練に挑む", Note: gitcommit.PrePush}}
		err := u.Ritual("試練に挑みます", "コンテナで hooks を実行しています", stages, func(w io.Writer) error {
			_, err := h.run(w, gitcommit.PrePush, []string{up.Remote, url}, up.PrePushInput(r, r.Head),
				"push していません。コミットは残っています")
			return err
		})
		if err != nil {
			return err
		}
	}
	// SSH のパスフレーズなどを端末で聞かれることがあるので、描画せずに git の出力をそのまま流す。
	// 標準出力と標準エラーは別々に渡す。Push は標準エラーだけを Escape して包むので、同じ先を渡すと、
	// 2 つの Writer が同じ先へ同時に書くことになる。
	if err := gitcommit.Push(g, up, r.Head, d.Stdout, d.Stderr); err != nil {
		return lore("解き放てませんでした", fmt.Errorf("%w。コミットは残っています", err))
	}
	info(d, "解き放ちました（%s %s）", ui.EscapeLine(up.Remote), ui.EscapeLine(up.MergeRef))
	return nil
}

// commitErr は gitcommit のエラーに世界観の言葉を添える。2 行目は普通の言葉での説明と対処。
func commitErr(err error) error {
	if err == nil {
		return nil
	}
	var ref *gitcommit.Refusal
	switch {
	case errors.As(err, &ref):
		return lore(ref.Lore, errors.New(ref.Msg))
	case errors.Is(err, gitcommit.ErrNoMessage):
		return loref("銘がありません", "コミットメッセージがありません。プロジェクトの最上位の %s に書いてください（コンテナの Claude Code に頼めます）", gitcommit.MessageFile)
	case errors.Is(err, gitcommit.ErrNothing):
		return loref("刻むものがありません", "コミットする変更がありません")
	case errors.Is(err, gitcommit.ErrMoved):
		// ErrMoved の文言が説明と対処を兼ねているので、そのまま 2 行目にする
		return lore("刻印は果たされませんでした", err)
	}
	return lore("刻印できません", err)
}
