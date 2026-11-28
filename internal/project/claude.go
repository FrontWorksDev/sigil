package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrClaudeSharedChanged は、VerifyClaudeShared で、神託のフォルダの場所がコンテナを作ったときから変わったことを表す。
var ErrClaudeSharedChanged = errors.New("神託のフォルダの場所が、コンテナを作ったときから変わりました")

// claudeSharedMaxDepth は、神託のフォルダの中をたどる深さの上限。skills（.claude/skills/<名前>/...）が収まる深さにする。
const claudeSharedMaxDepth = 8

// ResolveClaudeShared は、Claude Code の共通の設定（神託）のフォルダ dir を確かめ、
// シンボリックリンクを解決したパスを返す（設計書 22 章「共通の設定」）。dir がなければ ("", nil, nil)。
//
// 返したパスは、全コンテナの /etc/claude-code に読み取り専用でマウントされる。ホストの ~/.claude
// （ホストの Claude Code のログイン情報と履歴）や秘密情報の場所を見せないこと、コンテナから書き換えられる
// 場所でないことを確かめる。warnings は、中にあるハードリンク（dir からの相対パス）。
// sigilConfig は $XDG_CONFIG_HOME/sigil、extra はほかに守る場所（sigil の状態フォルダなど）。
func ResolveClaudeShared(dir, home, sigilConfig string, extra ...string) (resolved string, warnings []string, err error) {
	if err := checkPrintable(dir); err != nil {
		return "", nil, err
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("%s を確かめられません（%s）", dir, fsReason(err))
	}
	// フォルダそのものがリンクなら使わない。ホストの ~/.claude へのリンクで、ホストの Claude Code の
	// 履歴やログイン情報を、全コンテナに見せてしまうため
	if fi.Mode()&fs.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("%s がシンボリックリンクです。実体のフォルダを置いてください", dir)
	}
	if !fi.IsDir() {
		return "", nil, fmt.Errorf("%s がフォルダではありません", dir)
	}
	if err := checkClaudeOwnerMode(fi, dir); err != nil {
		return "", nil, err
	}
	// 上の階層（~/.config など）がリンクのこともあるので、解決した先で守る場所を確かめ、そのパスを docker に渡す
	resolved, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", nil, fmt.Errorf("%s のリンクを解決できません（%s）", dir, fsReason(err))
	}
	if err := checkPrintable(resolved); err != nil {
		return "", nil, err
	}
	if strings.Contains(resolved, ":") {
		// docker の -v は ":" で区切る（5 章）
		return "", nil, fmt.Errorf("%s のパスに \":\" があるので、マウントできません", resolved)
	}
	if err := checkClaudeProtected(resolved, home, sigilConfig, extra, true); err != nil {
		return "", nil, err
	}
	warnings, err = walkClaudeShared(resolved, "", 0)
	if err != nil {
		return "", nil, err
	}
	return resolved, warnings, nil
}

// walkClaudeShared は、神託のフォルダの中を、リンクをたどらずに確かめる。
//
// 中のシンボリックリンクは、コンテナの中で解決されるので、ホストのファイルは見えない。ところが、たとえば
// ~/.claude/CLAUDE.md へのリンクは、コンテナの中では /home/dev/.claude/CLAUDE.md（プロジェクトが書き込める
// ボリューム）を指し、共通の設定のつもりが、そのプロジェクトのコードに中身を決められてしまう。そこで拒否する。
func walkClaudeShared(root, rel string, depth int) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		return nil, fmt.Errorf("神託のフォルダの %q を読めません（%s）", rel, fsReason(err))
	}
	var warnings []string
	for _, e := range entries {
		r := filepath.Join(rel, e.Name())
		p := filepath.Join(root, r)
		fi, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("神託のフォルダの %q を確かめられません（%s）", r, fsReason(err))
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			return nil, fmt.Errorf("神託のフォルダの中にシンボリックリンク %q があります。実体のファイルを置いてください", r)
		case fi.IsDir():
			if depth+1 > claudeSharedMaxDepth {
				return nil, fmt.Errorf("神託のフォルダが深すぎます（%q）", r)
			}
		case !fi.Mode().IsRegular():
			return nil, fmt.Errorf("神託のフォルダの中に、ファイルでもフォルダでもないもの %q があります", r)
		}
		if err := checkClaudeOwnerMode(fi, filepath.Join(root, r)); err != nil {
			return nil, err
		}
		if fi.IsDir() {
			w, err := walkClaudeShared(root, r, depth+1)
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, w...)
			continue
		}
		// ハードリンクは、ほかの場所（ホストの ~/.claude のファイルなど）の中身そのものを見せる。
		// 利用者が意図していることもあるので、エラーにはせず知らせる
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
			warnings = append(warnings, r)
		}
	}
	return warnings, nil
}

// checkClaudeOwnerMode は、持ち主が実行ユーザーで、グループやほかのユーザーが書き込めないことを確かめる。
// ほかのローカルユーザーや、別の UID で動くプロセスに、全コンテナの Claude Code の設定を書き換えさせないため。
func checkClaudeOwnerMode(fi fs.FileInfo, p string) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%q の持ち主が、sigil を実行しているユーザーではありません", p)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%q は、グループかほかのユーザーが書き込めます（chmod go-w してください）", p)
	}
	return nil
}

// checkClaudeProtected は、解決したパスが、ホームそのもの・ホストの ~/.claude・秘密情報の場所でないことを確かめる。
//
// ~/.config の中なら、sigil の設定フォルダ（$XDG_CONFIG_HOME/sigil を、最後の段を除いて解決したもの）の
// 中だけを許す。~/.config/gh（gh のトークン）などを見せないため。最後の段を解決しないのは、
// sigil の設定フォルダそのものを ~/.config/gh へのリンクにした場合も弾くため。
//
// configScope が false なら、~/.config の中の場所の判定を省く（起動の前の確かめ直し用）。
func checkClaudeProtected(resolved, home, sigilConfig string, extra []string, configScope bool) error {
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	if within(home, resolved) {
		return fmt.Errorf("%s はホームディレクトリを含むので、神託として渡せません", resolved)
	}
	protected := append([]string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")}, extra...)
	for _, s := range sensitiveInHome {
		if s == ".config" {
			continue // 下で別に扱う
		}
		protected = append(protected, filepath.Join(home, s))
	}
	for _, s := range protected {
		if r, err := filepath.EvalSymlinks(s); err == nil && r != s {
			protected = append(protected, r)
		}
	}
	for _, s := range protected {
		if within(resolved, s) || within(s, resolved) {
			return fmt.Errorf("%s は守る場所（%s）に重なるので、神託として渡せません", resolved, s)
		}
	}
	if !configScope {
		return nil
	}
	cfg := filepath.Join(home, ".config")
	cfgs := []string{cfg}
	if r, err := filepath.EvalSymlinks(cfg); err == nil && r != cfg {
		cfgs = append(cfgs, r)
	}
	allowed := []string{sigilConfig}
	if r, err := filepath.EvalSymlinks(filepath.Dir(sigilConfig)); err == nil {
		allowed = append(allowed, filepath.Join(r, filepath.Base(sigilConfig)))
	}
	for _, c := range cfgs {
		if within(c, resolved) {
			return fmt.Errorf("%s は %s を含むので、神託として渡せません", resolved, c)
		}
		if !within(resolved, c) {
			continue
		}
		ok := false
		for _, a := range allowed {
			// a 自体がリンクなら、その先は sigil の設定フォルダではない
			if fi, err := os.Lstat(a); err == nil && fi.Mode()&fs.ModeSymlink == 0 && within(resolved, a) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("%s は %s の中の、sigil の設定フォルダの外なので、神託として渡せません", resolved, c)
		}
	}
	return nil
}

// VerifyClaudeShared は、停止中のコンテナを docker start する前に、作成時に確かめた神託のパス
// （ラベル sigil.claude）が、今も同じフォルダを指すかを確かめる（20 章の VerifyMounts と同じ考え方）。
// docker は起動のたびにマウント元を解決し直すので、途中がリンクに差し替えられていれば、
// ホストの ~/.claude などを見せてしまう。中も確かめ直す。
func VerifyClaudeShared(path, home, sigilConfig string, extra ...string) error {
	// ラベルはイメージのものを受け継ぐことがあるので、信頼できない値として形を確かめる
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || checkPrintable(path) != nil {
		return fmt.Errorf("神託のマウントの記録が不正です: %q", path)
	}
	if strings.Contains(path, ":") {
		// 作成時と同じ（docker の -v は ":" で区切る）
		return fmt.Errorf("%s のパスに \":\" があるので、マウントできません", path)
	}
	cur := "/"
	for _, c := range splitPath(path) {
		cur = filepath.Join(cur, c)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s は存在しません", path)
		}
		if err != nil {
			return fmt.Errorf("%s をたどれません（%s）", path, fsReason(err))
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			// リンクの先は読まない
			return fmt.Errorf("%w: %s の途中（%s）がシンボリックリンクになっています", ErrClaudeSharedChanged, path, cur)
		}
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s を確かめられません（%s）", path, fsReason(err))
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s はフォルダではありません", ErrClaudeSharedChanged, path)
	}
	if err := checkClaudeOwnerMode(fi, path); err != nil {
		return err
	}
	// ~/.config の中の場所の判定は、作成時の sigil の設定フォルダを基準にしたもの。作ったあとで
	// XDG_CONFIG_HOME を変えると、フォルダが動いていなくても外と判定されるので、ここでは省く。
	// 途中がリンクに替わっていないことは上で 1 段ずつ確かめているので、作成時と同じ場所を指している
	if err := checkClaudeProtected(path, home, sigilConfig, extra, false); err != nil {
		return err
	}
	// 中も確かめ直す。作ったあとで足したリンク（コンテナの中で、プロジェクトが書き込める場所を指すもの）や、
	// 書き込めるファイルを、起動のたびに渡さないため。ハードリンクの注意は、作成時にだけ出す
	_, err = walkClaudeShared(path, "", 0)
	return err
}
