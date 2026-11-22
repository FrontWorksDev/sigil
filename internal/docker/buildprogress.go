package docker

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// ビルドの段階（sigil build の演出で使う）。
const (
	BuildGather  = iota // 定義の読み込み、ベースイメージの取得
	BuildForge          // 最終ステージの各手順
	BuildEngrave        // イメージへの書き出し
)

// BuildWatcher は、docker build --progress=plain の出力を受け取り、
// 段階と進み具合を OnProgress に伝える。受け取った出力はすべて Log にためる
// （失敗したときに、docker のログをそのまま見せるため）。
//
// BuildKit の出力の形（例）:
//
//	#5 [stage-4  3/19] RUN apt-get update ...
//	#5 CACHED
//	#7 DONE 12.3s
//	#21 exporting to image
//
// Dockerfile の中身には依存しない（SIGIL_CONTEXT で別の Dockerfile を使っても動くように）。
// 手順の数がいちばん多いステージを最終ステージとみなし、その済んだ手順の数で進み具合を出す。
type BuildWatcher struct {
	OnProgress func(phase int, note string, ratio float64)
	Log        bytes.Buffer

	partial []byte
	phase   int
	stage   string         // 最終ステージとみなしている名前
	total   int            // その手順の数
	steps   map[int]string // 頂点の番号 → ステージの名前（手順の頂点だけ）
	done    map[int]bool   // 済んだ頂点（最終ステージの手順だけ数える）
	note    string
}

var (
	reVertex = regexp.MustCompile(`^#(\d+) \[([^\]]+)\] (.*)$`)
	reStep   = regexp.MustCompile(`^(.*?)\s+(\d+)/(\d+)$`)
	reDone   = regexp.MustCompile(`^#(\d+) (DONE|CACHED)\b`)
	reExport = regexp.MustCompile(`^#\d+ exporting to image`)
)

func (w *BuildWatcher) Write(b []byte) (int, error) {
	w.Log.Write(b)
	w.partial = append(w.partial, b...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.line(strings.TrimRight(string(w.partial[:i]), "\r"))
		w.partial = w.partial[i+1:]
	}
	return len(b), nil
}

func (w *BuildWatcher) line(l string) {
	if w.steps == nil {
		w.steps, w.done = map[int]string{}, map[int]bool{}
	}
	switch {
	case reExport.MatchString(l):
		w.phase = BuildEngrave
		w.note = "exporting to image"
	case reVertex.MatchString(l):
		m := reVertex.FindStringSubmatch(l)
		s := reStep.FindStringSubmatch(m[2])
		if s == nil {
			return // [internal] など
		}
		id, _ := strconv.Atoi(m[1])
		k, _ := strconv.Atoi(s[2])
		n, _ := strconv.Atoi(s[3])
		name := strings.TrimSpace(s[1])
		if n > w.total {
			w.stage, w.total = name, n
			// 最終ステージが替わったら、済んだ数を数え直す
			for d := range w.done {
				if w.steps[d] != name {
					delete(w.done, d)
				}
			}
		}
		w.steps[id] = name
		if name == w.stage && w.phase < BuildEngrave {
			if !strings.HasPrefix(m[3], "FROM ") {
				// FROM はベースイメージの取得（素材集め）。それ以外の手順からを鋳造とする
				// （FROM scratch では、1 番目の手順が COPY になるので、番号では決めない）
				w.phase = BuildForge
			}
			w.note = "[" + strconv.Itoa(k) + "/" + strconv.Itoa(n) + "] " + m[3]
		}
	case reDone.MatchString(l):
		id, _ := strconv.Atoi(reDone.FindStringSubmatch(l)[1])
		if name, ok := w.steps[id]; ok && name == w.stage {
			w.done[id] = true
		}
	default:
		return
	}
	if w.OnProgress != nil {
		ratio := -1.0
		if w.total > 0 {
			ratio = float64(len(w.done)) / float64(w.total)
		}
		w.OnProgress(w.phase, w.note, ratio)
	}
}

// HasProgressFlag は、追加の引数で出力の形が指定されているかを返す。
// 指定されていれば、利用者の指定を優先し、sigil は出力を読まない。
func HasProgressFlag(extra []string) bool {
	for _, a := range extra {
		if a == "--progress" || strings.HasPrefix(a, "--progress=") || a == "-q" || a == "--quiet" {
			return true
		}
	}
	return false
}
