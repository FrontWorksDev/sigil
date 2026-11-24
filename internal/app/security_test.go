package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"sigil/internal/secret"
)

// fakeSecurity は、本物の security の代わりに動かすシェルスクリプトを一時フォルダに作る。
// 子プロセスの環境は空（PATH もない）なので、スクリプトの中のコマンドは絶対パスで書く。
func fakeSecurity(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "security")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunSecurityEnvIsMinimal(t *testing.T) {
	t.Setenv("OP_FAKE_SECRET", "s3cret")
	p := fakeSecurity(t, "/usr/bin/env")
	stdout, _, err := runSecurity(p, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stdout), "s3cret") {
		t.Errorf("親の環境が子に渡っている: %q", stdout)
	}
}

func TestRunSecurityDirAndStdin(t *testing.T) {
	p := fakeSecurity(t, "/bin/pwd; /bin/cat")
	stdout, _, err := runSecurity(p, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(stdout) != "/\n" {
		t.Errorf("stdout = %q, want %q", stdout, "/\n")
	}
}

func TestRunSecurityPassesArgs(t *testing.T) {
	p := fakeSecurity(t, `for a in "$@"; do echo "[$a]"; done`)
	stdout, _, err := runSecurity(p, []string{"find-generic-password", "-s", "a b"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := "[find-generic-password]\n[-s]\n[a b]\n"; string(stdout) != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunSecurityTimeout(t *testing.T) {
	p := fakeSecurity(t, "/bin/sleep 5")
	start := time.Now()
	_, _, err := runSecurity(p, nil, 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("戻るまでに %v かかった", d)
	}
}

func TestRunSecurityLimitsStdout(t *testing.T) {
	n := secret.MaxSecretSize + 100
	// head -c で n バイトを出す。上限を超えた分も読み捨てるので、子が詰まらずに終わる
	p := fakeSecurity(t, "/usr/bin/head -c "+strconv.Itoa(n)+" /dev/zero")
	stdout, _, err := runSecurity(p, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(stdout) != secret.MaxSecretSize+1 {
		t.Errorf("len(stdout) = %d, want %d", len(stdout), secret.MaxSecretSize+1)
	}
}

func TestRunSecurityLimitsStderr(t *testing.T) {
	p := fakeSecurity(t, "/usr/bin/head -c 100000 /dev/zero >&2")
	_, stderr, err := runSecurity(p, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(stderr) != 4<<10 {
		t.Errorf("len(stderr) = %d, want %d", len(stderr), 4<<10)
	}
}

func TestRunSecurityNoControllingTTY(t *testing.T) {
	// POSIX の sh は exec のリダイレクトに失敗するとシェルごと終わるので、サブシェルで試す
	p := fakeSecurity(t, "( exec 3</dev/tty ) 2>/dev/null && echo tty || echo notty")
	stdout, _, err := runSecurity(p, nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdout)) != "notty" {
		t.Errorf("stdout = %q, want notty", stdout)
	}
}
