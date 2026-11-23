package gitcommit

import (
	"path"
	"path/filepath"
	"strings"

	"sigil/internal/ui"
)

// Warning は、確認の画面で「確かめてほしいもの」として出す 1 件。
// Path は生のまま（表示する側で Escape する）。Reason は Escape 済み。
type Warning struct {
	Path, Reason string
}

const largeFile = 1 << 20

// Warnings は、設計書 17 章「確認の画面」3 の一覧どおりに注意を分類する。
// 削除は、新しく何かを持ち込むわけではないので対象外。
func Warnings(cs []Change, r Repo) []Warning {
	hooksRel := ""
	if rel, err := filepath.Rel(r.Root, r.HooksDir); err == nil && r.HooksDir != "" &&
		rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		hooksRel = filepath.ToSlash(rel) + "/"
	}
	var out []Warning
	for _, c := range cs {
		if c.Status == 'D' {
			continue
		}
		add := func(reason string) { out = append(out, Warning{Path: c.Path, Reason: reason}) }
		p := c.Path
		base := path.Base(p)

		if strings.HasPrefix(p, ".github/") || p == ".gitlab-ci.yml" || strings.HasPrefix(p, ".circleci/") {
			add("GitHub などで実行されます")
		}
		if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") ||
			strings.HasSuffix(base, ".key") || strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") {
			add("秘密の情報が入っているかもしれません")
		}
		switch base {
		case ".gitignore", ".gitattributes", ".gitmodules", ".lfsconfig":
			add("git の動作を変えます")
		}
		if (hooksRel != "" && strings.HasPrefix(p, hooksRel)) || strings.HasPrefix(p, ".husky/") ||
			base == ".pre-commit-config.yaml" || base == "lefthook.yml" || base == "lefthook.yaml" {
			add("ほかの人の環境や CI で実行されます")
		}
		if p == ".sigil" {
			add("sigil の設定です")
		}
		if c.NewMode == "100755" && c.OldMode != "100755" {
			add("実行権限が付きます")
		}
		if c.NewMode == "120000" {
			add("シンボリックリンクです（リンク先: " + ui.EscapeLine(c.LinkTarget) + "）")
		}
		if c.Size > largeFile {
			add("1MiB を超えるファイルです")
		}
	}
	return out
}
