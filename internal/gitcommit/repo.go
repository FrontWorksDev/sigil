package gitcommit

import (
	"os"
	"path/filepath"
	"strings"

	"sigil/internal/ui"
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

	if h, err := g.Out(nil, "rev-parse", "-q", "--verify", "HEAD"); err == nil {
		r.Head = strings.TrimSpace(h)
	}

	// 2. 作業ツリーの中から読み込まれる設定
	cfg, err := g.Out(nil, "config", "--show-origin", "-z", "--list")
	if err != nil {
		return r, err
	}
	entries := parseConfig(cfg)
	for _, e := range entries {
		if !strings.HasPrefix(e.origin, "file:") {
			continue
		}
		p := absFrom(root, strings.TrimPrefix(e.origin, "file:"))
		if within(root, p) && !within(r.GitDir, p) && !within(r.CommonDir, p) {
			return r, refuse("作業ツリーの中のファイル（" + ui.EscapeLine(relOr(root, p)) +
				"）が git の設定として読み込まれています。コンテナが設定を書き換えると、ホストでコードを実行できてしまうため、中止しました。手動でコミットしてください")
		}
	}

	// フックの場所。-c core.hooksPath=/dev/null が優先されてしまうので、
	// rev-parse --git-path は使わず、設定ファイルの値を自分で読む。
	r.HooksDir = filepath.Join(r.CommonDir, "hooks")
	for _, e := range entries {
		if e.key == "core.hookspath" && strings.HasPrefix(e.origin, "file:") && e.value != "" {
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

	// 4. LFS
	lfs := refuse("LFS のリポジトリには対応していません。手動でコミット・push してください")
	if _, err := os.Lstat(filepath.Join(root, ".lfsconfig")); err == nil {
		return r, lfs
	}
	list, err := g.Out(nil, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ":(glob)**/.gitattributes")
	if err != nil {
		return r, err
	}
	for _, rel := range strings.Split(list, "\x00") {
		if rel == "" {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		// リンクの先を読まない。git も、リンクの .gitattributes は使わない。
		if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "filter=lfs") {
			return r, lfs
		}
	}
	return r, nil
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

// within は p が dir の中（dir 自身を含む）かを返す。
// macOS の /var → /private/var のようなリンクの違いで取り違えないよう、実体のパスで比べる。
func within(dir, p string) bool {
	dir, p = resolve(dir), resolve(p)
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolve(p string) string {
	p = filepath.Clean(p)
	if e, err := filepath.EvalSymlinks(p); err == nil {
		return e
	}
	return p
}

func relOr(root, p string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return p
}
