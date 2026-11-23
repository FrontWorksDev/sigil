package gitcommit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadMessage(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr error // nil なら成功。errAny なら何かのエラー
	}{
		{"通常", "feat: x\n\nbody\n", "feat: x\n\nbody", nil},
		{"BOM と CRLF", "\ufefffeat: x\r\n\r\nbody\r\n", "feat: x\n\nbody", nil},
		{"制御文字", "fix\x1b[31m: y\x07\n", "fix[31m: y", nil},
		{"タブは残す", "a\tb\n", "a\tb", nil},
		{"DEL と C1", "a\x7fb\u0085c\u009bd\n", "abcd", nil},
		{"# 行", "a\n# keep\n", "a\n# keep", nil},
		{"前後の空行", "\n\n a\n\n", " a", nil},
		{"空白だけの行", "\n \n\t\na\n \n", "a", nil},
		{"空", "\n \n", "", ErrNoMessage},
		{"不正な UTF-8", "\xff", "", errAny},
		{"U+202E", "a‮b", "", errAny},
		{"U+2066", "a⁦b", "", errAny},
		{"U+200F", "a‏b", "", errAny},
		{"U+061C", "a؜b", "", errAny},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), MessageFile)
			if err := os.WriteFile(p, []byte(c.in), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ReadMessage(p)
			switch {
			case c.wantErr == nil:
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if got != c.want {
					t.Errorf("got %q, want %q", got, c.want)
				}
			case c.wantErr == errAny:
				if err == nil {
					t.Fatalf("エラーになるはずが %q を返した", got)
				}
				if errors.Is(err, ErrNoMessage) {
					t.Errorf("ErrNoMessage ではなく別のエラーのはず: %v", err)
				}
			default:
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
			}
		})
	}
}

var errAny = errors.New("any")

func TestReadMessageNoFile(t *testing.T) {
	_, err := ReadMessage(filepath.Join(t.TempDir(), MessageFile))
	if !errors.Is(err, ErrNoMessage) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadMessageSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	const secret = "TOPSECRETCONTENT"
	if err := os.WriteFile(target, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, MessageFile)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := ReadMessage(link)
	if err == nil {
		t.Fatal("シンボリックリンクは拒否されるはず")
	}
	if errors.Is(err, ErrNoMessage) {
		t.Errorf("ErrNoMessage であってはならない: %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("エラーにリンク先の中身が含まれている: %v", err)
	}
}

func TestReadMessageFIFO(t *testing.T) {
	p := filepath.Join(t.TempDir(), MessageFile)
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo できない: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadMessage(p)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO は拒否されるはず")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO で固まった")
	}
}

func TestReadMessageTooLarge(t *testing.T) {
	p := filepath.Join(t.TempDir(), MessageFile)
	if err := os.WriteFile(p, []byte(strings.Repeat("a", MaxMessageSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMessage(p); err == nil {
		t.Fatal("上限超過は拒否されるはず")
	}
	// ちょうど上限なら通る
	if err := os.WriteFile(p, []byte(strings.Repeat("a", MaxMessageSize)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMessage(p); err != nil {
		t.Fatalf("上限ちょうどは通るはず: %v", err)
	}
}

func TestNormalizeTooLarge(t *testing.T) {
	if _, err := Normalize([]byte(strings.Repeat("a", MaxMessageSize+1))); err == nil {
		t.Fatal("Normalize も上限を超えたら拒否するはず")
	}
}
