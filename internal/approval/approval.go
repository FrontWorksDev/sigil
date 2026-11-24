// Package approval は、プロジェクトの .sigil と、.env で 1Password から読み込むものを
// 利用者に確認し、その結果を記録する。
//
// リポジトリに勝手に置かれた .sigil によって、ホストの環境変数が黙って
// コンテナに渡らないようにするための仕組み。
package approval

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sigil/internal/config"
	"sigil/internal/secret"
	"sigil/internal/ui"
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

// Ask は、.sigil で渡すものを u に表示し、tty から答えを読む。
//
// 標準入力ではなく /dev/tty から読むのは、パイプなどで標準入力に "y" を流し込まれても
// 承認されないようにするため（承認は、端末の前にいる人が行う）。
//
// files は対象のファイル（.sigil / .env）、c は .sigil の内容、refs は .env の内容。
// .env については、名前ごとに 1Password のどの項目から値を取るか（参照）を見せる。値は見せない。
//
// セキュリティ上の判断をしてもらう画面なので、演出より読みやすさを優先する。
// 決断の画面には色・枠・記号は付けるが、動きは付けない。用語（契約、持ち込むもの、門、禁書庫）には
// 必ず実際の意味を添える。端末でないとき（Plain）は、従来と同じ文面になる。
func Ask(tty io.Reader, u *ui.UI, files []string, c config.Config, refs []secret.Entry) bool {
	v := ui.ContractView{Files: files, Envs: c.Envs}
	for _, p := range c.Ports {
		v.Ports = append(v.Ports, p.Publish())
	}
	for _, r := range refs {
		v.Refs = append(v.Refs, [2]string{r.Name, r.Ref})
	}
	u.ContractScreen(v)
	return ui.IsYes(ui.ReadAnswer(tty))
}
