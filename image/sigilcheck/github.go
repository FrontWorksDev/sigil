package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GitHub のトークン（issue #12）。コンテナに渡したトークンは、プロジェクトのコードからも読めるので、
// 安全さはトークンに許す権限で決まる。ここでは確実に分かることだけを確かめる:
//   - 種類（先頭の文字列）: classic PAT と OAuth のトークンは、対象のリポジトリを絞れない
//   - 期限（API の応答ヘッダー）: 長すぎる・ないときは注意。PAT（fine-grained / classic）だけ
//   - コンテナの中で gh auth login されていないか（hosts.yml のトークン）
// 権限そのもの（Contents の書き込みなど）は、書き込みを試さないと分からないので確かめない
// （点検のために書き込みの API を呼ぶのは危ない）。README の手順で、人の手で一度確かめる。
// トークンの値は表示しない。

var ghTokenNames = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

func githubSection(s *sys) (section, bool) {
	home := s.env("HOME")
	type hostsFile struct{ path, label string }
	files := []hostsFile{{home + "/.config/gh/hosts.yml", "~/.config/gh/hosts.yml"}}
	// gh は GH_CONFIG_DIR があればそこを使うので、そちらも見る
	// （相対パスは、gh と同じく作業フォルダから見た場所になる）
	if d := s.env("GH_CONFIG_DIR"); d != "" && filepath.Clean(d) != filepath.Clean(home+"/.config/gh") {
		p := filepath.Join(d, "hosts.yml")
		files = append(files, hostsFile{p, ext(p)})
	}

	hasToken := false
	for _, n := range ghTokenNames {
		if s.env(n) != "" {
			hasToken = true
		}
	}
	// 在るか、調べられなかった hosts.yml（fail-closed）
	var present []hostsFile
	for _, h := range files {
		if _, err := s.lstat(h.path); !notExist(err) {
			present = append(present, h)
		}
	}
	if !hasToken && len(present) == 0 {
		return section{}, false
	}

	sec := section{title: "外界の鍵", sub: "GitHub のトークン（値は表示しない）"}
	for _, n := range ghTokenNames {
		tok := s.env(n)
		if tok == "" {
			continue
		}
		// GH_TOKEN と同じ値なら、同じトークンを 2 回数えないよう飛ばす（gh は GH_TOKEN を使う）
		if n == "GITHUB_TOKEN" && tok == s.env("GH_TOKEN") {
			sec.items = append(sec.items, item{kind: kindDimNote, text: "GITHUB_TOKEN は GH_TOKEN と同じ値"})
			continue
		}
		sec.items = append(sec.items, checkToken(s, n, tok)...)
	}
	// gh auth login は、すべてのリポジトリに届く OAuth のトークンを hosts.yml に書く。
	// README では GH_TOKEN で渡し、ログインしない手順にしているので、見つけたら失敗にする
	// （~/.config は永続化していないので、残るのはこのコンテナの中だけ）。
	for _, h := range present {
		b, err := s.readFile(h.path)
		switch {
		case err != nil:
			sec.items = append(sec.items, ng(h.label+" を確かめられない"))
		case bytes.Contains(b, []byte("oauth_token")):
			sec.items = append(sec.items, ng("gh auth login のトークンが "+h.label+" にある。gh auth logout してください"))
		default:
			sec.items = append(sec.items, ok("gh auth login のトークンはない"))
		}
	}
	return sec, true
}

func checkToken(s *sys, name, tok string) []item {
	var items []item
	switch {
	case strings.HasPrefix(tok, "github_pat_"):
		items = append(items, ok(name+" は fine-grained PAT"))
	case strings.HasPrefix(tok, "ghp_"):
		items = append(items, ng(name+" は classic PAT（対象のリポジトリを絞れない）。fine-grained PAT にしてください"))
	case strings.HasPrefix(tok, "gho_"):
		return []item{ng(name + " は OAuth のトークン（gh auth login などで作られ、リポジトリを絞れない）。fine-grained PAT にしてください")}
	case strings.HasPrefix(tok, "ghu_"), strings.HasPrefix(tok, "ghs_"):
		// GitHub App のトークンは、届く範囲がアプリの設定しだいで、期限も短い（数時間）ので、期限の案内は出さない
		return []item{warn(name + " は GitHub App のトークン（届く範囲はアプリの設定しだい。fine-grained PAT を勧めます）")}
	default:
		return []item{warn(name + " のトークンの種類が分からない（fine-grained PAT を勧めます）")}
	}
	// GHES 向けの名前は、github.com に問い合わせても意味がないので、種類だけを見る
	if strings.Contains(name, "ENTERPRISE") {
		return items
	}
	status, h, err := s.rateLimit(tok)
	return append(items, expiryItem(name, status, h, err, s.now()))
}

// expiryItem は、/rate_limit の応答から期限の行を作る。
// エラーの中身（err.Error()）と、ヘッダーの生の値は表示しない（トークンや細工した文字列が混じりうる）。
func expiryItem(name string, status int, h http.Header, err error, now time.Time) item {
	if err != nil {
		return warn("GitHub に問い合わせられないので、" + name + " の期限を確かめられない")
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return ng(name + " は GitHub に拒否された（無効か、期限が切れている）")
	default:
		return warn(fmt.Sprintf("GitHub の応答が %d なので、%s の期限を確かめられない", status, name))
	}
	v := h.Get("GitHub-Authentication-Token-Expiration")
	if v == "" {
		return warn(name + " に期限がない。30〜90 日の期限を付けて作り直してください")
	}
	exp, okParse := parseExpiry(v)
	if !okParse {
		return warn(name + " の期限を読み取れない")
	}
	days := (exp.Unix() - now.Unix()) / 86400 // 0 に向かって切り捨てる（bash 版と同じ）
	// コンテナの時刻は UTC なので、GitHub の画面（ブラウザの時刻）と 1 日ずれることがある。UTC と明記する
	day := exp.UTC().Format("2006-01-02") + " UTC"
	switch {
	case !exp.After(now):
		return ng(fmt.Sprintf("%s の期限が切れている（%s）", name, day))
	case days <= 7:
		return warn(fmt.Sprintf("%s の期限まであと %d 日（%s）。そろそろ作り直してください", name, days, day))
	case days > 90:
		return warn(fmt.Sprintf("%s の期限まであと %d 日（%s）。30〜90 日にすると、漏れたときの影響を抑えられます", name, days, day))
	default:
		return ok(fmt.Sprintf("%s の期限まであと %d 日（%s）", name, days, day))
	}
}

// parseExpiry は、期限のヘッダーを読む。GitHub は "2006-01-02 15:04:05 UTC" の形で返す。
// time.Parse の "MST" は知らない略称（JST など）を時差 0 として受け入れてしまうので使わず、
// UTC / GMT と、時差を数字で書く形だけを読む。
func parseExpiry(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", v); err == nil {
		return t, true
	}
	for _, z := range []string{" UTC", " GMT"} {
		if base, found := strings.CutSuffix(v, z); found {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", base, time.UTC); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

const (
	rateLimitURL = "https://api.github.com/rate_limit"
	// イメージ（Debian の ca-certificates）のルート証明書。Go は Linux で SSL_CERT_FILE / SSL_CERT_DIR を
	// 読むので、明示して読み、プロジェクトが渡した証明書で TLS を偽れないようにする。
	caFile    = "/etc/ssl/certs/ca-certificates.crt"
	caMaxSize = 4 << 20
)

var errNoCerts = errors.New("ルート証明書を読めない")

func loadRoots(path string) (*x509.CertPool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, caMaxSize))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b) {
		return nil, errNoCerts
	}
	return pool, nil
}

// newRateLimit は、/rate_limit（回数の制限に数えられない）に問い合わせる関数を作る。
// gh を使わないのは、PATH の偽の gh や、gh が読む環境変数（GH_HOST、GH_DEBUG など）に左右されないため。
func newRateLimit(url string, roots *x509.CertPool, timeout time.Duration) func(string) (int, http.Header, error) {
	client := &http.Client{
		Timeout: timeout,
		// リダイレクトはたどらない（Authorization を別の場所へ送らないため）。3xx は「その他の状態」になる
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			// プロキシには従う。ルート証明書を固定しているので、プロキシは中身を偽れない
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			// HTTP/1.1 に固定する。GODEBUG=http2debug=2 で、リクエストのヘッダー（トークン）が
			// 標準エラーに出るのを防ぐため
			TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
			DisableKeepAlives: true,
		},
	}
	return func(token string) (int, http.Header, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "sigil-check")
		res, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		// 本文は使わない
		res.Body.Close()
		return res.StatusCode, res.Header, nil
	}
}
