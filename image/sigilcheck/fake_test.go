package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// rateResp は、偽の GitHub の応答。
type rateResp struct {
	status int
	header http.Header
	err    error
}

// fake は、テスト用の外の世界。既定では「隔離がすべて効いている」コンテナになる。
// テストは本物の環境変数・ファイル・ネットワーク・コマンドに触れない（設計書 13 章）。
type fake struct {
	env       map[string]string
	lstat     map[string]fs.FileMode // 在るもの（Lstat）。ないものは ENOENT
	lstatErr  map[string]error       // Lstat のエラーを決める
	statDirs  map[string]bool        // Stat でフォルダに見えるもの（リンクをたどる）
	writeErr  map[string]error       // 書き込みの試しの結果。ないものは EROFS
	contents  map[string]string
	readErr   map[string]error
	cmds      map[string]string // 名前 → PATH で見つかる場所
	links     map[string]string // シンボリックリンクの解決
	uid       int
	uname     string
	rate      map[string]rateResp // トークン → 応答
	rateCalls []string
	versions  []string // version を呼んだ場所
	now       time.Time
}

var testNow = time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)

func newFake() *fake {
	return &fake{
		env: map[string]string{
			"HOME":              "/home/dev",
			"PATH":              "/usr/bin:/bin",
			"SIGIL":             "1",
			"SIGIL_PROJECT_DIR": "/work/app",
		},
		lstat:    map[string]fs.FileMode{"/work/app/.git": fs.ModeDir},
		lstatErr: map[string]error{},
		statDirs: map[string]bool{},
		writeErr: map[string]error{},
		contents: map[string]string{
			"/proc/self/status": "Name:\tsigil-check\nCapEff:\t0000000000000000\nNoNewPrivs:\t1\n",
		},
		readErr: map[string]error{},
		cmds:    map[string]string{},
		links:   map[string]string{},
		uid:     1000,
		uname:   "dev",
		rate:    map[string]rateResp{},
		now:     testNow,
	}
}

func (f *fake) sys() *sys {
	return &sys{
		lookupEnv: func(k string) (string, bool) { v, ok := f.env[k]; return v, ok },
		environ: func() []string {
			var out []string
			for k, v := range f.env {
				out = append(out, k+"="+v)
			}
			return out
		},
		lstat: func(p string) (fs.FileMode, error) {
			if err, ok := f.lstatErr[p]; ok {
				return 0, err
			}
			if m, ok := f.lstat[p]; ok {
				return m, nil
			}
			return 0, &fs.PathError{Op: "lstat", Path: p, Err: syscall.ENOENT}
		},
		isDir: func(p string) bool { return f.statDirs[p] },
		tryOpenWrite: func(p string) error {
			// ファイルも、書き込みの試しと同じ表を使う（ないものは EROFS）
			if err, ok := f.writeErr[p]; ok {
				return err
			}
			return &fs.PathError{Op: "open", Path: p, Err: syscall.EROFS}
		},
		tryWrite: func(dir string) error {
			if err, ok := f.writeErr[dir]; ok {
				return err
			}
			return &fs.PathError{Op: "open", Path: dir, Err: syscall.EROFS}
		},
		readFile: func(p string) ([]byte, error) {
			if err, ok := f.readErr[p]; ok {
				return nil, err
			}
			if c, ok := f.contents[p]; ok {
				return []byte(c), nil
			}
			return nil, &fs.PathError{Op: "open", Path: p, Err: syscall.ENOENT}
		},
		lookPath: func(n string) (string, error) {
			if p, ok := f.cmds[n]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		resolve: func(p string) string {
			if r, ok := f.links[p]; ok {
				return r
			}
			return p
		},
		user: func() (int, string) { return f.uid, f.uname },
		rateLimit: func(tok string) (int, http.Header, error) {
			f.rateCalls = append(f.rateCalls, tok)
			r, ok := f.rate[tok]
			if !ok {
				return 0, nil, errors.New("no response")
			}
			return r.status, r.header, r.err
		},
		version: func(path, name string) string {
			f.versions = append(f.versions, path)
			return name + " 1.0"
		},
		now: func() time.Time { return f.now },
	}
}

func expires(v string) http.Header {
	h := http.Header{}
	h.Set("GitHub-Authentication-Token-Expiration", v)
	return h
}

// lines は、判定の結果を「印 本文」の行にして返す（ツールの欄は除く）。
func lines(r report) []string {
	var out []string
	for _, sec := range r {
		if sec.title == "ツール" {
			continue
		}
		out = append(out, "■ "+sec.title)
		for _, it := range sec.items {
			out = append(out, itemLine(it))
		}
	}
	return out
}

func itemLine(it item) string {
	switch it.kind {
	case kindOK:
		return "✓ " + it.text
	case kindNG:
		return "✗ " + it.text
	case kindWarn:
		return "! " + it.text
	case kindNote:
		return "  " + it.text
	case kindDimNote:
		return "~ " + it.text
	case kindTool:
		return fmt.Sprintf("T %s %s %v", it.tool, it.text, it.warn)
	}
	return "? " + it.text
}

func sectionLines(r report, title string) []string {
	for _, sec := range r {
		if sec.title == title {
			var out []string
			for _, it := range sec.items {
				out = append(out, itemLine(it))
			}
			return out
		}
	}
	return nil
}

func hasLine(ls []string, want string) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}

func joinLines(ls []string) string { return strings.Join(ls, "\n") }

func assertLines(t interface {
	Helper()
	Errorf(string, ...any)
}, got, want []string) {
	t.Helper()
	if joinLines(got) != joinLines(want) {
		t.Errorf("got:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
	}
}
