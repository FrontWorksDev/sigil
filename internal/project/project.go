// Package project は、カレントディレクトリからプロジェクトを特定し、
// コンテナ名やコンテナ内のパスを決める。
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Project は 1 つのプロジェクトと、それに対応するコンテナの情報。
type Project struct {
	Root          string // ホスト側の最上位フォルダ（シンボリックリンク解決済み）
	Name          string // フォルダ名
	Key           string // Root の SHA-256 の先頭 8 文字。承認記録のファイル名にも使う
	ContainerName string // sigil-<safe>-<key>
	ContainerPath string // /workspace/<フォルダ名>
}

// New は解決済みの絶対パス root から Project を作る。
// key はプロジェクトのフルパスから決まるので、同じフォルダなら、いつも同じコンテナ名・承認記録になる。
func New(root string) Project {
	name := filepath.Base(root)
	sum := sha256.Sum256([]byte(root))
	key := hex.EncodeToString(sum[:])[:8]

	// WakaTime などはフォルダ名からプロジェクト名を判定するので、なるべくそのまま使う。
	// ただし ":" と "," は docker の -v の区切りと衝突するので、その場合だけ安全な名前にする。
	inner := name
	if strings.ContainsAny(name, ":,") {
		inner = SafeName(name)
	}
	return Project{
		Root:          root,
		Name:          name,
		Key:           key,
		ContainerName: "sigil-" + SafeName(name) + "-" + key,
		ContainerPath: "/workspace/" + inner,
	}
}

// SafeName は [A-Za-z0-9_.-] 以外の各バイトを "-" に置き換え、先頭 40 バイトに切り詰める。
// 文字ではなくバイト単位で処理する（もとのシェル版の tr -c / cut -c と同じ結果にしたため）。
func SafeName(name string) string {
	b := []byte(name)
	for i, c := range b {
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.' || c == '-'
		if !ok {
			b[i] = '-'
		}
	}
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}

// ホームの中で、秘密情報や、ホストで実行される設定を含む場所。
// その中や、それを含むフォルダは、コンテナにマウントしない。
//   - ~/.config には 1Password CLI の設定、gh のトークン、sigil の全体設定（承認なしで使われる）がある
//   - ~/Library には Keychain やアプリの認証情報がある
var sensitiveInHome = []string{".ssh", ".aws", ".gnupg", ".docker", ".config", "Library"}

// samePath は a と b が同じ場所かを返す。
// macOS の APFS は大文字小文字を区別しないので、文字列ではなくファイルの同一性で比べる。
// どちらかが存在しなければ、大文字小文字を無視した文字列で比べる。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if fa, err := os.Stat(a); err == nil {
		if fb, err := os.Stat(b); err == nil {
			return os.SameFile(fa, fb)
		}
	}
	return strings.EqualFold(a, b)
}

// within は p が base そのものか、その中にあるかを返す。
func within(p, base string) bool {
	p = filepath.Clean(p)
	for {
		if samePath(p, base) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// CheckSafe は、マウントすると危険な場所を拒否する。
// ホームやその上をマウントすると、~/.ssh や ~/.aws がコンテナから見えてしまう。
// extra は、ほかに守る場所（sigil の状態フォルダなど）。
func CheckSafe(root, home string, extra ...string) error {
	for _, p := range []string{"/", "/Users", "/home", home} {
		if samePath(root, p) {
			return fmt.Errorf("%s 全体はマウントできません。プロジェクトのフォルダで実行してください", root)
		}
	}
	if within(home, root) {
		return fmt.Errorf("%s はホームディレクトリを含むのでマウントできません", root)
	}
	sensitive := append([]string(nil), extra...)
	for _, s := range sensitiveInHome {
		sensitive = append(sensitive, filepath.Join(home, s))
	}
	for _, s := range sensitive {
		if within(root, s) {
			return fmt.Errorf("%s は秘密情報を含む場所（%s）の中なので、マウントできません", root, s)
		}
		if within(s, root) {
			return fmt.Errorf("%s は秘密情報を含む場所（%s）を含むので、マウントできません", root, s)
		}
	}
	return nil
}

// checkNoNestedRepos は、git リポジトリではないフォルダの直下に、別の git リポジトリがないかを確かめる。
// あると、その .git（ホストの git が実行する hooks を含む）が書き込める状態でマウントされてしまう。
func checkNoNestedRepos(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, e.Name(), ".git")); err == nil {
			return fmt.Errorf("%s は git リポジトリではなく、中に別のリポジトリ（%s）があります。リポジトリのフォルダで実行してください", root, e.Name())
		}
	}
	return nil
}

// Resolve は cwd からプロジェクトを特定する。
// gitTop は dir を含む git リポジトリの最上位を返す。失敗したら cwd をプロジェクトにする。
// extra は CheckSafe に渡す、ほかに守る場所。
func Resolve(cwd, home string, extra []string, gitTop func(dir string) (string, error)) (Project, error) {
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return Project{}, err
	}
	// ホームがシンボリックリンク経由でも比べられるように解決する（存在しなければそのまま）
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home = h
	}

	root := cwd
	isRepo := false
	if top, err := gitTop(cwd); err == nil {
		if top = strings.TrimSpace(top); top != "" {
			root, isRepo = top, true
		}
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Project{}, err
	}
	if err := CheckSafe(root, home, extra...); err != nil {
		return Project{}, err
	}
	// docker の -v は ":" で区切るので、ホスト側のパスに ":" があると、別のパスをマウントしたり失敗したりする
	if strings.Contains(root, ":") {
		return Project{}, fmt.Errorf(`%s には ":" が含まれているので、docker にマウントできません。フォルダ名を変えてください`, root)
	}
	if !isRepo {
		if err := checkNoNestedRepos(root); err != nil {
			return Project{}, err
		}
	}
	return New(root), nil
}

// Workdir は、ホストの cwd に対応するコンテナ内の作業フォルダを返す。
// プロジェクトの外や、名前が前方一致するだけの別フォルダ（/src/app に対する /src/application）では、
// プロジェクトの最上位を返す。
func (p Project) Workdir(cwd string) string {
	if cwd == p.Root {
		return p.ContainerPath
	}
	if rest, ok := strings.CutPrefix(cwd, p.Root+"/"); ok {
		return p.ContainerPath + "/" + rest
	}
	return p.ContainerPath
}
