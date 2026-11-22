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

// Parse は設定を読む。エラーは "<filename>:<行番号>: <内容>" の形。
func Parse(filename string, r io.Reader) (Config, error) {
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
		default:
			return fail("不明な指定です: %s（env / port のみ）", kind)
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
	c, err := Parse(path, bytes.NewReader(b))
	if err != nil {
		return Config{}, false, err
	}
	return c, true, nil
}
