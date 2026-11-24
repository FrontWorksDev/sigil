// Package app は sigil のサブコマンドの振り分けと、全体の流れを受け持つ。
package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sigil/internal/approval"
	"sigil/internal/config"
	"sigil/internal/docker"
	"sigil/internal/project"
	"sigil/internal/secret"
	"sigil/internal/ui"
)

// usage は普通の説明を主にし、メッセージに出てくる用語は〔〕で添える程度にする（issue #3）。
const usage = `使い方:
  sigil                   プロジェクトのコンテナに入る（なければ作る）〔領域に入る／領域展開〕
  sigil <command...>      コンテナ内でコマンドを実行   例: sigil nvim .   sigil claude
  sigil -- <command...>   stop / build などと同名のコマンドをコンテナ内で実行
  sigil stop              このプロジェクトのコンテナを削除（.sigil を変えたら一度これ）〔領域解除〕
  sigil ls                sigil のコンテナの一覧〔展開中の領域〕
  sigil check             隔離が効いているか、コンテナ内で確認〔結界の点検〕
  sigil build [args...]   イメージを作る（追加の引数は docker build へ）〔紋章の鋳造〕
  sigil commit [-- <path>...]
                          コンテナで用意した銘で、確認してコミット・push〔刻印の儀〕
  sigil help              この説明

設定:
  <project>/.sigil                このプロジェクトに渡す環境変数名とポート（変わるたびに確認）〔契約〕
  <project>/.env                  値（1Password の op:// 参照）
  ~/.config/sigil/config          全プロジェクト共通の環境変数名とポート
  ~/.config/sigil/secrets.env     全プロジェクト共通の値

環境変数:
  SIGIL_IMAGE     イメージ名（既定 sigil:latest）
  SIGIL_CONTEXT   sigil build で、埋め込みの代わりに使うビルドコンテキスト
  SIGIL_PLAIN     1 にすると演出（魔法陣やスピナー）を止める。NO_COLOR なら色だけ止める
  SIGIL_GLYPHS    greek にすると、魔法陣の外周をルーン文字ではなくギリシャ文字にする

`

// Run は sigil を実行し、終了コードを返す。
func Run(args []string, d Deps) int {
	if err := run(args, d); err != nil {
		printError(d.Stderr, err)
		return 1
	}
	return 0
}

func info(d Deps, format string, a ...any) {
	fmt.Fprintf(d.Stderr, "sigil: "+format+"\n", a...)
}

func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func run(args []string, d Deps) error {
	switch first(args) {
	case "help", "-h", "--help":
		// 標準出力が端末のときだけバナーを付ける（パイプでは説明だけにする）
		newStdoutUI(d).Banner()
		fmt.Fprint(d.Stdout, usage)
		return nil
	}

	// op run から起動し直された側では、op run に渡した一時ファイルはもう読まれているので消す。
	// 中身は参照（op://...）だけで秘密の値ではないが、残しておく理由もない。
	_, inner := d.LookupEnv(secret.InnerEnv)
	if inner {
		if dir, ok := d.LookupEnv(opTempEnv); ok && dir != "" {
			removeOpTemp(dir)
		}
	} else {
		warnLegacyEnv(d)
	}

	// commit は、コンテナでフックを実行するときだけ docker を使う。フックのないリポジトリでは
	// docker がなくてもコミットできるよう、ここでは止めない（要るときに commitCmd が同じエラーにする）。
	dockerPath, err := d.LookPath("docker")
	if err != nil && first(args) != "commit" {
		return errNoDocker()
	}
	client := docker.Client{R: d.Runner, Stderr: d.Stderr}
	u := newUI(d)

	switch first(args) {
	case "build":
		return build(d, u, args[1:])
	case "ls":
		return ls(d)
	}

	// ここから先はプロジェクトを特定してから動く。危険な場所なら、docker を呼ぶ前に止める。
	cwd, err := d.Getwd()
	if err != nil {
		return err
	}
	home, err := d.UserHomeDir()
	if err != nil {
		return err
	}
	dirs := newDirs(d, home)
	// sigil の設定と承認記録もマウントさせない（コンテナから書き換えられると、承認を偽造できる）
	p, err := project.Resolve(cwd, home, []string{dirs.config, dirs.state}, func(dir string) (string, error) {
		var out bytes.Buffer
		err := d.Runner.Run("git", []string{"-C", dir, "rev-parse", "--show-toplevel"}, &out, nil)
		return out.String(), err
	})
	if err != nil {
		return lore("ここに結界は張れません", err)
	}

	switch first(args) {
	case "stop":
		return stop(d, client, p)
	case "commit":
		// -- の後ろのパスは今いるフォルダから読むので、Root と同じくリンクを解いた cwd を渡す
		realCwd, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			realCwd = cwd
		}
		return commitCmd(d, u, p, realCwd, args[1:])
	}

	cmd := args
	switch first(args) {
	case "check":
		cmd = []string{"sigil-check"}
	case "--":
		cmd = args[1:]
	}
	if len(cmd) == 0 {
		cmd = []string{"bash", "-l"}
	}

	replaced, err := ensureContainer(d, u, client, p, home, dirs, args)
	if err != nil {
		return err
	}
	if replaced {
		// op run に置き換えた（本物の Exec なら、ここには来ない）。
		// 偽物の Exec のときに、そのままコンテナ作成や exec に進まないよう、ここで終える。
		return nil
	}

	// 作業フォルダは、シンボリックリンクを解決した cwd から決める（Root も解決済みなので）
	realCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		realCwd = cwd
	}
	// sigil check の色は、コンテナ内の sigil-check が決める（標準出力が端末なら色を付ける）。
	// ホストで NO_COLOR / SIGIL_PLAIN などにより色を止めているときは、それを伝える。
	// 値は秘密ではないので、コマンドラインに載せてよい。
	var env []string
	if first(args) == "check" && ui.DetectMode(d.StdoutIsTerminal(), d.LookupEnv) != ui.Color {
		env = []string{"NO_COLOR=1"}
	}
	argv := append([]string{"docker"},
		docker.ExecArgs(p.ContainerName, p.Workdir(realCwd), d.IsTerminal(), env, cmd)...)
	// docker に置き換え、Neovim や Claude Code に端末をそのまま渡す。
	// 置き換える前に、演出で変えた端末の状態（カーソルの非表示、色、描いていた行）を必ず戻す。
	// 戻さないと、Neovim や Claude Code の画面が壊れる。
	u.Restore()
	if err := d.Exec(dockerPath, argv, d.Environ()); err != nil {
		return loref("領域に入れませんでした", "docker exec を起動できませんでした: %w", err)
	}
	// 本物の Exec は戻らない。偽物のときに、ここで止める。
	return nil
}

// errNoDocker は、docker が見つからないときのエラー。
func errNoDocker() error {
	return lore("領域を開く術がありません", errors.New("docker が見つかりません。Docker Desktop をインストールしてください"))
}

// dirs は sigil の設定と状態のフォルダ。
type dirs struct {
	config string // $XDG_CONFIG_HOME/sigil
	state  string // $XDG_STATE_HOME/sigil
}

func newDirs(d Deps, home string) dirs {
	return dirs{
		config: filepath.Join(lookup(d, "XDG_CONFIG_HOME", filepath.Join(home, ".config")), "sigil"),
		state:  filepath.Join(lookup(d, "XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "sigil"),
	}
}

// settings は、コンテナを作るときに読む設定一式。
type settings struct {
	sigilPath string
	sigil     []byte // プロジェクトの .sigil（なければ nil）
	projCfg   config.Config
	globalCfg config.Config

	envPath   string
	opEntries []secret.Entry // .env のうち、1Password から読み込む名前と参照（確かめたもの）
	opEnv     []byte         // opEntries から作った、op run に渡す env ファイルの中身（なければ nil）
	warnings  []string

	secrets    []byte // 全体の secrets.env（なければ nil）
	secretsPth string
}

// loadSettings は設定を読み、.env から 1Password で読み込む名前と参照を選ぶ。
//
// op run は env ファイルの行をすべて、起動し直した sigil 自身の環境変数にする。
// 利用者の .env をそのまま渡すと、DOCKER_HOST や PATH を書かれたときに、sigil が攻撃者の
// docker に秘密を送ったり、偽の docker を実行したりしてしまう。そこで .env からは、
// 設定（.sigil / 全体設定）で列挙した名前で、制御用ではなく、値が op:// 参照のものだけを選び、
// それだけで作り直した env ファイルを op run に渡す（RenderEnv）。
// 参照ではない行（VITE_API_URL などのアプリの設定）は、アプリがコンテナ内で .env を直接読むので、sigil は扱わない。
func loadSettings(p project.Project, dd dirs) (settings, error) {
	s := settings{
		sigilPath:  filepath.Join(p.Root, ".sigil"),
		envPath:    filepath.Join(p.Root, ".env"),
		secretsPth: filepath.Join(dd.config, "secrets.env"),
	}
	var err error
	// プロジェクトの中のファイルは、リポジトリやコンテナが自由に作れるので、シンボリックリンクを拒否する
	if s.sigil, _, err = config.ReadFile(s.sigilPath, true); err != nil {
		return s, err
	}
	if s.sigil == nil {
		// 改名前の .devbox だけがあると、黙って無視すれば渡すはずの環境変数やポートが渡らず、
		// 気づきにくいので、止めて知らせる
		// 通常のファイルのときだけにする。Jetify Devbox などは .devbox/ というフォルダを作るため
		legacy := filepath.Join(p.Root, ".devbox")
		if fi, err := os.Lstat(legacy); err == nil && fi.Mode().IsRegular() {
			return s, loref("旧き契約の書が残っています", "%s があります。dev は sigil に改名したので、.sigil に名前を変えてください（mv .devbox .sigil）", legacy)
		}
	}
	// 改名前の全体設定だけがあると、WakaTime のキーなどが黙って渡らなくなるので、止めて知らせる
	if _, err := os.Lstat(dd.config); errors.Is(err, fs.ErrNotExist) {
		legacy := filepath.Join(filepath.Dir(dd.config), "devbox")
		if _, err := os.Lstat(legacy); err == nil {
			return s, loref("旧き契約の書が残っています", "改名前の全体設定 %s があります。%s に移してください（mv %s %s、そのうえで中の devbox を config に名前を変える）",
				legacy, dd.config, legacy, dd.config)
		}
	}
	if s.sigil != nil {
		// 承認の画面に出す内容と、記録するハッシュを、同じ読み込みから作る
		if s.projCfg, err = config.Parse(s.sigilPath, bytes.NewReader(s.sigil)); err != nil {
			return s, err
		}
	}
	if s.globalCfg, _, err = config.Load(filepath.Join(dd.config, "config")); err != nil {
		return s, err
	}
	env, _, err := config.ReadFile(s.envPath, true)
	if err != nil {
		return s, err
	}
	if s.secrets, _, err = config.ReadFile(s.secretsPth, false); err != nil {
		return s, err
	}

	listed := map[string]bool{}
	for _, n := range config.Merge(s.projCfg, s.globalCfg).Envs {
		listed[n] = true
	}
	if env != nil {
		entries, err := secret.ParseEnv(s.envPath, env)
		if err != nil {
			return s, err
		}
		for _, e := range entries {
			switch {
			case e.Scheme != secret.Op:
				// アプリの設定。sigil は扱わない（キーチェーンの参照も、いまは扱わない）
			case config.Denied(e.Name):
				s.warnings = append(s.warnings, fmt.Sprintf("注意: .env の %s は渡せない名前なので、禁書庫から読み込みません", e.Name))
			case !listed[e.Name]:
				s.warnings = append(s.warnings, fmt.Sprintf("注意: .env の %s は .sigil にも全体設定にも列挙されていないので、禁書庫から読み込みません", e.Name))
			default:
				s.opEntries = append(s.opEntries, e)
			}
		}
		s.opEnv = secret.RenderEnv(s.opEntries)
	}
	if s.secrets != nil {
		// 全体の secrets.env はホストのファイルだが、制御用の名前は同じ理由で拒否する
		entries, err := secret.ParseEnv(s.secretsPth, s.secrets)
		if err != nil {
			return s, err
		}
		for _, e := range entries {
			if config.Denied(e.Name) {
				return s, loref("禁書庫の目録に、書けない名前があります", "%s: %s は書けません（sigil や docker の動作を変えられるため）", s.secretsPth, e.Name)
			}
		}
	}
	return s, nil
}

const (
	// opTempPrefix は、op run に渡す env ファイルを置く一時フォルダの名前の接頭辞。
	opTempPrefix = "sigil-op-"
	// opTempEnv は、その一時フォルダの場所を、起動し直した側に伝える環境変数（消すため）。
	// .sigil / .env では渡せない名前にしてある（config.Denied）。
	opTempEnv = "SIGIL_OP_TMP"
)

// needsOp は、op run で読み込むものがあるかを返す。
func (s settings) needsOp() bool {
	return s.opEnv != nil || (s.secrets != nil && secret.HasOpRef(s.secrets))
}

// writeOpFiles は op run に渡すファイルを、プロジェクト → 全体の順で返す。
// op run は後のファイルを優先するので、プロジェクトの .env で全体のシークレットを上書きされないようにするため。
//
// プロジェクトの分は、確かめた組だけで作り直し、本人だけが読める一時フォルダに置く
// （リポジトリやコンテナから差し替えられないように）。tmpDir は、起動し直した側で消す。
func (s settings) writeOpFiles() (files []string, tmpDir string, err error) {
	if s.opEnv != nil {
		// MkdirTemp は 0700 でフォルダを作る
		tmpDir, err = os.MkdirTemp("", opTempPrefix)
		if err != nil {
			return nil, "", err
		}
		p := filepath.Join(tmpDir, "env")
		if err := os.WriteFile(p, s.opEnv, 0o600); err != nil {
			os.RemoveAll(tmpDir)
			return nil, "", err
		}
		files = append(files, p)
	}
	if s.secrets != nil && secret.HasOpRef(s.secrets) {
		files = append(files, s.secretsPth)
	}
	return files, tmpDir, nil
}

// removeOpTemp は、op run に渡すために作った一時フォルダを消す。
// 環境変数で渡された場所なので、自分が作る形（$TMPDIR/sigil-op-*）のときだけ消す。
func removeOpTemp(dir string) {
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == filepath.Clean(os.TempDir()) && strings.HasPrefix(filepath.Base(dir), opTempPrefix) {
		os.RemoveAll(dir)
	}
}

// ensureContainer はコンテナを用意する。op run に置き換えたときは replaced = true。
func ensureContainer(d Deps, u *ui.UI, client docker.Client, p project.Project, home string, dd dirs, args []string) (replaced bool, err error) {
	st, layout, err := client.Inspect(p.ContainerName)
	if err != nil {
		return false, lore("領域の様子を確かめられません", err)
	}
	if st != docker.Missing {
		// 既存のコンテナには、作成時に渡したものが使われる。設定の読み込みや承認はしない。
		return false, useExisting(u, client, p, st, layout)
	}

	s, err := loadSettings(p, dd)
	if err != nil {
		return false, lore("契約の書を読み解けません", err)
	}

	// op run から起動し直された側（inner）では、確認も op run もしない（無限ループ防止）。
	// ただし承認記録の照合は inner でも行う。SIGIL_INNER を外から設定されても、承認を回避できないように。
	_, inner := d.LookupEnv(secret.InnerEnv)
	if !inner {
		for _, w := range s.warnings {
			fmt.Fprintf(d.Stderr, "sigil: %s\n", w)
		}
	}
	// 確認は op run より前に行う。1Password のロックを解除する前に、何を渡すかを見せるため。
	if err := approve(d, approval.Store{Dir: filepath.Join(dd.state, "approved")}, p, s, !inner); err != nil {
		return false, err
	}
	if !inner && s.needsOp() {
		opPath, err := d.LookPath("op")
		if err != nil {
			info(d, "注意: op が見つからないので、禁書庫を開けません。op:// の参照は読み込みません")
		} else {
			self, err := d.Executable()
			if err != nil {
				return false, err
			}
			files, tmpDir, err := s.writeOpFiles()
			if err != nil {
				return false, err
			}
			info(d, "禁書庫から召喚します")
			env := append(d.Environ(), secret.InnerEnv+"=1")
			if tmpDir != "" {
				env = append(env, opTempEnv+"="+tmpDir)
			}
			// op run に置き換える前にも、端末を元に戻す（承認の画面のあとなので、念のため）
			u.Restore()
			if err := d.Exec(opPath, secret.Argv(self, files, args), env); err != nil {
				if tmpDir != "" {
					os.RemoveAll(tmpDir)
				}
				return false, loref("禁書庫を開けませんでした", "op run を起動できませんでした: %w", err)
			}
			return true, nil
		}
	}

	image := imageName(d)
	if !client.ImageExists(image) {
		return false, loref("紋章がありません", "イメージ %s がありません。先に sigil build を実行してください", image)
	}
	_, gitErr := os.Stat(filepath.Join(p.Root, ".git"))
	if gitErr != nil {
		info(d, "注意: .git がありません。リポジトリはホスト側で git init / clone してください")
	}
	nvim, nvimErr := os.Stat(filepath.Join(home, ".config", "nvim"))

	cfg := config.Merge(s.projCfg, s.globalCfg)
	var envs []string
	passWaka := false
	for _, name := range cfg.Envs {
		// 空文字で設定されているものは「設定されている」とみなして渡す
		if _, ok := d.LookupEnv(name); !ok {
			info(d, "注意: 持ち込むもの %s がホスト側で設定されていないので、渡しません", name)
			continue
		}
		envs = append(envs, name)
		if name == "WAKATIME_API_KEY" {
			passWaka = true
		}
	}

	spec := docker.RunSpec{
		Name:          p.ContainerName,
		ProjectRoot:   p.Root,
		ContainerPath: p.ContainerPath,
		Home:          home,
		Image:         image,
		HasGit:        gitErr == nil,
		HasNvimConfig: nvimErr == nil && nvim.IsDir(),
		Envs:          envs,
		Ports:         cfg.Ports,
	}
	if u.Fancy() {
		u.Banner()
	} else {
		info(d, "領域 %s を展開します", p.ContainerName)
	}
	// 段階のうち、最後以外は、ここまでに済んでいる（待ち時間を足さないため、実際の処理に合わせる）
	summon := ui.Stage{Label: "禁書庫から召喚する", Note: "1Password", State: ui.Skipped}
	if inner {
		summon.State = ui.Done
	}
	stages := []ui.Stage{
		{Label: "契約を読み解く", Note: ".sigil / .env", State: ui.Done},
		summon,
		{Label: "禁忌の扉を閉ざす", Note: "~/.ssh ~/.aws docker.sock", State: ui.Done},
		{Label: "権能を剥奪する", Note: "cap-drop=ALL", State: ui.Done},
		{Label: "結界を張る", Note: "no-new-privileges", State: ui.Done},
		{Label: "領域を展開する", Note: p.ContainerName},
	}
	var wakaErr error
	// docker run を裏で動かし、終わるまで魔法陣を描き続ける。docker の標準エラーは、
	// 描画中はためておき、描き終えてから出す（Plain のときはそのまま流れる）。
	err = u.Ritual("⟡ 領域展開", ui.Sanitize(filepath.Base(p.Root)), stages, func(stderr io.Writer) error {
		c := client
		c.Stderr = stderr
		if err := c.Create(spec); err != nil {
			return err
		}
		if passWaka {
			wakaErr = c.SetupWakaTime(p.ContainerName)
		}
		return nil
	})
	if err != nil {
		// cmux でペインを同時に開くと、別のペインが先に同じ名前で作っていることがある。
		// そのときは失敗とせず、そのコンテナを使う。
		if st, layout, ierr := client.Inspect(p.ContainerName); ierr == nil && st != docker.Missing {
			info(d, "別のペインで展開された領域 %s に入ります", p.ContainerName)
			return false, useExisting(u, client, p, st, layout)
		}
		return false, loref("領域を展開できませんでした", "コンテナ %s を作成できませんでした: %w", p.ContainerName, err)
	}
	// wakaErr は Ritual が work の終わりを待ってから戻るので、ここで読んでよい
	if wakaErr != nil {
		info(d, "注意: WakaTime の設定を書けませんでした: %s", wakaErr)
	}
	if u.Fancy() {
		lines := []string{"領域 " + u.Bold(ui.Sanitize(filepath.Base(p.Root))) + " は展開されました"}
		for _, port := range cfg.Ports {
			lines = append(lines, u.Dim("門  127.0.0.1:"+port.Host+" → "+port.Container))
		}
		lines = append(lines, u.Accent("鍵は外界に。コードは領域の中に。"))
		u.Box(lines...)
		u.Entering()
	}
	return false, nil
}

// useExisting は既存のコンテナを使えるようにする。古い layout ならエラー、停止中なら起動する。
// 演出は 1 行だけにし、待ち時間は足さない（停止中なら docker start の間だけスピナーを回す）。
func useExisting(u *ui.UI, client docker.Client, p project.Project, st docker.State, layout string) error {
	if layout != docker.LayoutVersion {
		return loref("旧き紋章の領域です", "%s は古い sigil で作られています。sigil stop してから、もう一度実行してください", p.ContainerName)
	}
	if st == docker.Stopped {
		err := u.Spin("領域を再展開する  "+p.ContainerName, func(stderr io.Writer) error {
			c := client
			c.Stderr = stderr
			return c.Start(p.ContainerName)
		})
		if err != nil {
			return loref("領域を再展開できませんでした", "%s を起動できませんでした: %w", p.ContainerName, err)
		}
	}
	u.Enter(filepath.Base(p.Root))
	return nil
}

// newStdoutUI は、標準出力に飾って書くための UI を返す（sigil ls / help）。
// 標準出力がパイプやリダイレクトなら Plain になり、今までどおりの出力を保つ。
func newStdoutUI(d Deps) *ui.UI {
	mode := ui.DetectMode(d.StdoutIsTerminal(), d.LookupEnv)
	var cols, rows int
	if mode != ui.Plain {
		// 標準エラーだけリダイレクトされていても測れるよう、標準出力の端末で測る
		cols, rows = d.StdoutTermSize()
	}
	return ui.New(d.Stdout, ui.Options{Mode: mode, Cols: cols, Rows: rows})
}

// ls は sigil のコンテナの一覧を出す。標準出力が端末なら、状態ごとに印と色を付ける。
// パイプやリダイレクトでは、今までどおり docker ps の表をそのまま出す（スクリプトで読めるように）。
func ls(d Deps) error {
	out := newStdoutUI(d)
	if !out.Fancy() {
		return d.Runner.Run("docker", docker.LsArgs(), d.Stdout, d.Stderr)
	}
	var buf bytes.Buffer
	if err := d.Runner.Run("docker", docker.LsRowsArgs(), &buf, d.Stderr); err != nil {
		return err
	}
	var realms []ui.Realm
	for _, r := range docker.ParseLs(buf.String()) {
		realms = append(realms, ui.Realm{Name: r.Name, State: r.State, Status: r.Status, Project: r.Project})
	}
	out.Realms(realms)
	return nil
}

// newUI は、標準エラーが端末か、NO_COLOR などの環境変数から、演出のしかたを決める。
func newUI(d Deps) *ui.UI {
	mode := ui.DetectMode(d.StderrIsTerminal(), d.LookupEnv)
	var cols, rows int
	if mode != ui.Plain {
		cols, rows = d.TermSize()
	}
	return ui.New(d.Stderr, ui.Options{
		Mode:  mode,
		Cols:  cols,
		Rows:  rows,
		Greek: lookup(d, ui.GlyphsEnv, "") == "greek",
		After: d.After,
	})
}

// approve は、プロジェクトの .sigil と .env が承認済みかを確かめる。
// 承認済みでなく ask のときは利用者に確認し、承認されたら記録する。ask でなければエラーにする。
// どちらのファイルもなければ、確認は要らない（全体設定はホストのファイルなので確認しない）。
//
// .env については、生の内容ではなく、1Password から読み込む名前と参照の組（opEnv）を対象にする。
// アプリの設定の行を変えただけで、確認し直さなくて済むように。
func approve(d Deps, store approval.Store, p project.Project, s settings, ask bool) error {
	if s.sigil == nil && s.opEnv == nil {
		return nil
	}
	sum := approval.Fingerprint(s.sigil, s.opEnv)
	if store.IsApproved(p.Key, sum) {
		return nil
	}
	var files []string
	if s.sigil != nil {
		files = append(files, s.sigilPath)
	}
	if s.opEnv != nil {
		files = append(files, s.envPath)
	}
	if !ask {
		return loref("契約が結ばれていません", "%s が承認されていません", strings.Join(files, " と "))
	}
	tty, err := d.OpenTTY()
	if err != nil {
		return loref("契約を確かめられません", "%s を確認できない環境です。対話できるターミナルで実行してください", strings.Join(files, " と "))
	}
	defer tty.Close()
	if !approval.Ask(tty, newUI(d), files, s.projCfg, s.opEntries) {
		return lore("契約は結ばれませんでした", errors.New("中止しました"))
	}
	return store.Record(p.Key, sum)
}

// warnLegacyEnv は、改名前の環境変数が設定されていて、新しい名前が設定されていなければ注意を出す。
// 改名前の名前は読まないので、黙っていると、指定したはずのイメージなどが使われず気づきにくいため。
func warnLegacyEnv(d Deps) {
	for _, name := range []string{"IMAGE", "CONTEXT"} {
		oldName, newName := "DEVBOX_"+name, "SIGIL_"+name
		if _, ok := d.LookupEnv(oldName); !ok {
			continue
		}
		if _, ok := d.LookupEnv(newName); !ok {
			info(d, "注意: %s は読みません（sigil に改名しました）。%s を使ってください", oldName, newName)
		}
	}
}

func lookup(d Deps, key, def string) string {
	if v, ok := d.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func imageName(d Deps) string {
	return lookup(d, "SIGIL_IMAGE", "sigil:latest")
}

// buildkitDisabled は、DOCKER_BUILDKIT で BuildKit が止められているかを返す。
// docker はこの値を真偽値（strconv.ParseBool）として読むので、"0" だけでなく "false" なども見る。
// 止められていると --progress が使えず、出力の形も違うので、演出せずにそのまま流す。
func buildkitDisabled(d Deps) bool {
	v, ok := d.LookupEnv("DOCKER_BUILDKIT")
	if !ok {
		return false
	}
	b, err := strconv.ParseBool(v)
	return err == nil && !b
}

// build はイメージを作る。ビルドコンテキストは、バイナリに埋め込んだものを一時フォルダに書き出して使う。
func build(d Deps, u *ui.UI, extra []string) error {
	ctxDir := lookup(d, "SIGIL_CONTEXT", "")
	if ctxDir == "" {
		tmp, err := os.MkdirTemp("", "sigil-build-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err := d.WriteBuildContext(tmp); err != nil {
			return err
		}
		ctxDir = tmp
	}
	image := imageName(d)
	// 演出は、出力の形を sigil が決められるときだけにする。利用者が --progress や -q を
	// 指定したとき、BuildKit を使わないとき（--progress がない）は、今までどおりそのまま流す。
	if !u.Fancy() || docker.HasProgressFlag(extra) || buildkitDisabled(d) {
		args := docker.BuildArgs(image, d.Getuid(), d.Getgid(), extra, ctxDir)
		if err := d.Runner.Run("docker", args, d.Stdout, d.Stderr); err != nil {
			return loref("紋章の鋳造に失敗しました", "docker build に失敗しました: %w", err)
		}
		return nil
	}

	// 出力を --progress=plain にして読み、段階と進み具合を魔法陣の横に出す
	args := docker.BuildArgs(image, d.Getuid(), d.Getgid(), append([]string{"--progress=plain"}, extra...), ctxDir)
	stages := []ui.Stage{
		{Label: "素材を集める", Note: "FROM / ビルドコンテキスト"},
		{Label: "紋章を鋳造する"},
		{Label: "紋章を刻む", Note: image},
	}
	p := &ui.Progress{}
	w := &docker.BuildWatcher{OnProgress: func(phase int, note string, ratio float64) { p.Set(phase, note, ratio) }}
	u.Banner()
	start := time.Now()
	err := u.RitualWithProgress("紋章の鋳造", image, stages, p, func(stderr io.Writer) error {
		if err := d.Runner.Run("docker", args, d.Stdout, w); err != nil {
			// 失敗したときは、docker のログをそのまま見せる（成功したときは捨てる。長いので）
			stderr.Write(w.Log.Bytes())
			return err
		}
		return nil
	})
	if err != nil {
		return loref("紋章の鋳造に失敗しました", "docker build に失敗しました: %w", err)
	}
	u.Success("紋章 " + u.Bold(image) + " の鋳造が完了しました " + u.Dim(fmt.Sprintf("(%.1fs)", time.Since(start).Seconds())))
	return nil
}

func stop(d Deps, client docker.Client, p project.Project) error {
	st, _, err := client.Inspect(p.ContainerName)
	if err != nil {
		return lore("領域の様子を確かめられません", err)
	}
	if st == docker.Missing {
		info(d, "領域 %s はありません", p.ContainerName)
		return nil
	}
	if err := client.Remove(p.ContainerName); err != nil {
		return loref("領域を解除できませんでした", "%s を削除できませんでした: %w", p.ContainerName, err)
	}
	info(d, "領域 %s を解除しました", p.ContainerName)
	return nil
}
