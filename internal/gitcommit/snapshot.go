package gitcommit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"sigil/internal/ui"
)

// ErrNothing は、確定した tree が HEAD と同じとき。
var ErrNothing = errors.New("刻むものがありません")

// Change は、HEAD と確定した tree の差分の 1 ファイル分。
type Change struct {
	// Status は 'A' 'M' 'D' 'R' 'T'。
	Status  byte
	Path    string
	OldPath string
	OldMode string
	NewMode string
	NewBlob string
	Added   int
	Deleted int
	Binary  bool
	// Size は新しい blob の大きさ（削除では 0）。
	Size       int64
	LinkTarget string
}

// Snapshot は、確定した tree。利用者に見せるのも、コミットするのも、この tree。
type Snapshot struct {
	Tree    string
	Base    string
	Changes []Change
	TmpDir  string
}

// Cleanup は一時フォルダを消す。
func (s Snapshot) Cleanup() {
	if s.TmpDir != "" {
		_ = os.RemoveAll(s.TmpDir)
	}
}

// TakeSnapshot は、本物の index に触れずに、作業ツリーの今の状態から tree を確定する。
// 確認のあとで git add をすると、そのあいだにコンテナが書いた変更が紛れ込むため、
// 先に tree を作って見せ、その tree をコミットする。
func TakeSnapshot(g Git, r Repo, pathspec []string) (s Snapshot, err error) {
	// 本人だけが読める場所に index を写す（0700。MkdirTemp の既定）。
	tmp, err := os.MkdirTemp("", "sigil-commit-*")
	if err != nil {
		return Snapshot{}, fmt.Errorf("一時フォルダを作れませんでした: %s", ui.Escape(err.Error()))
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmp)
			s = Snapshot{}
		}
	}()

	idx := filepath.Join(tmp, "index")
	// 本物の index がなければ写さない（空の index から始まる）。
	if b, rerr := os.ReadFile(filepath.Join(r.GitDir, "index")); rerr == nil {
		if err = os.WriteFile(idx, b, 0o600); err != nil {
			return Snapshot{}, fmt.Errorf("index を写せませんでした: %s", ui.Escape(err.Error()))
		}
	} else if !os.IsNotExist(rerr) {
		err = fmt.Errorf("index を読めませんでした: %s", ui.Escape(rerr.Error()))
		return Snapshot{}, err
	}
	gi := g.With("GIT_INDEX_FILE=" + idx)

	base := r.Head
	if base == "" {
		// SHA-256 のリポジトリもあるので、空の tree は固定値にせず git に求める。
		var out string
		if out, err = g.Out(strings.NewReader(""), "hash-object", "-t", "tree", "--stdin"); err != nil {
			return Snapshot{}, err
		}
		base = strings.TrimSpace(out)
	} else if _, err = gi.Out(nil, "reset", "-q"); err != nil {
		// HEAD にそろえる。ファイルの状態の記録は残るので、全部を読み直さずに済む。
		return Snapshot{}, err
	}

	if len(pathspec) == 0 {
		pathspec = []string{"."}
	}
	args := append([]string{"add", "-A", "--"}, pathspec...)
	// メッセージのファイルは、利用者に見せていないので、必ず除く。
	args = append(args, ":(exclude,top)"+MessageFile)
	if _, err = gi.Out(nil, args...); err != nil {
		return Snapshot{}, err
	}
	var tree string
	if tree, err = gi.Out(nil, "write-tree"); err != nil {
		return Snapshot{}, err
	}
	tree = strings.TrimSpace(tree)

	raw, err := g.Out(nil, "diff-tree", "-r", "-z", "-M", "--raw", base, tree)
	if err != nil {
		return Snapshot{}, err
	}
	changes, err := parseRaw(raw)
	if err != nil {
		return Snapshot{}, err
	}

	// gitlink（入れ子のリポジトリ）は、のちにホストの git が中の設定を読むきっかけになる。
	for _, c := range changes {
		if c.NewMode == "160000" {
			err = refuse("入れ子の git リポジトリ（" + ui.EscapeLine(c.Path) +
				"）が追加・変更されています。コンテナが作った設定をホストの git が読むおそれがあるため、中止しました。手動でコミットしてください")
			return Snapshot{}, err
		}
	}
	if len(changes) == 0 {
		err = ErrNothing
		return Snapshot{}, err
	}

	num, err := g.Out(nil, "diff-tree", "-r", "-z", "-M", "--numstat", base, tree)
	if err != nil {
		return Snapshot{}, err
	}
	if err = applyNumstat(changes, num); err != nil {
		return Snapshot{}, err
	}
	if err = fillBlobInfo(g, changes); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Tree: tree, Base: base, Changes: changes, TmpDir: tmp}, nil
}

// parseRaw は diff-tree --raw -z の出力を読む。
// 書式: :<旧mode> <新mode> <旧sha> <新sha> <状態>[点数]\0<パス>\0[<新パス>\0]
func parseRaw(s string) ([]Change, error) {
	toks := strings.Split(s, "\x00")
	var out []Change
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t == "" {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(t, ":"))
		if !strings.HasPrefix(t, ":") || len(f) != 5 {
			return nil, errors.New("git diff-tree の出力を読めませんでした")
		}
		c := Change{OldMode: f[0], NewMode: f[1], NewBlob: f[3], Status: f[4][0]}
		if i+1 >= len(toks) {
			return nil, errors.New("git diff-tree の出力が途中で切れています")
		}
		i++
		c.Path = toks[i]
		if c.Status == 'R' || c.Status == 'C' {
			if i+1 >= len(toks) {
				return nil, errors.New("git diff-tree の出力が途中で切れています")
			}
			i++
			c.OldPath, c.Path = c.Path, toks[i]
		}
		out = append(out, c)
	}
	return out, nil
}

// applyNumstat は増減の数を入れる。
// 書式: <追加>\t<削除>\t<パス>\0。改名は <追加>\t<削除>\t\0<旧>\0<新>\0。バイナリは -\t-。
func applyNumstat(cs []Change, s string) error {
	byPath := make(map[string]int, len(cs))
	for i, c := range cs {
		byPath[c.Path] = i
	}
	toks := strings.Split(s, "\x00")
	for i := 0; i < len(toks); i++ {
		if toks[i] == "" {
			continue
		}
		p := strings.SplitN(toks[i], "\t", 3)
		if len(p) != 3 {
			return errors.New("git diff-tree --numstat の出力を読めませんでした")
		}
		path := p[2]
		if path == "" {
			if i+2 >= len(toks) {
				return errors.New("git diff-tree --numstat の出力が途中で切れています")
			}
			path = toks[i+2]
			i += 2
		}
		idx, ok := byPath[path]
		if !ok {
			continue
		}
		if p[0] == "-" && p[1] == "-" {
			cs[idx].Binary = true
			continue
		}
		a, e1 := strconv.Atoi(p[0])
		d, e2 := strconv.Atoi(p[1])
		if e1 != nil || e2 != nil {
			return errors.New("git diff-tree --numstat の数を読めませんでした")
		}
		cs[idx].Added, cs[idx].Deleted = a, d
	}
	return nil
}

// fillBlobInfo は、新しい blob の大きさと、シンボリックリンクのリンク先を入れる。
func fillBlobInfo(g Git, cs []Change) error {
	var ids []string
	var which []int
	for i, c := range cs {
		if c.Status == 'D' {
			continue
		}
		ids = append(ids, c.NewBlob)
		which = append(which, i)
	}
	if len(ids) > 0 {
		out, err := g.Out(strings.NewReader(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch-check")
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) != len(ids) {
			return errors.New("git cat-file の出力を読めませんでした")
		}
		for n, line := range lines {
			f := strings.Fields(line) // <sha> <type> <size>
			if len(f) != 3 {
				return errors.New("git cat-file の出力を読めませんでした")
			}
			size, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				return errors.New("git cat-file の大きさを読めませんでした")
			}
			cs[which[n]].Size = size
		}
	}
	for i := range cs {
		if cs[i].Status != 'D' && cs[i].NewMode == "120000" {
			t, err := g.Out(nil, "cat-file", "blob", cs[i].NewBlob)
			if err != nil {
				return err
			}
			cs[i].LinkTarget = t
		}
	}
	return nil
}
