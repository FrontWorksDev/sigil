package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// 本物の関数のうち、一時フォルダの中だけで確かめられるもの（本物の HOME や /proc には触れない）。

func TestRealReadFileRegular(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hosts.yml")
	if err := os.WriteFile(p, []byte("x: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := realSys().readFile(p)
	if err != nil || string(b) != "x: y\n" {
		t.Errorf("readFile = %q, %v", b, err)
	}
}

func TestRealReadFileFIFODoesNotBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hosts.yml")
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skip("FIFO を作れない:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := realSys().readFile(p)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errNotRegular) {
			t.Errorf("FIFO の err = %v, want errNotRegular", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO を読もうとして止まった")
	}
}

func TestRealTryOpenWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "gitfile")
	if err := os.WriteFile(p, []byte("gitdir: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := realSys()
	if err := s.tryOpenWrite(p); err != nil {
		t.Errorf("書き込めるファイルで err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "gitdir: /x\n" {
		t.Errorf("中身が変わった: %q", b)
	}
	if os.Getuid() != 0 {
		if err := os.Chmod(p, 0o444); err != nil {
			t.Fatal(err)
		}
		if err := s.tryOpenWrite(p); !readOnly(err) {
			t.Errorf("読み取り専用のファイルで err = %v", err)
		}
	}
	// ないファイルは作らない
	missing := filepath.Join(dir, "none")
	if err := s.tryOpenWrite(missing); err == nil {
		t.Error("ないファイルで成功した")
	}
	if _, err := os.Lstat(missing); err == nil {
		t.Error("ないファイルを作った")
	}
	// シンボリックリンクはたどらない
	link := filepath.Join(dir, "link")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if err := s.tryOpenWrite(link); err == nil {
		t.Error("シンボリックリンクをたどった")
	}
}

func TestRealTryWriteLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	if err := realSys().tryWrite(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("一時ファイルが残った: %v", entries)
	}
}

func TestToolEnvHasNoSecrets(t *testing.T) {
	t.Setenv("GH_TOKEN", "github_pat_should_not_pass")
	env := strings.Join(toolEnv(), "\n")
	if strings.Contains(env, "github_pat_") || strings.Contains(env, "TOKEN") {
		t.Errorf("toolEnv にトークンが入っている")
	}
	if !strings.Contains(env, "DISABLE_AUTOUPDATER=1") || !strings.Contains(env, "GOTOOLCHAIN=local") {
		t.Errorf("toolEnv に必要な値がない: %s", env)
	}
}
