package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	"sigil/internal/config"
	"sigil/internal/secret"
	"sigil/internal/ui"
)

// readKeychain は、plan でキーチェーンから読むと決めた名前の値を、専用のキーチェーンから読む。
//
//   - values は読めた値（名前 → 値）。sigil 自身の環境（os.Setenv）には入れず、
//     docker run のコマンドの環境にだけ渡す（RunSpec.SecretEnv）。
//   - skipped は、全体設定に keychain がないので読まなかった名前。ホストのシェルや
//     inner の環境に同じ名前があっても渡さない（呼ぶ側が判断する）。
//
// 失敗（パスの確かめ、security の失敗・時間切れ、出力の不正）はすべてエラーにし、コンテナを作らない
// （op run も参照を解決できなければ止まるので、それにそろえる）。エラーには値（security の標準出力）を含めない。
func readKeychain(d Deps, s settings, home string) (values map[string]string, skipped map[string]bool, err error) {
	if len(s.plan.Keychain) == 0 {
		// キーチェーンを使わないプロジェクトでは、設定の確かめも解錠も求めない
		return nil, nil, nil
	}
	if s.globalCfg.Keychain == "" {
		skipped = map[string]bool{}
		for _, e := range s.plan.Keychain {
			info(d, "注意: 全体設定に keychain がないので、%s をキーチェーンから読み込みません", e.Name)
			skipped[e.Name] = true
		}
		return nil, skipped, nil
	}
	p, err := config.ExpandHome(s.globalCfg.Keychain, home)
	if err != nil {
		return nil, nil, loref("禁書庫を開けませんでした", "全体設定の keychain: %w", err)
	}
	// security には、確かめたもの（リンクを解いたパス）を渡す。入力の文字列は信用しない
	resolved, err := secret.CheckKeychainPath(p, home, d.Getuid())
	if err != nil {
		return nil, nil, lore("禁書庫を開けませんでした", err)
	}
	values = map[string]string{}
	for _, e := range s.plan.Keychain {
		service := e.Service()
		stdout, stderr, err := d.Security(secret.SecurityArgs(service, resolved))
		if err != nil {
			// 標準出力は値かもしれないので、理由には使わない
			return nil, nil, loref("禁書庫を開けませんでした",
				"キーチェーン %s から %s を読めませんでした（%s）。ロックされている場合は security unlock-keychain %s を実行してください",
				resolved, service, securityFailure(stderr, err), resolved)
		}
		v, err := secret.ParseSecret(stdout)
		if err != nil {
			return nil, nil, loref("禁書庫を開けませんでした", "キーチェーン %s の %s: %w", resolved, service, err)
		}
		values[e.Name] = v
	}
	return values, nil, nil
}

// securityFailure は、security が失敗した理由を、端末に出して安全な短い文にする。
// 標準エラーは制御文字を取り除いてから使う（ESC などで端末の表示を乱されないように）。
func securityFailure(stderr []byte, err error) string {
	if s := secret.CleanStderr(stderr); s != "" {
		// 時間切れまでに何か出ていても、時間切れで止めたことを隠さない
		if errors.Is(err, context.DeadlineExceeded) {
			s += "（時間切れ）"
		}
		return s
	}
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		return fmt.Sprintf("終了コード %d", ee.ExitCode())
	case errors.Is(err, context.DeadlineExceeded):
		return "時間切れ"
	}
	// 起動できなかったときなど。err の文には出力を含めていないが、念のため同じように整える
	return secret.CleanStderr([]byte(err.Error()))
}

// summonStage は、作成時の段階「禁書庫から召喚する」を返す。Note は使った提供元にする。
// inner は op run から起動し直した側（1Password から読んだ）、keychain はキーチェーンから読んだか。
func summonStage(inner, keychain bool) ui.Stage {
	st := ui.Stage{Label: "禁書庫から召喚する", Note: "1Password", State: ui.Done}
	switch {
	case inner && keychain:
		st.Note = "1Password・キーチェーン"
	case keychain:
		st.Note = "キーチェーン"
	case !inner:
		// どちらからも読んでいない
		st.State = ui.Skipped
	}
	return st
}
