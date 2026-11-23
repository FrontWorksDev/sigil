// Package gitcommit は sigil commit のうち、ホストの git に触れる部分を持つ。
package gitcommit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"unicode/utf8"
)

const (
	// MessageFile はコンテナの Claude Code が書く銘（コミットメッセージ）のファイル名。
	MessageFile = ".sigil-commit"
	// MaxMessageSize は銘の大きさの上限。
	MaxMessageSize = 64 << 10
)

// ErrNoMessage は、銘のファイルがない、または整形のあとで空だったことを表す。
var ErrNoMessage = errors.New("銘がありません")

// ReadMessage は path の銘を読んで整形する。ファイルがなければ ErrNoMessage。
//
// プロジェクトの中のファイルはコンテナが自由に作れるので、~/.netrc などへのリンクにされると
// ホストの秘密を読んでしまう。O_NOFOLLOW で開いてから Fstat で確かめるので、
// 確かめてから開くまでの差し替えも防げる。O_NONBLOCK は、FIFO に差し替えられても
// open で固まらないため。
func ReadMessage(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoMessage
	}
	if errors.Is(err, syscall.ELOOP) {
		return "", fmt.Errorf("%s はシンボリックリンクなので読みません。通常のファイルにしてください", path)
	}
	if err != nil {
		return "", fmt.Errorf("%s を開けません: %w", path, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("%s を調べられません: %w", path, err)
	}
	switch {
	case !fi.Mode().IsRegular():
		return "", fmt.Errorf("%s は通常のファイルではありません", path)
	case fi.Size() > MaxMessageSize:
		return "", fmt.Errorf("%s が大きすぎます（上限 %d バイト）", path, MaxMessageSize)
	}
	// Fstat のあとに伸ばされても上限を超えて読まない
	b, err := io.ReadAll(io.LimitReader(f, MaxMessageSize+1))
	if err != nil {
		return "", fmt.Errorf("%s を読めません: %w", path, err)
	}
	return Normalize(b)
}

// Normalize は銘を検査して整形する。コンテナのフックが書き換えた銘にも使う。
//
// コミットするのは、整形して画面に出した内容そのもの。画面の見た目とコミットされる
// 文字の並びを変えられないように、双方向の書式文字は拒否し、制御文字は取り除く。
// 整形のあとで空なら ErrNoMessage。
func Normalize(b []byte) (string, error) {
	if len(b) > MaxMessageSize {
		return "", fmt.Errorf("銘が大きすぎます（上限 %d バイト）", MaxMessageSize)
	}
	if !utf8.Valid(b) {
		return "", errors.New("銘が正しい UTF-8 ではありません")
	}
	b = bytes.TrimPrefix(b, []byte("\ufeff"))
	s := string(b)
	for _, r := range s {
		if isBidi(r) {
			return "", fmt.Errorf("銘に双方向の書式文字（U+%04X）があります", r)
		}
	}

	// 改行 \n とタブ \t 以外の制御文字（C0、DEL、C1）と CR を取り除く。
	// ESC を消せば、端末に出したときに画面を書き換える並びが効かなくなる。
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, s)

	// 前後の空行（空白だけの行も）を落とす。行頭・行末の空白はそのまま残す。
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return "", ErrNoMessage
	}
	return strings.Join(lines, "\n"), nil
}

// isBidi は双方向の書式文字か。
func isBidi(r rune) bool {
	switch {
	case r >= 0x202a && r <= 0x202e,
		r >= 0x2066 && r <= 0x2069,
		r == 0x200e, r == 0x200f, r == 0x061c:
		return true
	}
	return false
}
