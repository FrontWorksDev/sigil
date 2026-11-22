// Package docker は、docker コマンドの引数の組み立てと実行を受け持つ。
package docker

import (
	"strconv"

	"sigil/internal/config"
)

const (
	// LayoutLabel は、コンテナのマウント配置の版を示すラベル。
	// 配置を変えたら LayoutVersion を上げ、古いコンテナを作り直してもらう。
	LayoutLabel   = "sigil.layout"
	LayoutVersion = "2"
	// ProjectLabel は、コンテナがどのプロジェクトのものかを示すラベル。sigil ls で使う。
	ProjectLabel = "sigil.project"

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
func ExecArgs(name, workdir string, tty bool, cmd []string) []string {
	flag := "-i"
	if tty {
		flag = "-it"
	}
	args := []string{
		"exec", flag,
		// cmux（Ghostty 系）の独自の端末情報はコンテナ内にないので、どこにでもある値に固定する
		"-e", "TERM=xterm-256color",
		"-e", "COLORTERM=truecolor",
		"-w", workdir,
		name,
	}
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

// LsArgs は sigil ls の docker ps の引数を返す。
func LsArgs() []string {
	return []string{"ps", "-a", "--filter", "label=" + ProjectLabel,
		"--format", "table {{.Names}}\t{{.Status}}\t{{.Label \"" + ProjectLabel + "\"}}"}
}
