package secret

// Plan は、名前ごとに「どの提供元から読むか」を決めた結果。
type Plan struct {
	OpProject  []Entry         // op run に渡すプロジェクトの組（.env の順）
	Keychain   []Entry         // キーチェーンから読む組（プロジェクト → 全体の順）
	Overridden map[string]bool // 全体の secrets.env に負けたプロジェクトの名前（注意の表示用）
}

// MakePlan は、同じ名前が複数にあるときの優先順位を決める。
//
// 全体の secrets.env は、op://・keychain://・生の値のどれでもプロジェクトの .env に勝つ。
// プロジェクトの .env で全体のシークレットを上書きされないようにするため（op run の順序と同じ）。
//
//   - project は選び終えた組（列挙済み・拒否されていない、Op か Keychain）
//   - global は secrets.env の全行（生の値を含む。値自体は持たない）
//   - listed は、.sigil と全体設定で env として列挙された名前
//
// 全体の Keychain は、列挙された名前だけを読む（列挙していない名前を勝手に渡さない）。
func MakePlan(project, global []Entry, listed map[string]bool) Plan {
	p := Plan{Overridden: map[string]bool{}}
	inGlobal := map[string]bool{}
	for _, g := range global {
		inGlobal[g.Name] = true
	}
	for _, e := range project {
		if inGlobal[e.Name] {
			p.Overridden[e.Name] = true
			continue
		}
		switch e.Scheme {
		case Op:
			p.OpProject = append(p.OpProject, e)
		case Keychain:
			p.Keychain = append(p.Keychain, e)
		}
	}
	for _, g := range global {
		if g.Scheme == Keychain && listed[g.Name] {
			p.Keychain = append(p.Keychain, g)
		}
	}
	return p
}
