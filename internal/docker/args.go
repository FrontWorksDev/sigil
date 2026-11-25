// Package docker は、docker コマンドの引数の組み立てと実行を受け持つ。
package docker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"sigil/internal/config"
	"sigil/internal/project"
)

const (
	// LayoutLabel は、コンテナのマウント配置の版を示すラベル。
	// 配置を変えたら LayoutVersion を上げ、古いコンテナを作り直してもらう。
	LayoutLabel   = "sigil.layout"
	LayoutVersion = "2"
	// ProjectLabel は、コンテナがどのプロジェクトのものかを示すラベル。sigil ls で使う。
	ProjectLabel = "sigil.project"
	// ReadOnlyLabel は、承認した ro のマウント（解決したパスとマウント先の JSON）を残すラベル。
	// docker は docker start のたびにマウント元のパスを解決し直すので、停止中のコンテナを起動する前に、
	// パスが作成時から差し替えられていないかを、このラベルと比べて確かめる（20 章「既存のコンテナ」）。
	ReadOnlyLabel = "sigil.ro"

	containerHome = "/home/dev"
)

// RunSpec は、コンテナを作るのに必要な情報。
type RunSpec struct {
	Name          string
	ProjectRoot   string // ホスト側のプロジェクトのパス
	ContainerPath string // コンテナ内のプロジェクトのパス
	Home          string // ホストのホームディレクトリ（nvim の設定の場所を決めるためだけに使う）
	Image         string
	HasGit        bool // <ProjectRoot>/.git がある
	HasNvimConfig bool // <Home>/.config/nvim がある
	Envs          []string
	Ports         []config.Port
	// SecretEnv は、docker のプロセスにだけ環境として渡す "NAME=値"（キーチェーンから読んだ値など）。
	// RunArgs は使わない。値をコマンドライン（ps で誰でも見える）に載せず、
	// Envs の "-e NAME" で docker がこの環境から値を読む。
	SecretEnv []string
	// ReadOnly は、.sigil の ro で読み取り専用にマウントするフォルダ（20 章）。
	// Host は project.ResolveMounts で確かめ、シンボリックリンクを解決したもの。
	ReadOnly []project.Mount
}

// EncodeReadOnly は、ReadOnlyLabel の値を作る。マウントがなければ "[]"。
func EncodeReadOnly(ms []project.Mount) string {
	if len(ms) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ms)
	if err != nil {
		// 文字列だけの構造体なので失敗しない
		panic(err)
	}
	return string(b)
}

// DecodeReadOnly は、ReadOnlyLabel の値を読む。空ならマウントはない。
func DecodeReadOnly(v string) ([]project.Mount, error) {
	if v == "" {
		return nil, nil
	}
	var ms []project.Mount
	if err := json.Unmarshal([]byte(v), &ms); err != nil {
		return nil, fmt.Errorf("%s ラベルを読めません: %w", ReadOnlyLabel, err)
	}
	return ms, nil
}

// RunArgs は docker run の引数を返す。
//
// ホーム、~/.ssh、~/.aws、Docker ソケットは絶対にマウントしない。
// コンテナに渡すのは、プロジェクトのフォルダと、ここで明示したものだけ。
func RunArgs(s RunSpec) []string {
	args := []string{
		"run", "-d", "--init",
		"--name", s.Name,
		"--hostname", "sigil",
		"--label", ProjectLabel + "=" + s.ProjectRoot,
		"--label", LayoutLabel + "=" + LayoutVersion,
		// ro がなくても必ず付ける。docker run のラベルはイメージのラベルより優先されるので、
		// イメージ（ベースのイメージを含む）が sigil.ro を持っていても、その値を受け継がない
		"--label", ReadOnlyLabel + "=" + EncodeReadOnly(s.ReadOnly),
		// 権限を最小にし、setuid などで権限を取り戻させない
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"-v", s.ProjectRoot + ":" + s.ContainerPath,
	}
	if s.HasGit {
		// .git には、ホストの git が実行する hooks や config がある。
		// コンテナから書き換えられると、ホストで git を使ったときに任意のコードが動くので読み取り専用にする。
		args = append(args, "-v", s.ProjectRoot+"/.git:"+s.ContainerPath+"/.git:ro")
	}
	for _, m := range s.ReadOnly {
		// 別のリポジトリを参照のために読ませるだけなので、書き込めないようにする。
		// 解決したパスを渡し、途中のシンボリックリンクを docker にたどらせない
		args = append(args, "-v", m.Host+":"+m.Container+":ro")
	}
	if s.HasNvimConfig {
		// Neovim の設定はホストの Neovim も読むので、コンテナから書き換えさせない。
		args = append(args, "-v", s.Home+"/.config/nvim:"+containerHome+"/.config/nvim:ro")
	}
	args = append(args,
		"-e", "SIGIL_PROJECT_DIR="+s.ContainerPath,
		// コンテナ内では所有者が違って見えるので、git に安全なフォルダとして教える
		"-e", "GIT_CONFIG_COUNT=1",
		"-e", "GIT_CONFIG_KEY_0=safe.directory",
		"-e", "GIT_CONFIG_VALUE_0="+s.ContainerPath,
		// プロジェクトをまたいで共有するキャッシュ類（ホストのフォルダではなく名前付きボリューム）
		"-v", "sigil-nvim-data:"+containerHome+"/.local/share/nvim",
		"-v", "sigil-nvim-state:"+containerHome+"/.local/state/nvim",
		"-v", "sigil-claude:"+containerHome+"/.claude",
		"-v", "sigil-cache:"+containerHome+"/.cache",
		"-v", "sigil-go:"+containerHome+"/go",
		"-v", "sigil-cargo:"+containerHome+"/.cargo",
		// gh のキャッシュ（gh run view --log が保存する Actions のログなど）は、そのリポジトリの
		// 非公開のデータで、CI に出た秘密を含むこともある。共有の ~/.cache に置くと、ほかの
		// プロジェクトのコードから読めてしまうので、このコンテナだけのメモリ上に置く（issue #12）。
		// コンテナを止めると消えるが、ログはまた取りに行くだけなので困らない。
		"--tmpfs", containerHome+"/.cache/gh:rw,noexec,nosuid,nodev",
	)
	for _, e := range s.Envs {
		// 値は付けない。docker が sigil のプロセスの環境から読むので、
		// 値がコマンドライン（ps で誰でも見える）に載らない。
		args = append(args, "-e", e)
	}
	for _, p := range s.Ports {
		args = append(args, "-p", p.Publish())
	}
	return append(args, "-w", s.ContainerPath, s.Image)
}

// ExecArgs は docker exec の引数を返す。
//
// env は追加で渡す "NAME=VALUE"（sigil check の NO_COLOR=1 など）。秘密の値は決して入れないこと
// （コマンドラインは ps で見える）。
func ExecArgs(name, workdir string, tty bool, env []string, cmd []string) []string {
	flag := "-i"
	if tty {
		flag = "-it"
	}
	args := []string{
		"exec", flag,
		// cmux（Ghostty 系）の独自の端末情報はコンテナ内にないので、どこにでもある値に固定する
		"-e", "TERM=xterm-256color",
		"-e", "COLORTERM=truecolor",
	}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, "-w", workdir, name)
	return append(args, cmd...)
}

// BuildArgs は docker build の引数を返す。
// UID / GID をホストと合わせ、マウントしたファイルの所有者がずれないようにする。
func BuildArgs(image string, uid, gid int, extra []string, contextDir string) []string {
	args := []string{
		"build",
		"--build-arg", "USER_UID=" + strconv.Itoa(uid),
		"--build-arg", "USER_GID=" + strconv.Itoa(gid),
		"-t", image,
	}
	args = append(args, extra...)
	return append(args, contextDir)
}

// LsRowsArgs は、sigil ls を飾って出すときの docker ps の引数を返す（タブ区切りで読む）。
func LsRowsArgs() []string {
	return []string{"ps", "-a", "--filter", "label=" + ProjectLabel,
		"--format", "{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Label \"" + ProjectLabel + "\"}}"}
}

// LsRow は sigil ls の 1 行。
type LsRow struct {
	Name, State, Status, Project string
}

// ParseLs は LsRowsArgs の出力を読む。形の合わない行は飛ばす。
func ParseLs(out string) []LsRow {
	var rows []LsRow
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 4 {
			continue
		}
		rows = append(rows, LsRow{Name: f[0], State: f[1], Status: f[2], Project: f[3]})
	}
	return rows
}

// LsArgs は sigil ls の docker ps の引数を返す。
func LsArgs() []string {
	return []string{"ps", "-a", "--filter", "label=" + ProjectLabel,
		"--format", "table {{.Names}}\t{{.Status}}\t{{.Label \"" + ProjectLabel + "\"}}"}
}
