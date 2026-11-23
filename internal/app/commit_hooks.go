package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"sigil/internal/docker"
	"sigil/internal/gitcommit"
	"sigil/internal/project"
	"sigil/internal/ui"
)

// hookLogMax は、フックの出力をためておく上限。コンテナが際限なく出力しても、
// ホストのメモリを使い切らないようにする。
const hookLogMax = 1 << 20

// capBuffer は、max バイトまでためて、それ以降は捨てる Writer。
// 捨てるときもエラーにしない（docker の出力の書き込みを失敗させて、結果を変えないため）。
type capBuffer struct {
	buf     bytes.Buffer
	max     int
	dropped bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	room := c.max - c.buf.Len()
	if room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	if len(p) > room {
		c.dropped = true
	}
	return len(p), nil
}

// hookRunner は、リポジトリのフックをコンテナで実行する。
// フックの中身はプロジェクトのコードなので、ホストでは決して実行しない。
type hookRunner struct {
	d     Deps
	p     project.Project
	r     gitcommit.Repo
	hooks gitcommit.Hooks
}

// ready は、フックを実行できる状態か（docker があり、コンテナが実行中か）を確かめる。
// フックがあるのに実行できないとき、黙って飛ばすと検査が抜けたままコミットされるので、止める。
func (h hookRunner) ready() error {
	if _, err := h.d.LookPath("docker"); err != nil {
		return errNoDocker()
	}
	st, _, err := docker.Client{R: h.d.Runner, Stderr: h.d.Stderr}.Inspect(h.p.ContainerName)
	if err != nil {
		return lore("領域の様子を確かめられません", err)
	}
	if st != docker.Running {
		return loref("領域が開いていません", "sigil で領域を開いてから、もう一度実行してください")
	}
	return nil
}

// run は、フック name をコンテナで実行し、標準出力（msg のときは書き換えたあとの銘）を返す。
// フックの出力はためておき、失敗したときだけ Escape して w に出す（成功したら捨てる）。
// after は、失敗したときに何が起きなかったかの説明（「コミットしていません」など）。
func (h hookRunner) run(w io.Writer, name string, extra []string, stdin, after string) ([]byte, error) {
	args, err := gitcommit.HookExecArgs(h.p.ContainerName, h.p.ContainerPath, h.r.Root, h.hooks.Dir, name, extra)
	if err != nil {
		// FindHooks が先に拒否しているはずなので、ここに来るのは呼び出しの誤り。
		// HookExecArgs の文言はフックの場所を生のまま含むので使わず、Escape して出し直す。
		return nil, loref("刻印できません", "フックの場所（%s）がプロジェクトの外にあり、領域から実行できません", ui.EscapeLine(h.hooks.Dir))
	}
	log := &capBuffer{max: hookLogMax}
	// msg のときの標準出力は銘そのもの。上限を 1 バイト超えるまでためれば、Normalize が「大きすぎる」と判断できる。
	out := &capBuffer{max: gitcommit.MaxMessageSize + 1}
	var stdout io.Writer = out
	var in io.Reader
	if stdin != "" {
		in = strings.NewReader(stdin)
	}
	if name == gitcommit.PreCommit || name == gitcommit.PrePush {
		// 標準出力も、ためておく出力の一部（銘ではない）
		stdout = log
	}
	if err := gitcommit.RunHook(h.d.Cmd, args, in, stdout, log); err != nil {
		text := ui.Escape(log.buf.String())
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if log.dropped {
			text += "（出力が長いので、途中から省きました）\n"
		}
		io.WriteString(w, text)
		return nil, loref("試練に敗れました", "%s のフックが失敗したので、%s", name, after)
	}
	return out.buf.Bytes(), nil
}

// prepare は、pre-commit → tree の確定 → prepare-commit-msg / commit-msg を行う。
// フックがあれば、実際の待ち時間なので魔法陣を描き、その中で行う。なければ描かずに tree だけ確定する。
// フックが銘を書き換えたら、銘のファイルと同じ検査（Normalize）をやり直す。
// 返した Snapshot の一時フォルダは、呼んだ側が Cleanup する（エラーのときはここで片付ける）。
func (h hookRunner) prepare(u *ui.UI, g gitcommit.Git, pathspec []string, msg string) (gitcommit.Snapshot, string, []ui.HookRow, error) {
	pre := h.hooks.Present[gitcommit.PreCommit]
	var msgHooks []string
	for _, n := range []string{gitcommit.PrepareCommitMsg, gitcommit.CommitMsg} {
		if h.hooks.Present[n] {
			msgHooks = append(msgHooks, n)
		}
	}
	if !pre && len(msgHooks) == 0 {
		s, err := gitcommit.TakeSnapshot(g, h.r, pathspec)
		return s, msg, nil, commitErr(err)
	}

	stages := []ui.Stage{
		{Label: "試練に挑む", Note: gitcommit.PreCommit},
		{Label: "紋様を写し取る", Note: "tree の確定"},
		{Label: "銘を検める", Note: strings.Join(msgHooks, " / ")},
	}
	if !pre {
		stages[0].State = ui.Skipped
	}
	if len(msgHooks) == 0 {
		stages[2].State = ui.Skipped
	}
	const after = "コミットしていません"
	var (
		prog ui.Progress
		s    gitcommit.Snapshot
		ran  []ui.HookRow
	)
	// work は描画の裏の goroutine で動く。s・msg・ran は、Ritual が work の終わりを待ってから戻るので、
	// 戻ったあとに読んでよい。
	work := func(w io.Writer) error {
		if pre {
			prog.Set(0, "", -1)
			// コンテナの一時 index にも、ホストと同じ範囲を add させる
			if _, err := h.run(w, gitcommit.PreCommit, gitcommit.EffectivePathspec(pathspec), "", after); err != nil {
				return err
			}
			ran = append(ran, ui.HookRow{Name: gitcommit.PreCommit, OK: true})
		}
		// pre-commit が作業ツリーを整形していれば、整形後の内容がここで入る
		prog.Set(1, "", -1)
		var err error
		if s, err = gitcommit.TakeSnapshot(g, h.r, pathspec); err != nil {
			return commitErr(err)
		}
		prog.Set(2, "", -1)
		for _, n := range msgHooks {
			var extra []string
			if n == gitcommit.PrepareCommitMsg {
				// git が -m / -F で銘を受け取ったときと同じ引数
				extra = []string{"message"}
			}
			b, err := h.run(w, n, extra, msg+"\n", after)
			if err != nil {
				return err
			}
			m, err := gitcommit.Normalize(b)
			if errors.Is(err, gitcommit.ErrNoMessage) {
				return loref("銘がありません", "%s のフックのあとで、コミットメッセージが空になりました", n)
			}
			if err != nil {
				return lore("試練に敗れました", fmt.Errorf("%s のフックが書き換えた銘を使えません: %w", n, err))
			}
			msg = m
			ran = append(ran, ui.HookRow{Name: n, OK: true})
		}
		return nil
	}
	if err := u.RitualWithProgress("試練に挑みます", "コンテナで hooks を実行しています", stages, &prog, work); err != nil {
		s.Cleanup()
		return gitcommit.Snapshot{}, "", nil, err
	}
	return s, msg, ran, nil
}
