package docker

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestProjectVolumes(t *testing.T) {
	got := ProjectVolumes("sigil-proj-33bf5961")
	want := []Volume{
		{"sigil-proj-33bf5961-cache", "/home/dev/.cache"},
		{"sigil-proj-33bf5961-go", "/home/dev/go"},
		{"sigil-proj-33bf5961-cargo", "/home/dev/.cargo"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectVolumes = %v, want %v", got, want)
	}
}

// 共有していたボリュームは、もう docker run に渡さない（設計書 21 章）。
// Claude と Neovim のものはフェーズ 2・3 まで共有のまま。
func TestRunArgsPerProjectVolumes(t *testing.T) {
	s := sampleSpec()
	vols := flagValues(RunArgs(s), "-v")
	for _, v := range vols {
		for _, shared := range []string{"sigil-cache:", "sigil-go:", "sigil-cargo:"} {
			if strings.HasPrefix(v, shared) {
				t.Errorf("共有のボリュームを渡している: %s", v)
			}
		}
	}
	for _, want := range []string{
		s.Name + "-cache:/home/dev/.cache",
		s.Name + "-go:/home/dev/go",
		s.Name + "-cargo:/home/dev/.cargo",
		"sigil-nvim-data:/home/dev/.local/share/nvim",
		"sigil-nvim-state:/home/dev/.local/state/nvim",
		"sigil-claude:/home/dev/.claude",
	} {
		if !slices.Contains(vols, want) {
			t.Errorf("%s がない: %v", want, vols)
		}
	}
}

func TestRunArgsLayout3(t *testing.T) {
	if !slices.Contains(flagValues(RunArgs(sampleSpec()), "--label"), "sigil.layout=3") {
		t.Error("sigil.layout=3 がない")
	}
}

// name= の絞り込みは部分一致なので、名前が完全に一致する行だけを「ある」とする。
func TestVolumeExistsExactName(t *testing.T) {
	const name = "sigil-proj-33bf5961-cache"
	for _, tc := range []struct {
		out  string
		want bool
	}{
		{"", false},
		{name + "-old\n", false},
		{"x-" + name + "\n", false},
		{"x\n" + name + "\n", true},
		{name, true},
	} {
		f := &fakeRunner{responses: []response{{stdout: tc.out}}}
		got, err := Client{R: f}.VolumeExists(name)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("出力 %q: %v, want %v", tc.out, got, tc.want)
		}
		wantArgs := []string{"volume", "ls", "--filter", "name=" + name, "--format", "{{.Name}}"}
		if !reflect.DeepEqual(f.calls[0].args, wantArgs) {
			t.Errorf("args = %v", f.calls[0].args)
		}
	}
}

// docker が使えないときに「ない」として作りに行かない（Inspect と同じ方針）。
func TestVolumeExistsDockerDown(t *testing.T) {
	f := &fakeRunner{responses: []response{{stdout: "\n", err: errors.New("exit status 1")}}}
	_, err := Client{R: f}.VolumeExists("v")
	if err == nil || !strings.Contains(err.Error(), "docker に接続できません") {
		t.Errorf("err = %v", err)
	}
}

// ラベルは余白を取り除かずに返す。末尾が空白・改行のパスを、それがないパスと取り違えないため。
func TestVolumeProjectExactBytes(t *testing.T) {
	for _, tc := range []struct {
		out     string
		want    string
		wantOK  bool
		wantErr bool
	}{
		{`{"sigil.project":"/p/a "}` + "\n", "/p/a ", true, false},
		{`{"sigil.project":"/p/a\n"}` + "\n", "/p/a\n", true, false},
		{`{"sigil.project":"/p/a"}`, "/p/a", true, false},
		{"null\n", "", false, false},
		{"{}\n", "", false, false},
		{`{"other":"/p/a"}`, "", false, false},
		{"not json", "", false, true},
	} {
		f := &fakeRunner{responses: []response{{stdout: tc.out}}}
		got, ok, err := Client{R: f}.VolumeProject("v")
		if (err != nil) != tc.wantErr {
			t.Errorf("出力 %q: err = %v", tc.out, err)
			continue
		}
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("出力 %q: (%q, %v), want (%q, %v)", tc.out, got, ok, tc.want, tc.wantOK)
		}
		wantArgs := []string{"volume", "inspect", "--format", "{{json .Labels}}", "v"}
		if !reflect.DeepEqual(f.calls[0].args, wantArgs) {
			t.Errorf("args = %v", f.calls[0].args)
		}
	}
}

func TestVolumeProjectDockerError(t *testing.T) {
	f := &fakeRunner{responses: []response{{err: errors.New("exit status 1")}}}
	if _, _, err := (Client{R: f}).VolumeProject("v"); err == nil {
		t.Error("エラーにならない")
	}
}

// パスにカンマや = があっても、ラベルは argv の 1 要素として渡る（--mount の CSV を通さない）。
func TestCreateVolumeArgs(t *testing.T) {
	f := &fakeRunner{}
	root := "/x/a,dst=/etc b=c\t日本"
	if err := (Client{R: f}).CreateVolume("sigil-p-1-cache", root); err != nil {
		t.Fatal(err)
	}
	want := []string{"volume", "create", "--label", "sigil.project=" + root, "sigil-p-1-cache"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("args = %q", f.calls[0].args)
	}
}

// -f を付けない。ほかのペインがコンテナを作り直して使用中なら、docker が断るので消さずに済む。
func TestRemoveVolumesNoForce(t *testing.T) {
	f := &fakeRunner{}
	if err := (Client{R: f}).RemoveVolumes([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"volume", "rm", "a", "b"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("args = %v", f.calls[0].args)
	}
}

func TestContainerProject(t *testing.T) {
	f := &fakeRunner{responses: []response{{stdout: `{"sigil.project":"/p","sigil.layout":"3"}` + "\n"}}}
	got, err := Client{R: f}.ContainerProject("c")
	if err != nil || got != "/p" {
		t.Errorf("(%q, %v)", got, err)
	}
	want := []string{"container", "inspect", "--format", "{{json .Config.Labels}}", "c"}
	if !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("args = %v", f.calls[0].args)
	}
}

func TestEnsureVolumesCreatesMissing(t *testing.T) {
	const c = "sigil-p-1"
	var rs []response
	for range 3 {
		rs = append(rs, response{stdout: ""}, response{}, response{stdout: `{"sigil.project":"/p"}`})
	}
	f := &fakeRunner{responses: rs}
	if err := (Client{R: f}).EnsureVolumes(c, "/p"); err != nil {
		t.Fatal(err)
	}
	var creates [][]string
	for _, cl := range f.calls {
		if cl.args[1] == "create" {
			creates = append(creates, cl.args)
		}
	}
	want := [][]string{
		{"volume", "create", "--label", "sigil.project=/p", c + "-cache"},
		{"volume", "create", "--label", "sigil.project=/p", c + "-go"},
		{"volume", "create", "--label", "sigil.project=/p", c + "-cargo"},
	}
	if !reflect.DeepEqual(creates, want) {
		t.Errorf("create = %v", creates)
	}
}

func TestEnsureVolumesReusesMatching(t *testing.T) {
	const c = "sigil-p-1"
	var rs []response
	for _, v := range ProjectVolumes(c) {
		rs = append(rs, response{stdout: v.Name + "\n"}, response{stdout: `{"sigil.project":"/p"}`})
	}
	f := &fakeRunner{responses: rs}
	if err := (Client{R: f}).EnsureVolumes(c, "/p"); err != nil {
		t.Fatal(err)
	}
	for _, cl := range f.calls {
		if cl.args[1] == "create" {
			t.Errorf("作り直した: %v", cl.args)
		}
	}
}

// ラベルが違う・ない・余白だけ違うボリュームは、このプロジェクトのコンテナに渡さない。
func TestEnsureVolumesForeign(t *testing.T) {
	const c = "sigil-p-1"
	for _, labels := range []string{`{"sigil.project":"/other"}`, "null", `{}`, `{"sigil.project":"/p "}`} {
		f := &fakeRunner{responses: []response{{stdout: c + "-cache\n"}, {stdout: labels}}}
		err := (Client{R: f}).EnsureVolumes(c, "/p")
		var fe *ForeignVolumeError
		if !errors.As(err, &fe) || fe.Name != c+"-cache" {
			t.Errorf("ラベル %s: err = %v", labels, err)
		}
		if len(f.calls) != 2 {
			t.Errorf("ラベル %s: 次のボリュームに進んだ: %v", labels, f.calls)
		}
	}
}

func TestEnsureVolumesDockerDown(t *testing.T) {
	f := &fakeRunner{responses: []response{{err: errors.New("exit status 1")}}}
	err := (Client{R: f}).EnsureVolumes("sigil-p-1", "/p")
	if err == nil || !strings.Contains(err.Error(), "docker に接続できません") {
		t.Errorf("err = %v", err)
	}
	if len(f.calls) != 1 {
		t.Errorf("calls = %v", f.calls)
	}
}
