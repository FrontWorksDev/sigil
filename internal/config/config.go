// Package config は、.sigil（プロジェクト）と ~/.config/sigil/config（全体）の設定ファイルを読む。
//
// 設定ファイルはリポジトリの中にあり、悪意のある人が書いたものかもしれない。
// そのため、シェルとして実行（source）はせず、1 行ずつ空白で区切って読むだけにする。
// コマンド置換 $(...) などは、ただの文字列として扱われ、名前の検査で弾かれる。
package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Port は、ホスト側とコンテナ側のポートの組。
type Port struct {
	Host, Container string
}

// Publish は docker run -p に渡す値を返す。
// 外部からつながれないように、必ず 127.0.0.1 にだけ公開する。
func (p Port) Publish() string {
	return "127.0.0.1:" + p.Host + ":" + p.Container
}

// Config は、コンテナに渡す環境変数名とポート。値は持たない。
type Config struct {
	Envs  []string
	Ports []Port
	// Keychain は、シークレットを置く専用キーチェーンのパス（書かれたまま。~/ は展開しない）。
	// 全体設定でだけ書ける。
	Keychain string
	// ReadOnly は、読み取り専用でマウントするフォルダ（書かれた順）。プロジェクトの .sigil でだけ書ける。
	// パスの確かめと解決は project.ResolveMounts で行う（ここではファイルシステムに触れない）。
	ReadOnly []ReadOnly
	// ReadOnlyRoots は、ro でマウントしてよい範囲（全体設定でだけ書ける）。
	// .sigil は信頼できないので、拒否する場所の一覧だけでは防ぎきれない（~/.kube、別のプロジェクトの .env など）。
	// 範囲は、1Password と同じく、ホストの信頼できる側で決める。
	ReadOnlyRoots []ReadOnly
}

// ReadOnly は、.sigil の ro の 1 行。Line はエラーメッセージに行番号を出すために持つ。
type ReadOnly struct {
	Path string // 書かれたまま（~/ は展開しない）
	Line int
}

var (
	envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	portRe    = regexp.MustCompile(`^[0-9]{1,5}(:[0-9]{1,5})?$`)
)

// 渡してはいけない変数。
//   - SSH_AUTH_SOCK: ホストの SSH エージェントを使わせない
//   - PATH / HOME / LD_* / DYLD_*: 実行環境を乗っ取らせない
//
// さらに、これらの名前は .env にも書かせない。op run は .env の行をそのまま
// sigil 自身の環境変数にするので、DOCKER_HOST や XDG_* を書かれると、
// sigil が攻撃者の docker に秘密を送ったり、別の設定を読んだりしてしまう。
var deniedEnvs = map[string]bool{
	"SSH_AUTH_SOCK": true,
	"PATH":          true,
	"HOME":          true,
	// sigil 自身の動作を変える名前。SIGIL_ で始まる名前すべてではなく、sigil が読むものだけを拒否する
	// （利用者が SIGIL_TEST_VAR のような名前を使えるように）。sigil が読む名前を増やしたら、ここにも足すこと。
	"SIGIL":             true, // コンテナ内の目印（イメージの ENV）
	"SIGIL_INNER":       true,
	"SIGIL_IMAGE":       true,
	"SIGIL_CONTEXT":     true,
	"SIGIL_PROJECT_DIR": true,
	"SIGIL_OP_TMP":      true,
	"SIGIL_PLAIN":       true, // 演出を止める（internal/ui）
	"SIGIL_GLYPHS":      true, // 魔法陣の外周の文字（internal/ui）
}

// docker / git / 1Password / 動的リンカ / XDG のフォルダの動作を変えられる名前の接頭辞。
var deniedPrefixes = []string{"DOCKER_", "XDG_", "GIT_", "OP_", "LD_", "DYLD_"}

// Denied は、name をコンテナや sigil に渡してはいけないかを返す。
func Denied(name string) bool {
	if deniedEnvs[name] {
		return true
	}
	for _, p := range deniedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// MaxFileSize は設定ファイルと .env の大きさの上限。
// /dev/zero などへのリンクで、読み込みが終わらなくなるのを防ぐ。
const MaxFileSize = 64 << 10

// ReadFile は設定ファイルを読む。ファイルがなければ (nil, false, nil)。
//
// strict のときは、シンボリックリンクを拒否する。プロジェクトの中のファイルはリポジトリや
// コンテナが自由に作れるので、~/.netrc などへのリンクにされると、ホストの秘密を読んでしまう。
// どちらの場合も、通常のファイル以外（FIFO やデバイス）と、大きすぎるファイルは拒否する。
func ReadFile(path string, strict bool) ([]byte, bool, error) {
	stat := os.Stat
	if strict {
		stat = os.Lstat
	}
	fi, err := stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, false, fmt.Errorf("%s はシンボリックリンクなので読みません。通常のファイルにしてください", path)
	case !fi.Mode().IsRegular():
		return nil, false, fmt.Errorf("%s は通常のファイルではありません", path)
	case fi.Size() > MaxFileSize:
		return nil, false, fmt.Errorf("%s が大きすぎます（上限 %d バイト）", path, MaxFileSize)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	// Stat のあとに差し替えられても上限を超えて読まないようにする
	b, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > MaxFileSize {
		return nil, false, fmt.Errorf("%s が大きすぎます（上限 %d バイト）", path, MaxFileSize)
	}
	return b, true, nil
}

// Parse は全体設定を読む。エラーは "<filename>:<行番号>: <内容>" の形。
func Parse(filename string, r io.Reader) (Config, error) {
	return parse(filename, r, true)
}

// ParseProject はプロジェクトの .sigil を読む。keychain は書けない。
// リポジトリの持ち主に、どのキーチェーンを読むかを変えられてはいけないため。
func ParseProject(filename string, r io.Reader) (Config, error) {
	return parse(filename, r, false)
}

func parse(filename string, r io.Reader, global bool) (Config, error) {
	var c Config
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		text := sc.Text()
		if n == 1 {
			// エディタによっては先頭に UTF-8 の BOM が付くので取り除く
			text = strings.TrimPrefix(text, "\ufeff")
		}
		// 承認のハッシュ（approval.Fingerprint）は NUL で区切るので、NUL を入れて区切りをずらせないようにする。
		// コメントの中も対象（ハッシュには .sigil 全体が入るため）
		if strings.ContainsRune(text, 0) {
			return Config{}, fmt.Errorf("%s:%d: NUL 文字は書けません", filename, n)
		}
		line, _, _ := strings.Cut(text, "#")
		// strings.Fields はタブや CRLF の \r も区切りとして扱う
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		fail := func(format string, a ...any) (Config, error) {
			return Config{}, fmt.Errorf("%s:%d: %s", filename, n, fmt.Sprintf(format, a...))
		}
		switch {
		case len(words) == 1:
			return fail("値がありません: %s", words[0])
		case len(words) > 2:
			return fail("余分な値があります: %s", strings.Join(words, " "))
		}
		kind, val := words[0], words[1]
		switch kind {
		case "env":
			if !envNameRe.MatchString(val) {
				return fail("環境変数名が不正です: %s", val)
			}
			if Denied(val) {
				return fail("%s は渡せません", val)
			}
			c.Envs = append(c.Envs, val)
		case "port":
			if !portRe.MatchString(val) {
				return fail("ポート指定が不正です: %s", val)
			}
			host, cont, ok := strings.Cut(val, ":")
			if !ok {
				cont = host
			}
			// 正規表現だけだと 0 や 70000 が通り、docker で分かりにくい失敗になるので、ここで弾く
			if !validPort(host) || !validPort(cont) {
				return fail("ポート番号は 1〜65535 です: %s", val)
			}
			c.Ports = append(c.Ports, Port{Host: host, Container: cont})
		case "keychain":
			if !global {
				return fail("keychain は全体設定（~/.config/sigil/config）でだけ書けます")
			}
			if c.Keychain != "" {
				return fail("keychain が 2 回書かれています")
			}
			c.Keychain = val
		case "ro":
			// 全体設定に書くと、すべてのプロジェクト（信頼できないものを含む）のコードから
			// そのフォルダが読めてしまうので、プロジェクトの .sigil（承認の対象）でだけ書ける
			if global {
				return fail("ro はプロジェクトの .sigil でだけ書けます")
			}
			c.ReadOnly = append(c.ReadOnly, ReadOnly{Path: val, Line: n})
		case "ro_root":
			if !global {
				return fail("ro_root は全体設定（~/.config/sigil/config）でだけ書けます")
			}
			c.ReadOnlyRoots = append(c.ReadOnlyRoots, ReadOnly{Path: val, Line: n})
		default:
			return fail("不明な指定です: %s（env / port / keychain / ro / ro_root のみ）", kind)
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", filename, err)
	}
	return c, nil
}

func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

// Merge は複数の設定を合わせる。環境変数名は、最初に出た順を保って重複を除く。
func Merge(cs ...Config) Config {
	var out Config
	seen := map[string]bool{}
	for _, c := range cs {
		for _, e := range c.Envs {
			if !seen[e] {
				seen[e] = true
				out.Envs = append(out.Envs, e)
			}
		}
		out.Ports = append(out.Ports, c.Ports...)
		out.ReadOnly = append(out.ReadOnly, c.ReadOnly...)
		out.ReadOnlyRoots = append(out.ReadOnlyRoots, c.ReadOnlyRoots...)
		if out.Keychain == "" {
			out.Keychain = c.Keychain
		}
	}
	return out
}

// Load はホストの設定ファイル（全体設定）を読む。シンボリックリンクはたどる。
// ファイルがなければ (Config{}, false, nil)。
func Load(path string) (Config, bool, error) {
	return load(path, false)
}

// LoadStrict はプロジェクトの設定ファイル（.sigil）を読む。シンボリックリンクは拒否する。
func LoadStrict(path string) (Config, bool, error) {
	return load(path, true)
}

func load(path string, strict bool) (Config, bool, error) {
	b, ok, err := ReadFile(path, strict)
	if err != nil || !ok {
		return Config{}, false, err
	}
	parseFn := Parse
	if strict {
		parseFn = ParseProject
	}
	c, err := parseFn(path, bytes.NewReader(b))
	if err != nil {
		return Config{}, false, err
	}
	return c, true, nil
}

// ExpandHome は、~/x を home 配下に展開する。絶対パスはそのまま返す。
// ~ だけ、~user/x、相対パスは、どこを指すか曖昧（実行場所で変わる）なのでエラーにする。
// 文字列だけで処理し、ファイルシステムには触れない。
func ExpandHome(p, home string) (string, error) {
	switch {
	case strings.HasPrefix(p, "~/"):
		return home + p[1:], nil
	case strings.HasPrefix(p, "/"):
		return p, nil
	}
	return "", fmt.Errorf("パスは / か ~/ で始めてください: %q", p)
}
