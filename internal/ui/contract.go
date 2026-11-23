package ui

import (
	"fmt"
	"io"
	"strings"
)

// ContractView は、.sigil / .env の承認の画面に出す内容。
// Refs は、.env の名前と 1Password の参照（空なら .env に直接書かれた値。値そのものは渡さない）。
type ContractView struct {
	Files []string
	Envs  []string
	Ports []string
	Refs  [][2]string
}

const (
	contractPrompt = "sigil: この契約を結ぶ（この設定で承認する）なら y を入力してください [y/N] "
	directValue    = "（.env に直接書かれた値）"
)

// ContractScreen は、承認の画面を出す。
//
// セキュリティ上の判断をしてもらう画面なので、演出より読みやすさを優先する（16 章）。
// Plain は、approval.Ask が出していた文言と 1 文字も違えない（スクリプトや記憶に頼る人のため）。
// それ以外は、枠と、区分ごとの印・色で飾る。どちらも、用語には実際の意味を添える。
func (u *UI) ContractScreen(v ContractView) {
	files := strings.Join(v.Files, " と ")
	if u.mode == Plain {
		u.contractPlain(v, files)
	} else {
		u.contractBox(v, files)
	}
	io.WriteString(u.w, contractPrompt)
}

func (u *UI) contractPlain(v ContractView, files string) {
	out := u.w
	fmt.Fprintf(out, "sigil: 契約の確認 ── %s の内容が新しいか、前回の承認から変わっています。\n", files)
	fmt.Fprintln(out, "     このプロジェクトの領域（コンテナ）に、次のものを渡します:")
	for _, e := range v.Envs {
		fmt.Fprintf(out, "       持ち込むもの（ホストの環境変数）  %s\n", e)
	}
	for _, p := range v.Ports {
		fmt.Fprintf(out, "       門（ポート）                      %s\n", p)
	}
	if len(v.Envs) == 0 && len(v.Ports) == 0 {
		fmt.Fprintln(out, "       （なし）")
	}
	if len(v.Refs) > 0 {
		fmt.Fprintln(out, "     禁書庫から召喚するもの（.env で、1Password から値を読み込む名前と参照先）:")
		for _, r := range v.Refs {
			ref := r[1]
			if ref == "" {
				ref = directValue
			}
			fmt.Fprintf(out, "       %-24s %s\n", r[0], ref)
		}
	}
	fmt.Fprintln(out, "     このほか、全体設定（~/.config/sigil/config）の分も渡されます。")
}

// contractBox は、枠で囲んだ承認の画面。用語と意味を 2 列にそろえ、その下に実際の値を並べる。
func (u *UI) contractBox(v ContractView, files string) {
	type group struct {
		mark  string
		color int
		term  string
		means string
		rows  []string
	}
	var groups []group
	if len(v.Envs) > 0 {
		groups = append(groups, group{"◆", yellow, "持ち込むもの", "ホストの環境変数", v.Envs})
	}
	if len(v.Ports) > 0 {
		groups = append(groups, group{"◈", cyan, "門", "ポート", v.Ports})
	}
	if len(v.Refs) > 0 {
		var rows []string
		for _, r := range v.Refs {
			ref := r[1]
			if ref == "" {
				ref = directValue
			}
			rows = append(rows, fmt.Sprintf("%-24s %s", r[0], ref))
		}
		groups = append(groups, group{"✦", purple, "禁書庫", ".env で、1Password から値を読み込む名前と参照先", rows})
	}

	termW := 0
	for _, g := range groups {
		termW = max(termW, Width(g.term))
	}

	lines := []string{
		u.fg(purple, "⟡") + " " + u.bold("契約の確認"),
		u.dim(EscapeLine(files) + " の内容が新しいか、前回の承認から変わっています。"),
		"",
		"このプロジェクトの領域（コンテナ）に、次のものを渡します:",
	}
	if len(groups) == 0 {
		lines = append(lines, "  "+u.dim("（なし）"))
	}
	for _, g := range groups {
		lines = append(lines, "  "+u.fg(g.color, g.mark)+" "+u.bold(g.term)+strings.Repeat(" ", termW-Width(g.term))+"  "+u.dim(g.means))
		for _, r := range g.rows {
			lines = append(lines, "      "+EscapeLine(r))
		}
	}
	lines = append(lines, "", u.dim("このほか、全体設定（~/.config/sigil/config）の分も渡されます。"))
	u.Box(lines...)
}
