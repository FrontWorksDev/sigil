// Package approval は、プロジェクトの .sigil と、.env で 1Password から読み込むものを
// 利用者に確認し、その結果を記録する。
//
// リポジトリに勝手に置かれた .sigil によって、ホストの環境変数が黙って
// コンテナに渡らないようにするための仕組み。
package approval

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sigil/internal/config"
	"sigil/internal/opwrap"
)

// Sum は content の SHA-256 を 16 進で返す。
func Sum(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// Fingerprint は、承認の対象（.sigil とプロジェクトの .env）のハッシュを返す。
//
// .env も対象にするのは、承認の画面で見せた名前が、どの 1Password 項目から
// 値を取るかを .env で後から差し替えられないようにするため。
// 区切り（"\x00"）と見出しを入れて、.sigil と .env の境目をずらした別の組み合わせが、
// 同じハッシュにならないようにしている。
func Fingerprint(sigil, env []byte) string {
	var b []byte
	b = append(b, "sigil\x00"...)
	b = append(b, sigil...)
	b = append(b, "\x00env\x00"...)
	b = append(b, env...)
	return Sum(b)
}

// Store は承認記録の置き場所（$XDG_STATE_HOME/sigil/approved）。
type Store struct {
	Dir string
}

// IsApproved は、key の記録が sum と一致するかを返す。
// 記録は「ハッシュ + 改行」の形で書くので、前後の空白を無視して比べる（手で編集されても読めるように）。
func (s Store) IsApproved(key, sum string) bool {
	b, err := os.ReadFile(filepath.Join(s.Dir, key))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(b)) == sum
}

// Record は承認を記録する。どのプロジェクトを使っているかが分かるので、本人だけが読めるようにする。
func (s Store) Record(key, sum string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(s.Dir, 0o700); err != nil {
		return err
	}
	p := filepath.Join(s.Dir, key)
	if err := os.WriteFile(p, []byte(sum+"\n"), 0o600); err != nil {
		return err
	}
	return os.Chmod(p, 0o600)
}

// Ask は、.sigil で渡すものを out に表示し、tty から答えを読む。
//
// 標準入力ではなく /dev/tty から読むのは、パイプなどで標準入力に "y" を流し込まれても
// 承認されないようにするため（承認は、端末の前にいる人が行う）。
//
// files は対象のファイル（.sigil / .env）、c は .sigil の内容、refs は .env の内容。
// .env については、名前ごとに 1Password のどの項目から値を取るか（参照）を見せる。値は見せない。
func Ask(tty io.Reader, out io.Writer, files []string, c config.Config, refs []opwrap.Entry) bool {
	fmt.Fprintf(out, "sigil: %s の内容が新しいか、前回の承認から変わっています。\n", strings.Join(files, " と "))
	fmt.Fprintln(out, "     このプロジェクトのコンテナに次のものを渡します:")
	for _, e := range c.Envs {
		fmt.Fprintf(out, "       環境変数  %s\n", e)
	}
	for _, p := range c.Ports {
		fmt.Fprintf(out, "       ポート    %s\n", p.Publish())
	}
	if len(c.Envs) == 0 && len(c.Ports) == 0 {
		fmt.Fprintln(out, "       （なし）")
	}
	if len(refs) > 0 {
		fmt.Fprintln(out, "     .env で、次の名前に値を設定します（1Password の参照先）:")
		for _, r := range refs {
			ref := r.Ref
			if ref == "" {
				ref = "（.env に直接書かれた値）"
			}
			fmt.Fprintf(out, "       %-24s %s\n", r.Name, ref)
		}
	}
	fmt.Fprintln(out, "     このほか、全体設定（~/.config/sigil/config）の分も渡されます。")
	fmt.Fprint(out, "sigil: この設定でよければ y を入力してください [y/N] ")

	line, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.TrimRight(line, "\r\n") {
	case "y", "Y", "yes", "YES":
		return true
	}
	return false
}
