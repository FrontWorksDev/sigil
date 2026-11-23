package app

import (
	"strings"

	"sigil/internal/gitcommit"
	"sigil/internal/ui"
)

// commitView は、確認の画面に出す内容を作る。パスや銘は生のまま渡す（ui が描くときに Escape する）。
// Warning.Reason は gitcommit で Escape 済みだが、Escape は同じ結果になるので、ui でもう一度かけてよい。
func commitView(g gitcommit.Git, r gitcommit.Repo, s gitcommit.Snapshot, pathspec []string, msg string, ran []ui.HookRow) (ui.CommitView, error) {
	v := ui.CommitView{
		Branch:   strings.TrimPrefix(r.Branch, "refs/heads/"),
		Pathspec: pathspec,
		Message:  msg,
		Hooks:    ran,
	}
	if r.Head == "" {
		v.HeadShort = "（まだコミットがありません）"
	} else {
		// 署名の確認（log.showSignature）で gpg を動かさない
		out, err := g.Out(nil, "log", "-1", "--no-show-signature", "--format=%h%x00%s", r.Head)
		if err != nil {
			return v, lore("刻印できません", err)
		}
		v.HeadShort, v.HeadSubject, _ = strings.Cut(strings.TrimSuffix(out, "\n"), "\x00")
	}
	for _, c := range s.Changes {
		v.Files = append(v.Files, ui.FileRow{
			Status: c.Status, Path: c.Path, OldPath: c.OldPath,
			Added: c.Added, Deleted: c.Deleted, Binary: c.Binary, Size: c.Size,
		})
	}
	for _, w := range gitcommit.Warnings(s.Changes, r) {
		v.Warnings = append(v.Warnings, ui.WarnRow{Path: w.Path, Reason: w.Reason})
	}
	return v, nil
}

// shortHash は commit の短いハッシュ。git に求められなければ先頭 7 文字。
func shortHash(g gitcommit.Git, commit string) string {
	if out, err := g.Out(nil, "rev-parse", "--short", commit); err == nil {
		return strings.TrimSpace(out)
	}
	return commit[:min(7, len(commit))]
}
