package gitcommit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FrontWorksDev/sigil/internal/ui"
)

// Repo は点検したリポジトリの情報。パスはすべて絶対パス。
type Repo struct {
	Root      string
	GitDir    string
	CommonDir string
	// Branch は refs/heads/<name>。
	Branch string
	// Head はまだコミットがなければ空。
	Head     string
	HooksDir string
}

// Refusal は、安全のためにコミットを拒否する理由。
// Lore は世界観の短い言葉、Msg は理由と、利用者がすべきことを平易に書いたもの。
type Refusal struct {
	Lore string
	Msg  string
}

func (r *Refusal) Error() string { return r.Msg }

func refuse(msg string) *Refusal {
	return &Refusal{Lore: "刻印できません", Msg: msg}
}

// operationMarkers は、進行中の操作を示す GitDir の中のもの。
// commit-tree では、マージの親などを正しく残せない。
var operationMarkers = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"}

// Inspect は、コミットを進めてよいリポジトリかを調べる。
// 作業ツリーの中はコンテナが自由に書き換えられるので、ホストの git が
// そこから設定やフックを読む状況は、先にすべて拒否する。
func Inspect(g Git) (Repo, error) {
	root := g.Root
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	r := Repo{Root: root}

	// 呼び出し側の取り違えを防ぐ。Root が最上位でないと、「.git が Root の直下」という前提が崩れる。
	top, err := g.Out(nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return r, err
	}
	if resolve(strings.TrimSpace(top)) != resolve(root) {
		return r, fmt.Errorf("プロジェクトのフォルダ（%s）が git の最上位ではありません", ui.EscapeLine(root))
	}

	// 1. HEAD がブランチを指していること（まだコミットがなくてもよい）
	br, err := g.Out(nil, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return r, refuse("HEAD がブランチを指していません（detached HEAD）。ブランチに移ってから実行するか、手動でコミットしてください")
	}
	r.Branch = strings.TrimSpace(br)

	gd, err := g.Out(nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return r, err
	}
	r.GitDir = filepath.Clean(strings.TrimSpace(gd))
	cd, err := g.Out(nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return r, err
	}
	r.CommonDir = absFrom(root, strings.TrimSpace(cd))

	// .git が作業ツリーの直下の本物のフォルダであること。gitfile（.git がファイル）や
	// リンクだと、設定の置き場が作業ツリーの中になり、「.git の中の設定は安全」という前提が崩れる。
	// 別の場所の .git や、リンクされた worktree も同じ理由で対象外にする。
	dotGit := filepath.Join(root, ".git")
	fi, err := os.Lstat(dotGit)
	if err != nil || !fi.IsDir() || resolve(r.GitDir) != resolve(dotGit) || resolve(r.CommonDir) != resolve(dotGit) {
		return r, refuse("このリポジトリの形（リンクされた worktree、.git がファイルやリンク、別の場所の .git）には対応していません。手動でコミットしてください")
	}

	if h, err := g.Out(nil, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(h)
	}

	// 2. 作業ツリーの中から読み込まれる設定
	cfg, err := g.Out(nil, "config", "--show-origin", "-z", "--list")
	if err != nil {
		return r, err
	}
	entries := parseConfig(cfg)
	// 見かけのパスと実体のパスの、どちらかが作業ツリーの中（.git の外）なら危険とみなす。
	bad := func(p string) bool {
		return inWorktree(root, dotGit, filepath.Clean(p)) || inWorktree(resolve(root), resolve(dotGit), resolve(p))
	}
	refuseCfg := func(p string) *Refusal {
		return refuse("作業ツリーの中のファイル（" + ui.EscapeLine(relOr(root, p)) +
			"）が git の設定として読み込まれます。コンテナが設定を書き換えると、ホストでコードを実行できてしまうため、中止しました。手動でコミットしてください")
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.origin, "file:") {
			continue
		}
		origin := absFrom(root, strings.TrimPrefix(e.origin, "file:"))
		if bad(origin) {
			return r, refuseCfg(origin)
		}
		// include / includeIf のパスは、ファイルがなくても、のちに作られると読み込まれる。
		if e.key == "include.path" || (strings.HasPrefix(e.key, "includeif.") && strings.HasSuffix(e.key, ".path")) {
			v := e.value
			if strings.HasPrefix(v, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					v = filepath.Join(home, v[2:])
				}
			}
			if !filepath.IsAbs(v) {
				v = filepath.Join(filepath.Dir(origin), v)
			}
			if p := filepath.Clean(v); bad(p) {
				return r, refuseCfg(p)
			}
		}
	}

	// フックの場所。-c core.hooksPath=/dev/null が優先されてしまうので、
	// rev-parse --git-path は使わず、設定ファイルの値を自分で読む。
	// 空の値は git と同じく作業ツリーの最上位。~user/ と %(prefix)/ には対応しない。
	r.HooksDir = filepath.Join(r.CommonDir, "hooks")
	for _, e := range entries {
		if e.key == "core.hookspath" && strings.HasPrefix(e.origin, "file:") {
			v := e.value
			if strings.HasPrefix(v, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					v = filepath.Join(home, v[2:])
				}
			}
			r.HooksDir = absFrom(root, v)
		}
	}

	// 3. 進行中の操作
	for _, m := range operationMarkers {
		if _, err := os.Lstat(filepath.Join(r.GitDir, m)); err == nil {
			return r, refuse("マージ・cherry-pick・revert・rebase のいずれかが進行中です（" + m +
				"）。終わらせてから、または手動でコミットしてください")
		}
	}

	// 4. LFS。.gitattributes の filter=lfs は、コミットに入るパスごとに TakeSnapshot が check-attr で調べる。
	if _, err := os.Lstat(filepath.Join(root, ".lfsconfig")); err == nil {
		return r, lfsRefusal()
	}

	// 5. サブモジュール（gitlink）。HEAD に gitlink があると、スナップショットの git add が
	// その作業ツリーを調べるために、サブモジュールの中で git（status）を動かす。その子の git は
	// 入れ子のリポジトリの設定と .gitattributes を読むが、それはコンテナが書ける場所にある
	// （sub/.git/config を書く、sub/.git の gitfile を差し替える）。filter.<x>.clean などが
	// ホストで実行され、-c の上書きでは防げない（キーの名前をコンテナが決められるため）。
	// 変更がなくても add の前に必ず拒否する。新しく足された gitlink は TakeSnapshot が拒否する。
	if r.Head != "" {
		if p, err := findGitlink(g, r.Head); err != nil {
			return r, err
		} else if p != "" {
			return r, refuse("サブモジュール（gitlink）を含むリポジトリには対応していません。手動でコミットしてください（" +
				ui.EscapeLine(p) + "。サブモジュールの中の設定はコンテナが書き換えられ、ホストの git がそれを読んでしまうため）")
		}
	}
	return r, nil
}

// findGitlink は、commit の tree にある最初の gitlink（mode 160000）のパスを返す。なければ空。
// ls-tree は tree のオブジェクトを読むだけで、作業ツリーやサブモジュールの中には入らない。
// 書式: <mode> SP <type> SP <object> TAB <path>\0
func findGitlink(g Git, commit string) (string, error) {
	out, err := g.Out(nil, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return "", err
	}
	for _, e := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(e, "\t")
		if ok && strings.HasPrefix(meta, "160000 ") {
			return p, nil
		}
	}
	return "", nil
}

func lfsRefusal() *Refusal {
	return refuse("LFS のリポジトリには対応していません。手動でコミット・push してください")
}

// inWorktree は p が root の中で、かつ dotGit の外かを返す（見た目のパスだけで比べる）。
func inWorktree(root, dotGit, p string) bool {
	return lexWithin(root, p) && !lexWithin(dotGit, p)
}

func lexWithin(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type configEntry struct {
	origin, key, value string
}

// parseConfig は config --show-origin -z --list の出力を読む。
// 書式は <origin>\0<key>\n<value>\0 の繰り返し。キーは小文字にそろえる。
func parseConfig(s string) []configEntry {
	toks := strings.Split(s, "\x00")
	var out []configEntry
	for i := 0; i+1 < len(toks); i += 2 {
		k, v, _ := strings.Cut(toks[i+1], "\n")
		out = append(out, configEntry{origin: toks[i], key: strings.ToLower(k), value: v})
	}
	return out
}

func absFrom(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// resolve はリンクを解いた実体のパスを返す（macOS の /var → /private/var など）。
// 存在しないパスは、存在する一番深い親を解いて、残りをつなぐ。
func resolve(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for q := p; ; {
		if e, err := filepath.EvalSymlinks(q); err == nil {
			return filepath.Join(e, rest)
		}
		parent := filepath.Dir(q)
		if parent == q {
			return p
		}
		rest = filepath.Join(filepath.Base(q), rest)
		q = parent
	}
}

func relOr(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return p
}
