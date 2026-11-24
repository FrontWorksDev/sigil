package secret

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// SecurityPath は、キーチェーンを読む macOS の標準コマンド。
// PATH 検索にすると、リポジトリや PATH 上の偽物が動くので、絶対パスで呼ぶ。
const SecurityPath = "/usr/bin/security"

// SecurityTimeout は、security の実行の上限。ロック解除のダイアログなどで止まり続けないようにする。
const SecurityTimeout = 120 * time.Second

// MaxSecretSize は、security の標準出力として受け入れる最大のバイト数。
// 想定外の巨大な出力を環境変数に載せないための上限。
const MaxSecretSize = 64 << 10

// CheckKeychainPath は、キーチェーンのパスが「ホスト直下の専用キーチェーン」であることを確かめ、
// リンクを解いたパスを返す。
//
// .env はリポジトリが書けるので、そこから来たパスでログインキーチェーンなどを
// 読まされないよう、解いた結果だけを判断の根拠にする（入力の文字列は信用しない）。
// 条件: 絶対パス、解いた結果が <home>/Library/Keychains の直下、通常のファイル、
// 持ち主が uid、ログインキーチェーンではない。
func CheckKeychainPath(path, home string, uid int) (string, error) {
	// 相対パスは作業フォルダ次第で結果が変わるので受け付けない。
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("キーチェーンのパスは絶対パスで指定してください: %s", path)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("ホームのパスを解決できません: %v", err)
	}
	// EvalSymlinks の結果は絶対パスで、.. も取り除かれている。
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("キーチェーンのパスを解決できません: %s", path)
	}
	// ホーム直下の Library/Keychains の「直下」だけを許す（サブフォルダは不可）。
	if filepath.Dir(resolved) != filepath.Join(resolvedHome, "Library", "Keychains") {
		return "", fmt.Errorf("キーチェーンは ~/Library/Keychains の直下に置いてください: %s", resolved)
	}
	// ログインキーチェーンには、ほかの多くの秘密が入っている。専用のものだけを許す。
	// APFS は大文字小文字を区別せず、EvalSymlinks も大文字小文字を正規化しないので、
	// 小文字にして比べる。パスワードのリセットで残る login_renamed_1.keychain-db のような
	// 古いログインキーチェーンも同じ接頭辞で止める。
	if strings.HasPrefix(strings.ToLower(filepath.Base(resolved)), "login") {
		return "", fmt.Errorf("ログインキーチェーンは使えません。専用のキーチェーンを作ってください: %s", resolved)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("キーチェーンを調べられません: %s", resolved)
	}
	// ハードリンクなど名前では見分けられない場合に備え、本物のログインキーチェーンと同一ファイルか調べる。
	// （確認から使用までの間の差し替えは、同じユーザーのプロセスにしかできず、脅威の範囲外。）
	for _, name := range []string{"login.keychain-db", "login.keychain"} {
		if lfi, err := os.Stat(filepath.Join(resolvedHome, "Library", "Keychains", name)); err == nil && os.SameFile(fi, lfi) {
			return "", fmt.Errorf("ログインキーチェーンは使えません。専用のキーチェーンを作ってください: %s", resolved)
		}
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("キーチェーンが通常のファイルではありません: %s", resolved)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != uid {
		return "", fmt.Errorf("キーチェーンの持ち主が自分ではありません: %s", resolved)
	}
	return resolved, nil
}

// SecurityArgs は、security の argv（argv[0] を含まない）を返す。
// 値は標準出力から受け取るので、argv に載るのはサービス名とパスだけ。
func SecurityArgs(service, keychain string) []string {
	return []string{"find-generic-password", "-s", service, "-w", keychain}
}

// ParseSecret は、security の標準出力から秘密の値を取り出す。
// 末尾の改行は 1 つだけ取り除く（値自身の改行や \r は変えない）。
// エラーには値を含めない。
func ParseSecret(stdout []byte) (string, error) {
	if len(stdout) > MaxSecretSize {
		return "", errors.New("キーチェーンの値が大きすぎます")
	}
	for _, b := range stdout {
		if b == 0 {
			// NUL は環境変数に載せられず、途中で切れて別の値になる。
			return "", errors.New("キーチェーンの値に NUL が含まれています")
		}
	}
	s := string(stdout)
	s = strings.TrimSuffix(s, "\n")
	return s, nil
}

// CleanStderr は、security の標準エラー出力を、端末に出して安全な短い 1 行にする。
// 制御文字や書式文字（ESC、U+202E など）は、端末の表示を乱すので取り除く。
func CleanStderr(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			sb.WriteRune(' ')
		case r == utf8.RuneError || !unicode.IsPrint(r):
			// 表示できない文字は捨てる。
		default:
			sb.WriteRune(r)
		}
	}
	s := strings.TrimSpace(sb.String())
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:200]) + "…"
	}
	return s
}
