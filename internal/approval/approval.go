// Package approval は、プロジェクトの .sigil と、.env で 1Password・キーチェーンから読み込むものを
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

	"github.com/FrontWorksDev/sigil/internal/config"
	"github.com/FrontWorksDev/sigil/internal/project"
	"github.com/FrontWorksDev/sigil/internal/secret"
	"github.com/FrontWorksDev/sigil/internal/ui"
)

// Sum は content の SHA-256 を 16 進で返す。
func Sum(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// Fingerprint は、承認の対象（.sigil とプロジェクトの .env）のハッシュを返す。
//
// .env も対象にするのは、承認の画面で見せた名前が、どの 1Password・キーチェーンの項目から
// 値を取るかを .env で後から差し替えられないようにするため。
// 区切り（"\x00"）と見出しを入れて、.sigil と .env の境目をずらした別の組み合わせが、
// 同じハッシュにならないようにしている。
//
// mounts は、.sigil の ro を解決したもの（20 章）。.sigil の文字列だけでは、承認のあとにシンボリックリンクの
// 向きを変えるだけで別のフォルダをマウントさせられるので、解決したパスとマウント先も含める。
// mounts がなければ今までと同じバイト列にして、既存の承認記録を使えるようにする。
// 区切りの NUL は、.sigil（config が拒否する）にも、パス（project が表示されない文字として拒否する）にも現れない。
func Fingerprint(sigil, env []byte, mounts ...project.Mount) string {
	var b []byte
	b = append(b, "sigil\x00"...)
	b = append(b, sigil...)
	b = append(b, "\x00env\x00"...)
	b = append(b, env...)
	if len(mounts) > 0 {
		b = append(b, "\x00ro\x00"...)
		for _, m := range mounts {
			b = append(b, m.Host...)
			b = append(b, 0)
			b = append(b, m.Container...)
			b = append(b, 0)
		}
	}
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
// files は対象のファイル（.sigil / .env）、c は .sigil の内容、refs は .env から選んだ組、
// mounts は .sigil の ro を解決したもの（解決したホスト側のパスを見せる）。
// .env については、名前ごとに提供元（1Password / キーチェーン）と、どの項目から値を取るか（参照）を
// 見せる。値は見せない。overridden は、全体の secrets.env に負けて使われない名前。
// 使われない組も承認の対象なので隠さず、使わないことが分かるように添える。
//
// セキュリティ上の判断をしてもらう画面なので、演出より読みやすさを優先する。
// 決断の画面には色・枠・記号は付けるが、動きは付けない。用語（契約、持ち込むもの、門、禁書庫）には
// 必ず実際の意味を添える。端末でないとき（Plain）は、飾りのない文面になる（提供元の列と、
// 使わない組の注記を含む。文面は ui の golden テストで固定している）。
func Ask(tty io.Reader, u *ui.UI, files []string, c config.Config, refs []secret.Entry, overridden map[string]bool, mounts []project.Mount) bool {
	v := ui.ContractView{Files: files, Envs: c.Envs}
	for _, m := range mounts {
		v.Mounts = append(v.Mounts, ui.ContractMount{Host: m.Host, Container: m.Container})
	}
	for _, p := range c.Ports {
		v.Ports = append(v.Ports, p.Publish())
	}
	for _, r := range refs {
		v.Refs = append(v.Refs, ui.ContractRef{Name: r.Name, Ref: r.Ref, Source: sourceName(r.Scheme), Overridden: overridden[r.Name]})
	}
	u.ContractScreen(v)
	return ui.IsYes(ui.ReadAnswer(tty))
}

// sourceName は、承認の画面に出す提供元の名前。
func sourceName(s secret.Scheme) string {
	switch s {
	case secret.Op:
		return "1Password"
	case secret.Keychain:
		return "キーチェーン"
	}
	return ""
}
