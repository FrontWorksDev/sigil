package secret

import (
	"reflect"
	"testing"
)

func TestMakePlanTable(t *testing.T) {
	opX := Entry{Name: "X", Ref: "op://v/i/f", Scheme: Op}
	kcX := Entry{Name: "X", Ref: "keychain://proj", Scheme: Keychain}
	gOp := Entry{Name: "X", Ref: "op://g/i/f", Scheme: Op}
	gKc := Entry{Name: "X", Ref: "keychain://glob", Scheme: Keychain}
	gRaw := Entry{Name: "X"}
	listed := map[string]bool{"X": true}

	cases := []struct {
		name            string
		project, global []Entry
		wantOp, wantKc  []Entry
		overridden      bool
	}{
		{"全体 op × プロジェクト keychain", []Entry{kcX}, []Entry{gOp}, nil, nil, true},
		{"全体 keychain × プロジェクト op", []Entry{opX}, []Entry{gKc}, nil, []Entry{gKc}, true},
		{"全体 生の値 × プロジェクト keychain", []Entry{kcX}, []Entry{gRaw}, nil, nil, true},
		{"全体なし × プロジェクト keychain", []Entry{kcX}, nil, nil, []Entry{kcX}, false},
		{"全体なし × プロジェクト op", []Entry{opX}, nil, []Entry{opX}, nil, false},
	}
	for _, c := range cases {
		p := MakePlan(c.project, c.global, listed)
		if !reflect.DeepEqual(p.OpProject, c.wantOp) {
			t.Errorf("%s: OpProject = %v, want %v", c.name, p.OpProject, c.wantOp)
		}
		if !reflect.DeepEqual(p.Keychain, c.wantKc) {
			t.Errorf("%s: Keychain = %v, want %v", c.name, p.Keychain, c.wantKc)
		}
		if p.Overridden["X"] != c.overridden {
			t.Errorf("%s: Overridden[X] = %v, want %v", c.name, p.Overridden["X"], c.overridden)
		}
	}
}

func TestMakePlanGlobalKeychainNeedsListed(t *testing.T) {
	g := []Entry{{Name: "A", Ref: "keychain://a", Scheme: Keychain}, {Name: "B", Ref: "keychain://b", Scheme: Keychain}}
	p := MakePlan(nil, g, map[string]bool{"B": true})
	if want := []Entry{g[1]}; !reflect.DeepEqual(p.Keychain, want) {
		t.Errorf("Keychain = %v, want %v", p.Keychain, want)
	}
}

func TestMakePlanOrder(t *testing.T) {
	proj := []Entry{
		{Name: "P1", Ref: "keychain://p1", Scheme: Keychain},
		{Name: "O1", Ref: "op://v/i/f", Scheme: Op},
		{Name: "P2", Ref: "keychain://p2", Scheme: Keychain},
	}
	g := []Entry{{Name: "G1", Ref: "keychain://g1", Scheme: Keychain}}
	p := MakePlan(proj, g, map[string]bool{"G1": true})
	if len(p.Keychain) != 3 || p.Keychain[0].Name != "P1" || p.Keychain[1].Name != "P2" || p.Keychain[2].Name != "G1" {
		t.Errorf("Keychain = %v", p.Keychain)
	}
	if len(p.OpProject) != 1 || p.OpProject[0].Name != "O1" {
		t.Errorf("OpProject = %v", p.OpProject)
	}
}
