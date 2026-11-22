package docker

import (
	"io"
	"strings"
	"testing"
)

const sampleBuild = `#0 building with "desktop-linux" instance using docker driver

#1 [internal] load build definition from Dockerfile
#1 transferring dockerfile: 4.21kB done
#1 DONE 0.0s

#2 [node 1/1] FROM docker.io/library/node:lts-trixie-slim
#2 DONE 0.1s

#3 [stage-4  1/19] FROM docker.io/library/php:cli-trixie
#3 DONE 0.2s

#4 [stage-4  2/19] RUN apt-get update && apt-get install -y
#4 CACHED

#5 [stage-4  3/19] RUN docker-php-ext-install -j"$(nproc)" zip intl bcmath
#5 0.345 Configuring for:
#5 DONE 12.3s

#21 exporting to image
#21 exporting layers 0.4s done
#21 naming to docker.io/library/sigil:latest done
#21 DONE 0.5s
`

type got struct {
	phase int
	note  string
	ratio float64
}

func watch(t *testing.T, chunks ...string) (*BuildWatcher, []got) {
	t.Helper()
	var gs []got
	w := &BuildWatcher{OnProgress: func(p int, n string, r float64) { gs = append(gs, got{p, n, r}) }}
	for _, c := range chunks {
		io.WriteString(w, c)
	}
	return w, gs
}

func TestBuildWatcherPhases(t *testing.T) {
	w, gs := watch(t, sampleBuild)
	if w.Log.String() != sampleBuild {
		t.Errorf("出力をすべてためること")
	}
	// 最初は素材集め（FROM まで）
	if gs[0].phase != BuildGather {
		t.Errorf("最初の段階 = %v", gs[0])
	}
	var forge *got
	for i := range gs {
		if gs[i].phase == BuildForge {
			forge = &gs[i]
			break
		}
	}
	if forge == nil || forge.note != "[2/19] RUN apt-get update && apt-get install -y" {
		t.Fatalf("鋳造の段階 = %v", forge)
	}
	last := gs[len(gs)-1]
	if last.phase != BuildEngrave {
		t.Errorf("最後は書き出し: %v", last)
	}
	// 最終ステージ（19 手順）のうち、済んだのは 3 つ（FROM、CACHED、DONE）。node の 1/1 は数えない
	if want := 3.0 / 19; last.ratio != want {
		t.Errorf("進み具合 = %v, want %v", last.ratio, want)
	}
}

func TestBuildWatcherSplitWrites(t *testing.T) {
	// 書き込みが行の途中で切れても、行としてまとめて読む
	var chunks []string
	for i := 0; i < len(sampleBuild); i += 7 {
		chunks = append(chunks, sampleBuild[i:min(i+7, len(sampleBuild))])
	}
	_, gs := watch(t, chunks...)
	if len(gs) == 0 || gs[len(gs)-1].phase != BuildEngrave {
		t.Errorf("分割した書き込みで段階を追えていない: %v", gs)
	}
}

func TestBuildWatcherIgnoresUnknown(t *testing.T) {
	_, gs := watch(t, "Sending build context to Docker daemon\nStep 1/3 : FROM x\n")
	if len(gs) != 0 {
		t.Errorf("BuildKit 以外の出力で進めている: %v", gs)
	}
}

func TestHasProgressFlag(t *testing.T) {
	for _, extra := range [][]string{{"--progress=tty"}, {"--progress", "plain"}, {"-q"}, {"--no-cache", "--quiet"}} {
		if !HasProgressFlag(extra) {
			t.Errorf("%v", extra)
		}
	}
	if HasProgressFlag([]string{"--no-cache", "--pull"}) || HasProgressFlag(strings.Fields("")) {
		t.Errorf("指定がないのに true")
	}
}

func TestBuildWatcherScratchFirstStepIsForge(t *testing.T) {
	// 実際の BuildKit の出力（FROM scratch の多段ビルド）。1 番目の手順が COPY でも鋳造とする
	_, gs := watch(t, "#4 [stage-1 1/3] COPY a.txt /x\n#4 DONE 0.0s\n#5 [one 1/1] COPY a.txt /a\n#5 DONE 0.0s\n")
	if gs[0].phase != BuildForge || gs[0].note != "[1/3] COPY a.txt /x" {
		t.Errorf("%v", gs[0])
	}
	if last := gs[len(gs)-1]; last.ratio != 1.0/3 {
		t.Errorf("別のステージ（one 1/1）を数えている: %v", last)
	}
}

func TestBuildWatcherSingleStage(t *testing.T) {
	// 1 ステージの Dockerfile では、手順にステージ名が付かない
	_, gs := watch(t, "#5 [1/3] FROM docker.io/library/alpine\n#5 DONE 0.1s\n#6 [2/3] RUN apk add git\n#6 DONE 1.0s\n")
	last := gs[len(gs)-1]
	if last.phase != BuildForge || last.note != "[2/3] RUN apk add git" || last.ratio != 2.0/3 {
		t.Errorf("%v", last)
	}
}
