package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"sigil/internal/config"
)

// Mount は、読み取り専用でマウントするフォルダ（設計書 20 章）。
type Mount struct {
	// json の名前は、コンテナのラベル（docker.ReadOnlyLabel）に残る形なので、項目名を変えても変えないこと
	Host      string `json:"host"`      // ホスト側のパス（シンボリックリンク解決済み。docker にはこれを渡す）
	Container string `json:"container"` // /workspace/<名前>
}

// MountInput は ResolveMounts に渡すもの。
type MountInput struct {
	SigilPath  string            // エラーメッセージに出す .sigil のフルパス
	ReadOnly   []config.ReadOnly // .sigil の ro
	GlobalPath string            // エラーメッセージに出す全体設定のフルパス
	Roots      []config.ReadOnly // 全体設定の ro_root
	Project    Project
	Home       string
	Extra      []string // CheckSafe に渡す、ほかに守る場所（全体設定と状態のフォルダ）
}

// ResolveMounts は、.sigil の ro を確かめ、マウントするフォルダを返す。ro がなければ nil。
//
// .sigil はリポジトリの中にあり、悪意のある人が書いたものかもしれない。拒否する場所の一覧（CheckSafe）だけでは
// ~/.kube や別のプロジェクトの .env などを防ぎきれないので、マウントしてよい範囲を、全体設定（ホストのファイル）の
// ro_root で決め、その中だけを許す。
//
// 結果は承認のハッシュに入れる。.sigil の文字列だけでは、承認のあとにシンボリックリンクの向きを変えるだけで
// 別のフォルダをマウントさせられるため。docker run の直前にも呼び直し、承認したものと比べる（差し替え対策）。
func ResolveMounts(in MountInput) ([]Mount, error) {
	if len(in.ReadOnly) == 0 {
		// ro がなければ ro_root を確かめない（存在しない ro_root で、すべてのプロジェクトが止まらないように）
		return nil, nil
	}
	home := in.Home
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	if len(in.Roots) == 0 {
		r := in.ReadOnly[0]
		// XDG_CONFIG_HOME で場所が変わるので、実際に読む全体設定のパスを案内する
		return nil, fmt.Errorf("%s:%d: ro でマウントしてよい範囲が決まっていません。全体設定（%s）に ro_root <フォルダ> を書いてください", in.SigilPath, r.Line, in.GlobalPath)
	}
	var roots []string
	for _, r := range in.Roots {
		p, err := resolveDir(r.Path, in.Home)
		if err == nil {
			err = CheckSafe(p, home, in.Extra...)
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: ro_root %w", in.GlobalPath, r.Line, err)
		}
		roots = append(roots, p)
	}

	var out []Mount
	for _, r := range in.ReadOnly {
		m, err := resolveMount(r.Path, in.Home, home, roots, in)
		if err == nil {
			for _, o := range out {
				if o.Container == m.Container {
					err = fmt.Errorf("マウント先 %s が、ほかの ro と重なります", m.Container)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", in.SigilPath, r.Line, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// resolveMount は ro の 1 行を確かめる。rawHome は ~/ の展開に、home（解決済み）は CheckSafe に使う。
func resolveMount(written, rawHome, home string, roots []string, in MountInput) (Mount, error) {
	host, err := resolveDir(written, rawHome)
	if err != nil {
		return Mount{}, err
	}
	if !slices.ContainsFunc(roots, func(root string) bool { return within(host, root) }) {
		return Mount{}, fmt.Errorf("%s は ro_root の中にありません（全体設定の ro_root で決めた範囲だけをマウントできます）", host)
	}
	// ro_root はすでに CheckSafe を通っているので、その中のフォルダが CheckSafe に掛かることは、通常はない
	// （ro_root が秘密情報を含む場所を含めば、ro_root の時点で拒否される）。それでも、ro_root の判定に
	// 不具合があったときのための多重の守りとして、マウントするフォルダそのものにもかける。
	if err := CheckSafe(host, home, in.Extra...); err != nil {
		return Mount{}, err
	}
	// docker の -v は ":" で区切るので、ホスト側のパスに ":" があると、別のパスをマウントしたり失敗したりする
	if strings.Contains(host, ":") {
		return Mount{}, fmt.Errorf(`%s には ":" が含まれているので、docker にマウントできません`, host)
	}
	// プロジェクトはすでに書き込める状態でマウントされている。その中を ro の元にすると、
	// コンテナが書き換えられる場所を指すことになるので拒否する
	if within(host, in.Project.Root) {
		return Mount{}, fmt.Errorf("%s はプロジェクトの中なので、ro には書けません（すでにマウントされています）", host)
	}

	// マウント先の名前は、シンボリックリンクの先ではなく、利用者が書いた名前にする
	expanded, _ := config.ExpandHome(written, rawHome) // resolveDir で確かめ済み
	name := filepath.Base(filepath.Clean(expanded))
	if name == "" || name == "." || name == ".." || name == "/" {
		return Mount{}, fmt.Errorf("%s からマウント先の名前を決められません", written)
	}
	// プロジェクトと同じく、":" と "," だけを安全な名前にする（WakaTime などがフォルダ名で見分けるため）
	if strings.ContainsAny(name, ":,") {
		name = SafeName(name)
	}
	m := Mount{Host: host, Container: "/workspace/" + name}
	if m.Container == in.Project.ContainerPath {
		return Mount{}, fmt.Errorf("マウント先 %s が、プロジェクトと重なります", m.Container)
	}
	return m, nil
}

// resolveDir は、~/ か / で始まるパスを、シンボリックリンクを解決したフォルダのパスにする。
func resolveDir(written, rawHome string) (string, error) {
	p, err := config.ExpandHome(written, rawHome)
	if err != nil {
		return "", err
	}
	// Clean で消える前に確かめる。.. で、書かれた見た目と違う場所を指させないため
	if slices.Contains(strings.Split(p, "/"), "..") {
		return "", fmt.Errorf(`パスに ".." は書けません: %q`, written)
	}
	// 承認の画面を偽装させないため、表示されない文字（制御文字、U+202E などの書式文字）を拒否する
	if err := checkPrintable(p); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s は存在しません", p)
	}
	if err != nil {
		return "", fmt.Errorf("%s をたどれません（%s）", p, fsReason(err))
	}
	if err := checkPrintable(resolved); err != nil {
		return "", err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%s を確かめられません（%s）", resolved, fsReason(err))
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s はフォルダではありません", resolved)
	}
	return resolved, nil
}

// checkPrintable は、表示されない文字と、ASCII 以外の空白を拒否する。
// 全角空白（U+3000）や NBSP（U+00A0）も拒否するのは、承認の画面で普通の空白と見分けられず、
// 別のフォルダと見誤らせうるため（unicode.IsPrint は ASCII の空白だけを表示できる文字とする）。
func checkPrintable(p string) error {
	if !utf8.ValidString(p) {
		return fmt.Errorf("パスに不正な文字があります: %q", p)
	}
	for _, r := range p {
		switch {
		case r != ' ' && unicode.IsSpace(r):
			return fmt.Errorf("パスに ASCII 以外の空白（U+%04X）があります。承認の画面で普通の空白と見分けられないので使えません: %q", r, p)
		case !unicode.IsPrint(r):
			return fmt.Errorf("パスに表示されない文字（U+%04X）があります: %q", r, p)
		}
	}
	return nil
}

// ErrMountChanged は、VerifyMounts で、パスが作成時と違う場所を指すようになったことを表す。
var ErrMountChanged = errors.New("ro のフォルダの場所が、コンテナを作ったときから変わりました")

// VerifyMounts は、停止中のコンテナを docker start する前に、作成時に承認したマウント（ラベルに残したもの）の
// ホスト側のパスが、今も同じフォルダを指すかを確かめる。
//
// docker は起動のたびにマウント元のパスを解決し直す。作成時のパスはシンボリックリンクを解決済みなので、
// 途中のフォルダがシンボリックリンクに差し替えられていれば、解決し直した結果が変わる。
// 差し替えには、別のプロジェクトのコンテナが書き込めるフォルダなどが使われうる（20 章）。
func VerifyMounts(ms []Mount) error {
	for _, m := range ms {
		// ラベルはコンテナからは書き換えられないが、念のため形を確かめる
		if !filepath.IsAbs(m.Host) || filepath.Clean(m.Host) != m.Host || !strings.HasPrefix(m.Container, "/workspace/") {
			return fmt.Errorf("ro のマウントの記録が不正です: %q → %q", m.Host, m.Container)
		}
		resolved, err := filepath.EvalSymlinks(m.Host)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s は存在しません", m.Host)
		}
		if err != nil {
			return fmt.Errorf("%s をたどれません（%s）", m.Host, fsReason(err))
		}
		if resolved != m.Host {
			// 差し替えた先は、ほかのコンテナが決められる。端末の制御文字を送り込まれないよう、%q で出す
			return fmt.Errorf("%w: %s → %q", ErrMountChanged, m.Host, resolved)
		}
		fi, err := os.Stat(resolved)
		if err != nil {
			return fmt.Errorf("%s を確かめられません（%s）", m.Host, fsReason(err))
		}
		if !fi.IsDir() {
			return fmt.Errorf("%w: %s はフォルダではありません", ErrMountChanged, m.Host)
		}
	}
	return nil
}

// fsReason は、ファイルシステムのエラーの理由だけを日本語で返す。
//
// EvalSymlinks や os.Stat のエラーには、シンボリックリンクの先から作られたパスが入る。リンクの先は、
// 別のプロジェクトのコンテナなどが決められるので、そのまま出すと端末の制御文字を送り込まれうる。
// そこでパスは出さず（呼ぶ側が、確かめ済みのパスを出す）、理由だけを返す。
func fsReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "権限がありません"
	case errors.Is(err, syscall.ENOTDIR):
		return "途中に、フォルダではないものがあります"
	case errors.Is(err, syscall.ELOOP), strings.Contains(err.Error(), "too many links"):
		// filepath.EvalSymlinks は、循環を errno ではなく独自のエラー（too many links）で返す
		return "シンボリックリンクが循環しているか、多すぎます"
	case errors.Is(err, syscall.ENAMETOOLONG):
		return "パスが長すぎます"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("エラー番号 %d", int(errno))
	}
	return "原因の分からないエラー"
}
