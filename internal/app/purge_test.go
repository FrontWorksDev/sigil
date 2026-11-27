package app

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- sigil purge（設計書 21 章） ----

func (e *testEnv) volNames() []string {
	n := e.name()
	return []string{n + "-cache", n + "-go", n + "-cargo"}
}

// volume は、名前 name のボリュームがあり、ラベル sigil.project が root であるよう偽の docker に設定する。
func (e *testEnv) volume(name, root string) {
	e.runner.on("docker volume ls --filter name="+name+" ", name+"\n", nil)
	e.runner.on("docker volume inspect --format {{json .Labels}} "+name, labelsJSON(root), nil)
}

func (e *testEnv) allVolumes() {
	for _, v := range e.volNames() {
		e.volume(v, e.proj)
	}
}

func TestPurgeNothing(t *testing.T) {
	e := newEnv(t)
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if e.ttyOpened != 0 {
		t.Error("消すものがないのに確かめた")
	}
	if !strings.Contains(e.stderr.String(), "浄化するものはありません") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestPurgeNoTTY(t *testing.T) {
	e := newEnv(t)
	e.allVolumes()
	e.ttyErr = errors.New("device not configured")
	if code := e.run("purge"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker rm"))+len(e.runner.find("docker volume rm")) != 0 {
		t.Errorf("確かめずに消した: %v", e.runner.calls)
	}
}

func TestPurgeAnswerNo(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	e.allVolumes()
	e.tty = "n\n"
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if len(e.runner.find("docker rm"))+len(e.runner.find("docker volume rm")) != 0 {
		t.Errorf("断ったのに消した: %v", e.runner.calls)
	}
	if !strings.Contains(e.stderr.String(), "浄化しませんでした") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestPurgeYes(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	e.allVolumes()
	e.tty = "y\n"
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	rmAt, volAt := -1, -1
	for i, c := range e.runner.calls {
		switch {
		case strings.HasPrefix(c.String(), "docker rm -f "+e.name()):
			rmAt = i
		case strings.HasPrefix(c.String(), "docker volume rm"):
			volAt = i
			want := append([]string{"volume", "rm"}, e.volNames()...)
			if !reflect.DeepEqual(c.args, want) {
				t.Errorf("volume rm = %v", c.args)
			}
		}
	}
	if rmAt < 0 || volAt < 0 || rmAt > volAt {
		t.Errorf("コンテナを消してからボリュームを消していない: %v", e.runner.calls)
	}
	out := e.stderr.String()
	for _, want := range []string{e.name() + "-cache", "消したものは戻せません", "浄化しました"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr に %q がない: %s", want, out)
		}
	}
}

// ラベルの合わないコンテナとボリュームは、消さずに知らせる。
func TestPurgeSkipsForeign(t *testing.T) {
	e := newEnv(t)
	e.runner.on("docker ps -a --filter name=", e.name()+"\trunning\t3\n", nil)
	e.runner.on("docker container inspect", `{"sigil.project":"/other"}`+"\n", nil)
	n := e.volNames()
	e.volume(n[0], e.proj)
	e.volume(n[1], "/other")
	e.volume(n[2], e.proj+" ")
	e.tty = "y\n"
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if len(e.runner.find("docker rm")) != 0 {
		t.Error("ラベルの違うコンテナを消した")
	}
	rms := e.runner.find("docker volume rm")
	if len(rms) != 1 || !reflect.DeepEqual(rms[0].args, []string{"volume", "rm", n[0]}) {
		t.Errorf("volume rm = %v", rms)
	}
	if got := strings.Count(e.stderr.String(), "このプロジェクトのものではないので消しません"); got != 3 {
		t.Errorf("注意の数 = %d: %s", got, e.stderr.String())
	}
}

// name= の部分一致で出てきた、前方一致するだけの別の名前は対象にしない。
func TestPurgeIgnoresPrefixMatch(t *testing.T) {
	e := newEnv(t)
	other := e.name() + "-cache-x"
	e.runner.on("docker volume ls", other+"\n", nil)
	e.runner.on("docker volume inspect", labelsJSON(e.proj), nil)
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "浄化するものはありません") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

func TestPurgeVolumeRmFailure(t *testing.T) {
	e := newEnv(t)
	e.allVolumes()
	e.runner.on("docker volume rm", "", errors.New("volume is in use"))
	e.tty = "y\n"
	if code := e.run("purge"); code != 1 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(e.stderr.String(), "ほかのペインで sigil を使っていないか") {
		t.Errorf("stderr = %s", e.stderr.String())
	}
}

// 確認を待つあいだにラベルが変わったボリュームは消さない。
func TestPurgeRechecksLabelBeforeRemove(t *testing.T) {
	e := newEnv(t)
	e.allVolumes()
	n := e.volNames()
	reads := 0
	e.runner.onFunc("docker volume inspect --format {{json .Labels}} "+n[1], func() (string, error) {
		reads++
		if reads == 1 {
			return labelsJSON(e.proj), nil
		}
		return labelsJSON("/other"), nil
	})
	e.tty = "y\n"
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	rms := e.runner.find("docker volume rm")
	if len(rms) != 1 || !reflect.DeepEqual(rms[0].args, []string{"volume", "rm", n[0], n[2]}) {
		t.Errorf("volume rm = %v", rms)
	}
}

// フォルダ名の双方向の書式文字は、見える形で出す（消す対象の表示を偽装させない）。
func TestPurgeEscapesDisplay(t *testing.T) {
	e := newEnv(t)
	e.proj = filepath.Join(filepath.Dir(e.proj), "pro\u202ej")
	e.cwd = e.proj
	mustMkdir(t, filepath.Join(e.proj, ".git"))
	e.runner.on("git -C", e.proj+"\n", nil)
	e.allVolumes()
	e.tty = "n\n"
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	out := e.stderr.String()
	if !strings.Contains(out, "<U+202E>") || strings.Contains(out, "\u202e") {
		t.Errorf("stderr = %q", out)
	}
}

func TestDashDashPurgeRunsInContainer(t *testing.T) {
	e := newEnv(t)
	if code := e.run("--", "purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	argv := e.lastExec().argv
	if argv[len(argv)-1] != "purge" {
		t.Errorf("argv = %v", argv)
	}
	if len(e.runner.find("docker volume rm")) != 0 {
		t.Error("ボリュームを消した")
	}
}

// 確認を待つあいだに、別のプロジェクトのラベルのコンテナに替わっていたら消さない。
func TestPurgeRechecksContainerBeforeRemove(t *testing.T) {
	e := newEnv(t)
	e.container("running", "3")
	reads := 0
	e.runner.onFunc("docker container inspect", func() (string, error) {
		reads++
		if reads == 1 {
			return `{"sigil.project":` + jsonString(e.proj) + `}` + "\n", nil
		}
		return `{"sigil.project":"/other"}` + "\n", nil
	})
	e.tty = "y\n"
	if code := e.run("purge"); code != 0 {
		t.Fatalf("code = %d, stderr = %s", code, e.stderr.String())
	}
	if len(e.runner.find("docker rm")) != 0 {
		t.Error("ラベルの替わったコンテナを消した")
	}
}

// 動いているコンテナは、ほかのペインのセッションごと終わらせるので、確認の一覧で知らせる。
func TestPurgeWarnsRunningContainer(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  bool
	}{{"running", true}, {"exited", false}} {
		e := newEnv(t)
		e.container(tc.state, "3")
		e.tty = "n\n"
		if code := e.run("purge"); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if got := strings.Contains(e.stderr.String(), "実行中のコンテナを止めます"); got != tc.want {
			t.Errorf("%s: 知らせ = %v, stderr = %s", tc.state, got, e.stderr.String())
		}
	}
}
