package main

import (
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 本物の api.github.com には問い合わせない。手元の TLS のテスト用サーバーで確かめる。

func testServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	// 信頼しない証明書のテストで出る TLS のエラーのログを捨てる
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

func TestRateLimitRequest(t *testing.T) {
	var gotAuth, gotPath, gotProto string
	srv, pool := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotProto = r.Proto
		w.Header().Set("GitHub-Authentication-Token-Expiration", "2027-01-01 00:00:00 UTC")
		w.WriteHeader(200)
	})
	status, h, err := newRateLimit(srv.URL+"/rate_limit", pool, 5*time.Second)("github_pat_x")
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 || h.Get("Github-Authentication-Token-Expiration") != "2027-01-01 00:00:00 UTC" {
		t.Errorf("status=%d header=%v", status, h)
	}
	if gotAuth != "Bearer github_pat_x" || gotPath != "/rate_limit" {
		t.Errorf("Authorization=%q path=%q", gotAuth, gotPath)
	}
	if gotProto != "HTTP/1.1" {
		t.Errorf("proto = %s, want HTTP/1.1", gotProto)
	}
}

func TestRateLimitNoRedirect(t *testing.T) {
	followed := false
	srv, pool := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	status, _, err := newRateLimit(srv.URL+"/rate_limit", pool, 5*time.Second)("github_pat_x")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusFound || followed {
		t.Errorf("リダイレクトをたどった: status=%d followed=%v", status, followed)
	}
}

func TestRateLimitUntrustedCert(t *testing.T) {
	srv, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("信頼していない証明書のサーバーに要求が届いた")
	})
	// 空の証明書の集まりでは、つながらない（SSL_CERT_FILE などに頼らない）
	_, _, err := newRateLimit(srv.URL, x509.NewCertPool(), 5*time.Second)("github_pat_x")
	if err == nil {
		t.Error("信頼していない証明書なのにつながった")
	}
}

func TestLoadRoots(t *testing.T) {
	if _, err := loadRoots(filepath.Join(t.TempDir(), "none.crt")); err == nil {
		t.Error("ないファイルで成功した")
	}
	empty := filepath.Join(t.TempDir(), "empty.crt")
	if err := os.WriteFile(empty, []byte("not a cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRoots(empty); err == nil {
		t.Error("証明書のないファイルで成功した")
	}
}
