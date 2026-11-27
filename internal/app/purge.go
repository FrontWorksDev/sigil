package app

import (
	"fmt"
	"strings"

	"github.com/FrontWorksDev/sigil/internal/docker"
	"github.com/FrontWorksDev/sigil/internal/project"
	"github.com/FrontWorksDev/sigil/internal/ui"
)

// purge は、このプロジェクトのコンテナと、プロジェクトごとのボリュームを、確かめてから消す（設計書 21 章）。
//
// 消すのは、ラベル sigil.project がこのプロジェクトのパスとバイトの完全一致で、名前も完全に一致するものだけ。
// 名前の前方一致では、foo に対する foo-1a2b3c4d-x のような別のプロジェクトのボリュームにも合ってしまい、
// docker volume ls --filter label= は値の余白を取り除くので、どちらも選ぶのに使わない。
// 消したものは戻らないので、確認を飛ばすオプションは作らない。
func purge(d Deps, client docker.Client, p project.Project) error {
	name := ui.EscapeLine(p.ContainerName)

	st, _, err := client.Inspect(p.ContainerName)
	if err != nil {
		return lore("領域の様子を確かめられません", err)
	}
	container := false
	if st != docker.Missing {
		root, err := client.ContainerProject(p.ContainerName)
		if err != nil {
			return lore("領域の様子を確かめられません", err)
		}
		if root == p.Root {
			container = true
		} else {
			info(d, "注意: コンテナ %s は、このプロジェクトのものではないので消しません", name)
		}
	}

	var vols []string
	for _, v := range docker.ProjectVolumes(p.ContainerName) {
		mine, err := ownVolume(client, v.Name, p.Root)
		if err != nil {
			return lore("領域の様子を確かめられません", err)
		}
		switch mine {
		case volMine:
			vols = append(vols, v.Name)
		case volForeign:
			info(d, "注意: ボリューム %s は、このプロジェクトのものではないので消しません", ui.EscapeLine(v.Name))
		}
	}

	if !container && len(vols) == 0 {
		info(d, "領域 %s に浄化するものはありません", name)
		return nil
	}

	info(d, "次のものを消します。消したものは戻せません")
	if container {
		fmt.Fprintf(d.Stderr, "  コンテナ      %s\n", name)
	}
	for _, v := range vols {
		fmt.Fprintf(d.Stderr, "  ボリューム    %s\n", ui.EscapeLine(v))
	}
	fmt.Fprintf(d.Stderr, "  プロジェクト  %s\n", ui.EscapeLine(p.Root))
	fmt.Fprint(d.Stderr, "浄化しますか？ [y/N] ")

	tty, err := d.OpenTTY()
	if err != nil {
		fmt.Fprintln(d.Stderr)
		return loref("浄化を確かめられません", "対話できるターミナルで実行してください")
	}
	defer tty.Close()
	if !ui.IsYes(ui.ReadAnswer(tty)) {
		info(d, "浄化しませんでした")
		return nil
	}

	if container {
		if err := client.Remove(p.ContainerName); err != nil {
			return loref("領域を浄化できませんでした", "%s を削除できませんでした: %w", name, err)
		}
	}
	// 確認を待つあいだに、ほかのペインが作り直したなどでラベルが変わっていないか、消す直前に確かめ直す
	var rm []string
	for _, v := range vols {
		mine, err := ownVolume(client, v, p.Root)
		if err != nil {
			return lore("領域の様子を確かめられません", err)
		}
		if mine == volMine {
			rm = append(rm, v)
		} else if mine == volForeign {
			info(d, "注意: ボリューム %s は、このプロジェクトのものではないので消しません", ui.EscapeLine(v))
		}
	}
	if len(rm) > 0 {
		if err := client.RemoveVolumes(rm); err != nil {
			return loref("領域を浄化できませんでした",
				"ボリューム %s を削除できませんでした。ほかのペインで sigil を使っていないか確かめてください: %w",
				ui.EscapeLine(strings.Join(rm, ", ")), err)
		}
	}
	info(d, "領域 %s を浄化しました", name)
	return nil
}

type volOwner int

const (
	volMissing volOwner = iota
	volMine
	volForeign
)

// ownVolume は、ボリュームがあるか、あればラベルがこのプロジェクトのもの（バイトの完全一致）かを返す。
func ownVolume(client docker.Client, name, root string) (volOwner, error) {
	ok, err := client.VolumeExists(name)
	if err != nil {
		return volMissing, err
	}
	if !ok {
		return volMissing, nil
	}
	got, has, err := client.VolumeProject(name)
	if err != nil {
		return volMissing, err
	}
	if !has || got != root {
		return volForeign, nil
	}
	return volMine, nil
}
