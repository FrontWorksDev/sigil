package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Volume は、プロジェクトごとの名前付きボリュームと、コンテナ内のマウント先。
type Volume struct {
	Name, Target string
}

// ProjectVolumes は、コンテナ（= プロジェクト）ごとに分けるボリュームの一覧を返す（設計書 21 章）。
//
// ~/.cache・~/go・~/.cargo は、以前は全プロジェクトで共有していた。ところがハッシュの検証は
// ダウンロードのときだけで、キャッシュから読むときには働かない。Go のビルドキャッシュ（サイズしか照合しない）、
// 展開済みのソース、uvx / npx の環境、PATH で /usr/local/bin より前にある ~/go/bin・~/.cargo/bin を通じて、
// あるプロジェクトのコードが、ほかのプロジェクトのコンテナで実行できた（issue #7、#14、#15）。
// そこで、名前にコンテナ名（[A-Za-z0-9_.-] だけ）を付け、プロジェクトごとに分ける。
func ProjectVolumes(container string) []Volume {
	return []Volume{
		{container + "-cache", containerHome + "/.cache"},
		{container + "-go", containerHome + "/go"},
		{container + "-cargo", containerHome + "/.cargo"},
		// ~/.claude（CLAUDE_CONFIG_DIR）。共有すると、hooks やプラグインでほかのプロジェクトの Claude Code の中で
		// コードを実行でき、会話の履歴も読めた（22 章）
		{container + "-claude", containerHome + "/.claude"},
	}
}

// ForeignVolumeError は、プロジェクトごとのボリュームの名前のものがあるのに、ラベルがこのプロジェクトのものではないこと。
// 名前は外から来うる文字列なので、表示する側でエスケープすること。
type ForeignVolumeError struct {
	Name string
}

func (e *ForeignVolumeError) Error() string {
	return "ボリューム " + e.Name + " は、このプロジェクトのために sigil が作ったものではありません。中身を確かめてから docker volume rm で消してください"
}

// EnsureVolumes は、プロジェクトごとのボリュームを用意する。なければラベル付きで作り、
// あればラベルがこのプロジェクトのものかを確かめる。docker run の前に呼ぶこと。
//
// ラベルが違う・ないボリュームは使わずに *ForeignVolumeError を返す。sigil が作っていないものや、
// 別のプロジェクトのもの（コンテナ名の衝突など）を、このプロジェクトのコンテナに渡さないため。
// 2 つのペインが同時に作っても、docker volume create は既にあれば成功するので、作ったあとにもラベルを読む。
func (c Client) EnsureVolumes(container, projectRoot string) error {
	for _, v := range ProjectVolumes(container) {
		exists, err := c.VolumeExists(v.Name)
		if err != nil {
			return err
		}
		if !exists {
			if err := c.CreateVolume(v.Name, projectRoot); err != nil {
				return fmt.Errorf("ボリューム %s を作れません（%w）", v.Name, err)
			}
		}
		got, ok, err := c.VolumeProject(v.Name)
		if err != nil {
			return err
		}
		if !ok || got != projectRoot {
			return &ForeignVolumeError{Name: v.Name}
		}
	}
	return nil
}

// VolumeExists は、名前が完全に一致するボリュームがあるかを返す。
//
// docker volume inspect は、ボリュームがないときも docker が使えないときも失敗するので、
// あるかどうかの判定には使わない（失敗を「ない」とすると、Docker Desktop が止まっているときに作りに行く）。
// docker volume ls なら、なければ成功して何も出さず、docker の異常は終了コードで分かる。
// name= の絞り込みは部分一致なので、行ごとに完全一致を比べる。
func (c Client) VolumeExists(name string) (bool, error) {
	var out bytes.Buffer
	args := []string{"volume", "ls", "--filter", "name=" + name, "--format", "{{.Name}}"}
	if err := c.R.Run("docker", args, &out, nil); err != nil {
		return false, fmt.Errorf("docker に接続できません。Docker Desktop が起動しているか確認してください（%w）", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if line == name {
			return true, nil
		}
	}
	return false, nil
}

// VolumeProject は、ボリュームのラベル sigil.project の値を返す。ラベルがなければ ok は false。
//
// 値は余白を取り除かずに返す。呼ぶ側はプロジェクトのパスとバイトの完全一致で比べること。
// 末尾が空白・改行のパスと、それがないパスを同じものとみなすと、別のプロジェクトのボリュームを
// このプロジェクトのものとして使ったり消したりしうる（docker volume ls --filter label= は値の余白を
// 取り除くので、選ぶのにも使わない）。
func (c Client) VolumeProject(name string) (value string, ok bool, err error) {
	var out bytes.Buffer
	args := []string{"volume", "inspect", "--format", "{{json .Labels}}", name}
	if err := c.R.Run("docker", args, &out, nil); err != nil {
		return "", false, fmt.Errorf("ボリューム %s のラベルを読めません（%w）", name, err)
	}
	var labels map[string]string
	// JSON の外側の改行（docker の出力の末尾）だけを除く。値の中の余白は JSON の文字列の中にあるので残る
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &labels); err != nil {
		return "", false, fmt.Errorf("ボリューム %s のラベルを読めません（%w）", name, err)
	}
	value, ok = labels[ProjectLabel]
	return value, ok, nil
}

// CreateVolume は、ラベル sigil.project=<projectRoot> を付けてボリュームを作る。
// 既にあれば docker は何もせずに成功する（ラベルも変えない）ので、呼ぶ側は作ったあとでラベルを確かめる。
//
// ラベルは docker run --mount の volume-label= では付けない。--mount の値は CSV として解釈されるので、
// パスにカンマがあると、マウントの指定（dst= など）を差し込めてしまう。ここでは argv の 1 要素として渡る。
func (c Client) CreateVolume(name, projectRoot string) error {
	return c.run(nil, "volume", "create", "--label", ProjectLabel+"="+projectRoot, name)
}

// RemoveVolumes はボリュームを消す。
//
// -f は付けない。確認を待つあいだに、ほかのペインがコンテナを作り直して使用中になっていれば、
// docker が断るので、使用中のものを消さずに済む。
func (c Client) RemoveVolumes(names []string) error {
	return c.run(nil, append([]string{"volume", "rm"}, names...)...)
}

// ContainerProject は、コンテナのラベル sigil.project の値を返す（余白は取り除かない）。
func (c Client) ContainerProject(name string) (string, error) {
	labels, err := c.containerLabels(name)
	if err != nil {
		return "", err
	}
	return labels[ProjectLabel], nil
}

// containerLabels は、コンテナのラベルを JSON で読む。
func (c Client) containerLabels(name string) (map[string]string, error) {
	var out bytes.Buffer
	args := []string{"container", "inspect", "--format", "{{json .Config.Labels}}", name}
	if err := c.R.Run("docker", args, &out, nil); err != nil {
		return nil, fmt.Errorf("%s のラベルを読めません（%w）", name, err)
	}
	var labels map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &labels); err != nil {
		return nil, fmt.Errorf("%s のラベルを読めません（%w）", name, err)
	}
	return labels, nil
}
