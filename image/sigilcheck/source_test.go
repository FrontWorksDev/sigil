package main

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sigil-check はイメージのビルドで、外からモジュールを取らずにコンパイルする（GOPROXY=off）。
// 標準ライブラリだけを import していること（パスの最初の要素に "." がない）を確かめる。
func TestOnlyStdlibImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			first, _, _ := strings.Cut(p, "/")
			if strings.Contains(first, ".") || p == "C" {
				t.Errorf("%s: 標準ライブラリ以外を import している: %s", name, p)
			}
		}
	}
}

// Dockerfile の ENV で設定する名前は、イメージが決めるもので、ホストから渡されたものではない。
// 「持ち込むもの」に出さないよう、除外リストにすべて入っていることを確かめる（設計書 11 章）。
func TestEnvAllowedCoversDockerfile(t *testing.T) {
	names := dockerfileEnvNames(t, "../Dockerfile")
	if len(names) < 5 {
		t.Fatalf("Dockerfile の ENV を読めていない: %v", names)
	}
	for _, n := range names {
		if !envAllowed(n) {
			t.Errorf("Dockerfile の ENV %s が除外リストにない（check.go の imageEnv に足すこと）", n)
		}
	}
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func dockerfileEnvNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// 行の続き（末尾の \）をつないで、命令ごとにまとめる
	var insts []string
	var cur strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if cur.Len() > 0 && strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cur.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if strings.HasSuffix(trimmed, `\`) {
			cur.WriteString(strings.TrimSuffix(trimmed, `\`) + " ")
			continue
		}
		cur.WriteString(trimmed)
		insts = append(insts, cur.String())
		cur.Reset()
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, inst := range insts {
		fields := strings.Fields(inst)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "ENV") {
			continue
		}
		if !strings.Contains(fields[1], "=") {
			// ENV NAME value の古い形
			names = append(names, fields[1])
			continue
		}
		for _, kv := range fields[1:] {
			name, _, ok := strings.Cut(kv, "=")
			if ok && envNameRe.MatchString(name) {
				names = append(names, name)
			}
		}
	}
	return names
}

func TestDockerfileEnvNamesParser(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Dockerfile")
	src := "FROM x\nENV A=1 \\\n    # comment\n    B=2\nRUN echo C=3\nENV D value with spaces\nenv E=5\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(dockerfileEnvNames(t, p), ",")
	if got != "A,B,D,E" {
		t.Errorf("got %s, want A,B,D,E", got)
	}
}
