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
	var roots, lexRoots []string
	var missing []string
	for _, r := range in.Roots {
		p, err := resolveDir(r.Path, in.Home)
		if errors.Is(err, errNotExist) {
			// 外付けのディスクが外れているときなど。ほかの ro_root の中の ro まで止めないよう、飛ばす。
			// 許す範囲が狭まるだけなので、安全側。ro がどの ro_root にも入らなければ、下でエラーになる
			missing = append(missing, fmt.Sprintf("%s:%d", in.GlobalPath, r.Line))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s:%d: ro_root %w", in.GlobalPath, r.Line, err)
		}
		if err := CheckSafe(p, home, in.Extra...); err != nil {
			// CheckSafe の文面はプロジェクト向けの言い回しを含まない。直すのは全体設定なので、そう案内する
			return nil, fmt.Errorf("%s:%d: ro_root %w。ro_root には、他人のコードに見せてよいリポジトリの置き場（例: ~/src）を書いてください", in.GlobalPath, r.Line, err)
		}
		roots = append(roots, p)
		// 書かれたまま（展開のみ）の形も、文字列での先の確かめに使う（ホームがシンボリックリンクのときなど）
		raw, _ := expandPath(r.Path, in.Home) // resolveDir で確かめ済み
		lexRoots = append(lexRoots, p, raw)   // 解決したもの、書かれたもの、の組で並べる（resolveMount が組で読む）
	}

	var out []Mount
	for _, r := range in.ReadOnly {
		m, err := resolveMount(r.Path, in.Home, home, roots, lexRoots, in)
		if errors.Is(err, errOutsideRoots) && len(missing) > 0 {
			err = fmt.Errorf("%w。存在しない ro_root（%s）は飛ばしました", err, strings.Join(missing, "、"))
		}
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

var (
	// errNotExist は、resolveDir で、パスが存在しないことを表す（ro_root なら飛ばす）。
	errNotExist = errors.New("存在しません")
	// errOutsideRoots は、ro が、どの ro_root の中にもないことを表す。
	errOutsideRoots = errors.New("ro_root の中にありません（全体設定の ro_root で決めた範囲だけをマウントできます）")
)

// resolveMount は ro の 1 行を確かめる。rawHome は ~/ の展開に、home（解決済み）は CheckSafe に使う。
// lexRoots は、ファイルシステムに触れる前の、文字列での確かめに使う ro_root（解決したものと、書かれたもの）。
func resolveMount(written, rawHome, home string, roots, lexRoots []string, in MountInput) (Mount, error) {
	// .sigil は承認より前に読むので、EvalSymlinks に任せると、/net/<ホスト>/...（macOS の autofs）への
	// シンボリックリンクを、プロジェクトの中やほかのリポジトリ（別のプロジェクトのコンテナが書ける）に置くだけで、
	// 承認の画面より前に外部のホストへ接続させたり、ホストにどんなパスがあるかを探らせたりできてしまう。
	// そこで、ro_root から 1 段ずつたどり、ro_root の外には触れない（walkResolve）
	expanded, err := expandPath(written, rawHome)
	if err != nil {
		return Mount{}, err
	}
	host, err := walkResolve(expanded, lexRoots, in.Project.Root)
	if err != nil {
		return Mount{}, err
	}
	if err := checkPrintable(host); err != nil {
		return Mount{}, err
	}
	fi, err := os.Stat(host)
	if err != nil {
		return Mount{}, fmt.Errorf("%s を確かめられません（%s）", host, fsReason(err))
	}
	if !fi.IsDir() {
		return Mount{}, fmt.Errorf("%s はフォルダではありません", host)
	}
	if !slices.ContainsFunc(roots, func(root string) bool { return within(host, root) }) {
		return Mount{}, fmt.Errorf("%s は %w", host, errOutsideRoots)
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

// maxLinks は、walkResolve がたどるシンボリックリンクの数の上限（循環を止める）。
const maxLinks = 40

// walkResolve は、ro のパス p を、ro_root（解決済み）から 1 段ずつたどって、シンボリックリンクを解決する。
//
// filepath.EvalSymlinks と違い、次のものには触れない（Lstat もしない）:
//   - ro_root の外。リンクの先が外なら、その場で拒否する（/net/<ホスト> などへ接続させない）
//   - プロジェクトの中のシンボリックリンクの先。プロジェクトは信頼できないリポジトリなので、たどらずに拒否する。
//     プロジェクトの中かは within（ファイルの同一性）で判定するので、NFD/NFC や大文字小文字の違いでは抜けられない
//
// lexRoots は「解決したもの、書かれたもの」の組の並び。たどる途中のパスは、すべて解決済みの ro_root の下にある。
func walkResolve(p string, lexRoots []string, projectRoot string) (string, error) {
	cur, rest, ok := rootStart(p, lexRoots)
	if !ok {
		return "", fmt.Errorf("%s は %w", p, errOutsideRoots)
	}
	links := 0
	for len(rest) > 0 {
		c := rest[0]
		rest = rest[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			// cur はシンボリックリンクを含まないので、文字列で親に上がってよい
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		// next には、リンクの先（信頼できない文字列）が入りうるので、%q で出す
		if _, _, ok := rootStart(next, lexRoots); !ok {
			return "", fmt.Errorf("%s のシンボリックリンクの先 %q は %w", p, next, errOutsideRoots)
		}
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s は %w", p, errNotExist)
		}
		if err != nil {
			return "", fmt.Errorf("%s をたどれません（%s）", p, fsReason(err))
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			cur = next
			continue
		}
		if within(cur, projectRoot) {
			return "", fmt.Errorf("%s はプロジェクトの中のシンボリックリンクを通るので、ro には書けません", p)
		}
		if links++; links > maxLinks {
			return "", fmt.Errorf("%s をたどれません（シンボリックリンクが循環しているか、多すぎます）", p)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("%s をたどれません（%s）", p, fsReason(err))
		}
		if !filepath.IsAbs(target) {
			rest = append(splitPath(target), rest...)
			continue
		}
		// 絶対パスの先は、文字列で ro_root の中かを確かめてから、その ro_root からたどり直す。
		// ".." を含むと、文字列での判定と実際の解決が食い違いうるので拒否する
		if slices.Contains(splitPath(target), "..") {
			return "", fmt.Errorf(`%s のシンボリックリンクの先 %q に ".." があるので、たどりません`, p, target)
		}
		root, trest, ok := rootStart(target, lexRoots)
		if !ok {
			return "", fmt.Errorf("%s のシンボリックリンクの先 %q は %w", p, target, errOutsideRoots)
		}
		cur, rest = root, append(trest, rest...)
	}
	return cur, nil
}

// rootStart は、p がどれかの ro_root の中なら、その解決済みの ro_root と、残りの要素を返す。文字列だけで判定する。
func rootStart(p string, lexRoots []string) (string, []string, bool) {
	clean := filepath.Clean(p)
	for i := 0; i+1 < len(lexRoots); i += 2 {
		resolved := filepath.Clean(lexRoots[i])
		for _, spelled := range []string{resolved, filepath.Clean(lexRoots[i+1])} {
			if rest, ok := cutRoot(clean, spelled); ok {
				return resolved, rest, true
			}
		}
	}
	return "", nil, false
}

// cutRoot は、p が base そのものか、その中なら、base より後ろの要素を返す。
// lexWithin は大文字小文字を無視するので、ケルビン記号（U+212A）などでバイト長が違っても一致しうる。
// 区切りの位置がそろうときだけ切る（ずれた位置で切ると panic するか、別のパスになる）。
func cutRoot(p, base string) ([]string, bool) {
	if base == "/" || !lexWithin(p, base) || len(p) < len(base) || (len(p) > len(base) && p[len(base)] != '/') {
		return nil, false
	}
	return splitPath(p[len(base):]), true
}

// splitPath は、パスを "/" で区切った、空でない要素にする。
func splitPath(p string) []string {
	var out []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// lexWithin は、p が base そのものか、その中にあるかを、文字列だけで返す（ファイルシステムに触れない）。
// APFS は大文字小文字を区別しないので、大文字小文字を無視して比べる（広く通すのはこの先の確かめの入口だけで、
// 解決したパスで within による本来の確かめをする）。
func lexWithin(p, base string) bool {
	p, base = filepath.Clean(p), filepath.Clean(base)
	if base == "/" || strings.EqualFold(p, base) {
		return true
	}
	return len(p) > len(base) && strings.EqualFold(p[:len(base)+1], base+"/")
}

// expandPath は、~/ を展開し、.. と表示されない文字を拒否する。ファイルシステムには触れない。
func expandPath(written, rawHome string) (string, error) {
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
	return p, nil
}

// resolveDir は、~/ か / で始まるパスを、シンボリックリンクを解決したフォルダのパスにする。
func resolveDir(written, rawHome string) (string, error) {
	p, err := expandPath(written, rawHome)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%s は %w", p, errNotExist)
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

// blankLooking は、unicode.IsPrint が真で unicode.IsSpace が偽なのに、空白のように見える（何も描かれない）文字。
var blankLooking = map[rune]bool{
	0x115F: true, // HANGUL CHOSEONG FILLER
	0x1160: true, // HANGUL JUNGSEONG FILLER
	0x2800: true, // BRAILLE PATTERN BLANK
	0x3164: true, // HANGUL FILLER
	0xFFA0: true, // HALFWIDTH HANGUL FILLER
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
		case blankLooking[r]:
			return fmt.Errorf("パスに空白に見える文字（U+%04X）があります。承認の画面で普通の空白と見分けられないので使えません: %q", r, p)
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
//
// 作成のあとで守る場所が変わる（~/.config をそのフォルダにリンクした、など）こともあるので、CheckSafe もかけ直す。
// home と extra は CheckSafe に渡す。ro_root は読み直さない（既存のコンテナでは設定を読まない。9 章）。
func VerifyMounts(ms []Mount, home string, extra ...string) error {
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}
	for _, m := range ms {
		// ラベルはコンテナからは書き換えられないが、イメージのラベル（ベースのイメージを含む）を受け継ぐことがある。
		// 信頼できない値として形を確かめ、表示されない文字があればエラーにも載せない（%q で出す）
		if !filepath.IsAbs(m.Host) || filepath.Clean(m.Host) != m.Host || !strings.HasPrefix(m.Container, "/workspace/") ||
			checkPrintable(m.Host) != nil || checkPrintable(m.Container) != nil {
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
		if err := CheckSafe(m.Host, home, extra...); err != nil {
			return err
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
