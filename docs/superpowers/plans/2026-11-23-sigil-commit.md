# sigil commit 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** コンテナが書いた銘（`.sigil-commit`）で、ホストの git により、確認のうえコミットし、push を別に確かめる `sigil commit` を作る。あわせて、判断の画面（`.sigil` の承認と `sigil commit` の確認）を色・枠・記号で飾る。

**Architecture:**
- git の手順（点検、tree の確定、刻印、push、コンテナでのフック）は新しい `internal/gitcommit` に置く。外部コマンドは、環境変数と標準入力を渡せる `gitcommit.Runner` を通す。
- 画面の描画は `internal/ui` に置く。ui は gitcommit を知らず、ui 側の型（`CommitView` など）を受け取る。変換は `internal/app/commit.go` が行う。
- 流れ全体は `internal/app/commit.go`。テストは、git は本物（一時フォルダのリポジトリ。git がなければ skip）、docker と tty は偽物で行う。

**Tech Stack:** Go 1.27.4、標準ライブラリのみ、`CGO_ENABLED=0`、git、docker CLI。

**Spec:** `docs/superpowers/specs/2026-11-19-dev-cli-design.md`（以下「設計書」）の 17 章、16 章、13 章、14 章と、2026-11-23 の決定事項。

## Global Constraints

- 外部モジュール禁止、cgo 禁止。`gofmt` / `go vet` が通ること（`make check`）。
- ユーザー向けのメッセージ・エラーは日本語で `sigil: ` から始め、標準エラーへ。失敗は終了コード 1。エラーは `lore` / `loref`（1 行目が用語、2 行目が普通の言葉）。
- コメントは日本語で「なぜ」を書く。特にセキュリティ上の理由。
- 銘のファイル名は `.sigil-commit`（プロジェクトの最上位）。上限 64KiB。
- ホストの git の呼び出しには必ず `-c core.hooksPath=/dev/null -c core.fsmonitor=false -c submodule.recurse=false -c diff.ignoreSubmodules=all` を付ける。
- 答えは `/dev/tty` から 1 行読む。`y` / `Y` / `yes` / `YES` だけを肯定とする。
- コンテナが書いた内容（差分、ファイル名、銘、フックの出力、push の URL）は、`ui.Escape` を通してから画面に出す。git の `--color` は使わない。
- 判断の画面は動かさない。演出（魔法陣）は、コンテナでフックを実行しているあいだと、刻印のあとの明滅だけ。刻印と push の最中は描かない。
- Plain では記号と言葉だけの素のテキスト、Mono では色なし。
- 用語: 刻印の儀 / 刻印（コミット）、銘（コミットメッセージ）、試練（フック）、解き放つ / 外界（push / push 先）。判断の画面では用語に意味を添える。
- コミットは**ユーザーに確認してから**（CLAUDE.md）。各タスクの「コミット」は提案のタイミング。
- 実機の確認では `sigil:dev-test` と一時フォルダだけを使う。`sigil-*` と `sigil:latest` には触れない。`op` は実行しない。

## Review Focus

1. **最初のコミット（HEAD がまだない）** → 親なしで刻印でき、`update-ref` は「まだない」ことを条件にする。差分は空の tree と比べる（Task 3、4 にテスト）。
2. **ファイル名にスペース・日本語・改行・ESC がある** → `-z` で正しく読み、画面ではエスケープとして出ない（Task 3、7 にテスト）。
3. **確認のあいだに作業ツリーが変わる／別の端末でコミットされる** → 前者は画面の tree をコミットする。後者はエラーにし、index を変えない（Task 4、9 にテスト）。
4. **`sigil commit -- <path>` で一部だけ** → pathspec の外で利用者が add してあった状態は、コミットのあとも index に残る（Task 4 にテスト）。
5. **銘が CRLF・BOM 付き・`#` 行あり・前後に空行** → CR と BOM を取り除き、`#` 行は残し、前後の空行を落とす。画面に出した文字列とコミットの銘が一致する（Task 1、9 にテスト）。

---

## ファイル構成

```
internal/gitcommit/
  message.go       銘の読み込みと整形（ReadMessage, Normalize）
  runner.go        Runner / RunOpts / ExecRunner、Git（共通の -c を付けて実行）
  repo.go          点検（Inspect）と拒否する条件
  snapshot.go      一時 index での tree の確定と差分（Snapshot, Change）、注意の分類（Warnings）
  seal.go          刻印（Seal）と index の後始末
  hooks.go         フックの場所の決定、コンテナで実行する固定のスクリプトと docker exec の引数
  push.go          upstream の読み取りと push
  *_test.go        本物の git を使うテストは git がなければ t.Skip
internal/ui/
  escape.go        Escape（制御文字を見える形に）
  answer.go        ReadAnswer / IsYes
  confirm.go       CommitScreen, DiffPage, PushScreen, Sealed, ContractScreen
internal/approval/approval.go   Ask が ui の ContractScreen と ReadAnswer を使う
internal/app/
  deps.go          Cmd（gitcommit.Runner）と Pager を足す
  app.go           commit の振り分け、usage
  commit.go        sigil commit の流れ
  commit_test.go
README.md, examples/sigil-commit.md（コンテナの Claude Code への説明のひな形）
```

---

### Task 1: 銘の読み込みと整形

**Files:**
- Create: `internal/gitcommit/message.go`, `internal/gitcommit/message_test.go`

**Interfaces:**
- Produces:
  - `const MessageFile = ".sigil-commit"`、`const MaxMessageSize = 64 << 10`
  - `func ReadMessage(path string) (string, error)`: ファイルがなければ `ErrNoMessage`。読んだバイト列を `Normalize` に通して返す
  - `func Normalize(b []byte) (string, error)`: 銘の検査と整形。コンテナのフックが書き換えた銘にも使う
  - `var ErrNoMessage = errors.New(...)`（「銘がありません」の判定用）

- [ ] **Step 1: 失敗するテストを書く**

`TestReadMessage` を表で書く:
- 通常: `"feat: x\n\nbody\n"` → `"feat: x\n\nbody"`
- BOM・CRLF: `"﻿feat: x\r\n\r\nbody\r\n"` → `"feat: x\n\nbody"`
- 制御文字: `"fix\x1b[31m: y\x07\n"` → `"fix[31m: y"`（ESC と BEL だけが消える）。`\t` は残る
- `#` 行: `"a\n# keep\n"` → `"a\n# keep"`
- 前後の空行: `"\n\n a\n\n"` → `" a"`
- 空: `"\n \n"` → `ErrNoMessage`
- 不正な UTF-8 `"\xff"` → エラー
- 双方向の書式文字: U+202E、U+2066、U+200F、U+061C をそれぞれ含む → エラー
- シンボリックリンク（中身のあるファイルへのリンク）→ エラー。エラーの文字列にリンク先の中身が含まれない
- FIFO（`syscall.Mkfifo`）→ エラー（固まらないこと。`O_NONBLOCK` で開く）
- 64KiB + 1 バイト → エラー
- ファイルなし → `errors.Is(err, ErrNoMessage)`

- [ ] **Step 2: 失敗を確かめる**

Run: `go test ./internal/gitcommit/ -run TestReadMessage`
Expected: FAIL（未定義）

- [ ] **Step 3: 実装する**

`os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)` で開く。リンクなら `ELOOP` になる。`f.Stat()` で通常のファイルか、大きさが上限以下かを確かめる。`io.LimitReader(f, MaxMessageSize+1)` で読む。前後の空行は「空白だけの行」も含めて落とす。行頭・行末の空白はそのまま残す。双方向の書式文字の一覧は設計書 17 章のとおり。エラーの文言は日本語で、パスを含めてよい。

- [ ] **Step 4: 通ることを確かめる**

Run: `go test ./internal/gitcommit/ -run TestReadMessage`
Expected: PASS

- [ ] **Step 5: コミット（提案）** `銘の読み込みと整形を足す (#13)`

---

### Task 2: Runner と Git、エスケープ

**Files:**
- Create: `internal/gitcommit/runner.go`, `internal/gitcommit/runner_test.go`, `internal/ui/escape.go`, `internal/ui/escape_test.go`

**Interfaces:**
- Produces:
  - `type RunOpts struct { Env []string; Stdin io.Reader; Stdout, Stderr io.Writer }`
  - `type Runner interface { Run(name string, args []string, o RunOpts) error }`
  - `type ExecRunner struct{}`: `os/exec` で実行する。`Env` は `os.Environ()` の後ろに足す。`Stdin` が nil なら何もつながない（`os.Stdin` をつながない）
  - `var SafeConfig = []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-c", "diff.ignoreSubmodules=all"}`
  - `type Git struct { R Runner; Root string; Env []string }`
  - `func (g Git) Out(stdin io.Reader, args ...string) (string, error)`: `git -C <Root> <SafeConfig...> <args...>` を実行し、標準出力を返す。失敗したら標準エラーを `ui.Escape` したものを含むエラーを返す
  - `func (g Git) With(env ...string) Git`: Env を足したコピー
  - `func Escape(s string) string`（ui）: C0（`\n` と `\t` を除く）、DEL、C1 は `^@`〜`^_` / `^?` / `<U+0085>` の形にする。`unicode.IsPrint` が偽の書式文字（U+202E、U+200B など）は `<U+202E>` の形にする。それ以外（日本語、絵文字、空白）はそのまま
  - `func EscapeLine(s string) string`（ui）: `Escape` に加えて `\n` → `^J`、`\t` → `^I`。ファイル名など 1 行で出すもの用

- [ ] **Step 1: 失敗するテストを書く**

- `TestEscape`: `"a\x1b[2Jb"` → `"a^[[2Jb"`、`"\x7f"` → `"^?"`、`"\u0085"` → `"<U+0085>"`、`"x‮y"` → `"x<U+202E>y"`、`"日本語 🎉\tz\n"` → そのまま
- `TestEscapeLine`: `"a\nb\tc"` → `"a^Jb^Ic"`
- `TestGitAddsSafeConfig`: 記録する偽の Runner で `Git{Root: "/r"}.Out(nil, "status")` を呼ぶと、argv が `-C /r -c core.hooksPath=/dev/null -c core.fsmonitor=false -c submodule.recurse=false -c diff.ignoreSubmodules=all status` になる
- `TestGitErrorEscapesStderr`: 偽の Runner が標準エラーに `"bad\x1b[31m"` を書いて失敗すると、エラーの文字列に `\x1b` が含まれず、`^[` が含まれる

- [ ] **Step 2: 失敗を確かめる** — Run: `go test ./internal/gitcommit/ ./internal/ui/ -run 'Escape|Git'` Expected: FAIL

- [ ] **Step 3: 実装する**（上の Interfaces のとおり）

- [ ] **Step 4: 通ることを確かめる** — 同じコマンドで PASS

- [ ] **Step 5: コミット（提案）** `git の実行とエスケープを足す (#13)`

---

### Task 3: 点検と、tree の確定・差分・注意の分類

**Files:**
- Create: `internal/gitcommit/repo.go`, `internal/gitcommit/snapshot.go`, `internal/gitcommit/repo_test.go`, `internal/gitcommit/snapshot_test.go`, `internal/gitcommit/testutil_test.go`

**Interfaces:**
- Consumes: Task 2 の `Git`
- Produces:
  - `type Repo struct { Root, GitDir, CommonDir, Branch, Head, HooksDir string }`。`Branch` は `refs/heads/<name>`、`Head` はまだコミットがなければ `""`。パスはすべて絶対パス
  - `func Inspect(g Git) (Repo, error)`: 次の順に調べ、当てはまれば `*Refusal` を返す
    1. HEAD がブランチを指していない（`symbolic-ref -q HEAD` が失敗）
    2. 作業ツリーの中から読み込まれる設定（`config --show-origin -z --list` の `file:<path>` を Root からの絶対パスにし、Root の中で、かつ GitDir / CommonDir の外）
    3. 進行中の操作（GitDir に `MERGE_HEAD`、`CHERRY_PICK_HEAD`、`REVERT_HEAD`、`rebase-merge`、`rebase-apply`）
    4. LFS（Root の `.lfsconfig` がある。または `git ls-files -z --cached --others --exclude-standard -- ':(glob)**/.gitattributes'` で見つかった各ファイルに `filter=lfs` がある）
    - `HooksDir` は `rev-parse --git-path hooks` を Root からの絶対パスにしたもの
  - `type Refusal struct { Lore, Msg string }`（`Error()` は Msg）。app はこれを `lore(r.Lore, …)` にする
  - `type Change struct { Status byte; Path, OldPath, OldMode, NewMode, NewBlob string; Added, Deleted int; Binary bool; Size int64; LinkTarget string }`。Status は `'A' 'M' 'D' 'R' 'T'`
  - `type Snapshot struct { Tree, Base string; Changes []Change; TmpDir string }`。`Base` は HEAD（なければ空の tree）
  - `func TakeSnapshot(g Git, r Repo, pathspec []string) (Snapshot, error)`: 一時フォルダ（`os.MkdirTemp("", "sigil-commit-*")`、0700）に `GitDir/index` を写す（なければ写さない）。`GIT_INDEX_FILE` で指し、`reset -q`（Head があるときだけ）→ `add -A -- <pathspec...> ':(exclude,top).sigil-commit'` → `write-tree` を行う。差分は `diff-tree -r -z -M --raw` と `--numstat` を `Base` と `Tree` で取る。追加・変更されたシンボリックリンクは `cat-file blob` でリンク先を読み、新しい blob の大きさは `cat-file -s` で読む。gitlink（160000）の追加・変更があれば `*Refusal`、変更がなければ `ErrNothing`
  - `func (s Snapshot) Cleanup()`: 一時フォルダを消す
  - `var ErrNothing`（「刻むものがありません」）
  - `type Warning struct { Path, Reason string }`
  - `func Warnings(cs []Change, r Repo) []Warning`: 設計書 17 章「確認の画面」3 の一覧どおり。CI は「GitHub などで実行されます」、フックは「ほかの人の環境や CI で実行されます」。実行権限（NewMode が 100755 で、OldMode がそうでない）、シンボリックリンク（理由にリンク先）、1MiB 超。削除は対象外
  - 空の tree は `hash-object -t tree --stdin`（標準入力は空）で求める。SHA-256 のリポジトリに備えて、固定値を使わない

- [ ] **Step 1: テスト用の補助を書く**（`testutil_test.go`）

`newRepo(t) (root string, g Git)`: `exec.LookPath("git")` が失敗したら `t.Skip`。`t.TempDir()` を EvalSymlinks して `git init -q -b main`。`user.name` / `user.email` / `commit.gpgSign=false` をリポジトリの設定に書く。テスト中は `GIT_CONFIG_GLOBAL=/dev/null`、`GIT_CONFIG_NOSYSTEM=1` を `Git.Env` に入れ、利用者の全体設定に左右されないようにする。`write(t, root, rel, content)`、`commitAll(t, g, msg)` も用意する。

- [ ] **Step 2: 失敗するテストを書く**

- `TestInspectRefuses`: 次のそれぞれで `*Refusal` になる
  - detached HEAD
  - `.git/config` に `[include] path = ../evil.gitconfig`
  - `.git/MERGE_HEAD` がある
  - `sub/.gitattributes` に `*.bin filter=lfs`
  - `.lfsconfig` がある
- `TestInspectOK`: 最初のコミット前は `Head == ""`、`Branch == "refs/heads/main"`。`core.hooksPath=.husky/_` なら `HooksDir == root+"/.husky/_"`
- `TestSnapshotAllChanges`: 変更・追加（追跡していない）・削除・`a b/日本語.txt`・`.sigil-commit` があるとき、Changes に `.sigil-commit` 以外が正しい Status で入る。本物の index（`git diff --cached --name-only`）は空のまま
- `TestSnapshotPathspec`: `-- a.txt` なら a.txt だけ
- `TestSnapshotInitialCommit`: HEAD がなくても Changes がすべて `'A'`
- `TestSnapshotNameWithNewline`: `"x\ny\x1b.txt"` という名前を 1 つの Change として読む
- `TestSnapshotRefusesGitlink`: `sub/` に別の `git init` をして中でコミットしてあると `*Refusal`
- `TestSnapshotNothing`: 変更がなければ `errors.Is(err, ErrNothing)`
- `TestSnapshotBinaryAndLink`: バイナリは `Binary` と `Size`、シンボリックリンクは `NewMode=="120000"` と `LinkTarget`
- `TestWarnings`: `.github/workflows/ci.yml`、`.env.local`、`.gitignore`、`.husky/pre-commit`、`.sigil`、755 になったファイル、リンク、2MiB のファイルにそれぞれ Warning が付き、`src/a.go` には付かない

- [ ] **Step 3: 失敗を確かめる** — Run: `go test ./internal/gitcommit/ -run 'Inspect|Snapshot|Warnings'` Expected: FAIL

- [ ] **Step 4: 実装する**（Interfaces のとおり。`--raw -z` の書式は `:<old mode> <new mode> <old sha> <new sha> <status>[score]\0<path>\0[<new path>\0]`。`--numstat -z` は改名のとき `<a>\t<d>\t\0<old>\0<new>\0`、バイナリは `-\t-`）

- [ ] **Step 5: 通ることを確かめる** — 同じコマンドで PASS

- [ ] **Step 6: コミット（提案）** `コミットの前の点検と、tree の確定を足す (#13)`

---

### Task 4: 刻印

**Files:**
- Create: `internal/gitcommit/seal.go`, `internal/gitcommit/seal_test.go`

**Interfaces:**
- Consumes: Task 3 の `Repo`, `Snapshot`
- Produces:
  - `func Seal(g Git, r Repo, s Snapshot, message string, pathspec []string) (commit string, err error)`:
    1. `commit-tree <Tree> [-p <Head>] -F -`（標準入力に `message + "\n"`）
    2. `update-ref -m "commit (sigil): <件名>" <Branch> <新> <旧>`。旧は Head。Head が空なら、ハッシュと同じ長さの `0` の並び。失敗したら `ErrMoved`（「別の場所で刻印されたようです。もう一度実行してください」）を返し、本物の index は触らない
    3. 本物の index で `reset -q -- <pathspec...> ':(exclude,top).sigil-commit'`
  - `var ErrMoved`
  - 署名で端末を使うことがあるので、`commit-tree` だけは `RunOpts.Stderr` を呼んだ側の標準エラーにつなぐ: `func (g Git) Interactive(stdin io.Reader, stderr io.Writer, args ...string) (string, error)` を Task 2 の Git に足す

- [ ] **Step 1: 失敗するテストを書く**

- `TestSealCommitsSnapshotNotWorktree`: Snapshot のあとで作業ツリーのファイルを書き換えてから Seal する。コミットの tree が Snapshot.Tree と同じで、書き換えた内容は含まれない（`git show HEAD:a.txt`）
- `TestSealMessageExact`: 銘 `"feat: x\n\n# keep"` が `git log -1 --format=%B` でそのまま（末尾の改行を除く）出る
- `TestSealInitialCommit`: 最初のコミットで、親がない
- `TestSealMovedHead`: Snapshot のあとに別のコミットを作ると `errors.Is(err, ErrMoved)`。ブランチは別のコミットのまま。本物の index も変わらない（Seal の前後で `.git/index` のバイト列が同じ）
- `TestSealKeepsOtherStaged`: b.txt を本物の index に add しておき、`pathspec=["a.txt"]` で刻印すると、あとで `git diff --cached --name-only` が `b.txt` を返す
- `TestSealDoesNotRunHooks`: `core.hooksPath=.husky` にし、`.husky/pre-commit`、`commit-msg`、`post-commit`、`prepare-commit-msg`、`reference-transaction`（どれも `touch "$MARK"` の 755 のスクリプト。`MARK` は作業ツリーの外の一時フォルダ）を置く。TakeSnapshot と Seal のあとで、印のファイルがない。比較のため、同じリポジトリで素の `git commit --allow-empty -m x` を実行すると、印ができる（テストが hook を正しく置けていることの確認）

- [ ] **Step 2: 失敗を確かめる** — Run: `go test ./internal/gitcommit/ -run Seal` Expected: FAIL

- [ ] **Step 3: 実装する**

- [ ] **Step 4: 通ることを確かめる** — 同じコマンドで PASS

- [ ] **Step 5: コミット（提案）** `一時 index の tree を刻印する (#13)`

---

### Task 5: コンテナでのフック

**Files:**
- Create: `internal/gitcommit/hooks.go`, `internal/gitcommit/hooks_test.go`

**Interfaces:**
- Consumes: Task 3 の `Repo`
- Produces:
  - `const (PreCommit = "pre-commit"; PrepareCommitMsg = "prepare-commit-msg"; CommitMsg = "commit-msg"; PrePush = "pre-push")`
  - `type Hooks struct { Dir string; Present map[string]bool }`
  - `func FindHooks(r Repo) (Hooks, error)`: 4 つの名前ごとに、`HooksDir/<name>` が実行できる通常のファイルか（`os.Stat`、mode&0111 != 0）を調べる。1 つでもあって、HooksDir が Root の外にあれば `*Refusal`（「フックがプロジェクトの外（<dir>）にあり、領域から実行できません。手動でコミットしてください」）
  - `func (h Hooks) Any(names ...string) bool`
  - `func HookExecArgs(container, containerRoot, hostRoot, hooksDir, name string, extra []string) []string`: `docker exec` の引数。`exec -i -w <containerRoot> <container> sh -c <hookScript> sh <mode> <フックのコンテナ内パス> <extra...>`。mode は `pre-commit` / `msg` / `pre-push`。フックのコンテナ内パスは hostRoot を containerRoot に読み替えたもの
  - `func RunHook(r Runner, args []string, stdin io.Reader, stdout, stderr io.Writer) error`: `docker` を実行する
  - extra: pre-commit は pathspec（`.sigil-commit` の除外を含む）、prepare-commit-msg は `message`、commit-msg はなし、pre-push は `<remote> <url>`
  - `hookScript` は次の固定の文字列（フックのパスなどは位置引数で受け取り、文字列に埋め込まない）:

```sh
set -eu
mode="$1"; hook="$2"; shift 2
gitdir="$(git rev-parse --absolute-git-dir)"
common="$(cd "$(git rev-parse --git-common-dir)" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/objects"
[ -f "$gitdir/index" ] && cp "$gitdir/index" "$tmp/index"
# .git は読み取り専用なので、フックの中の git add などが書く場所を一時フォルダに向ける
export GIT_INDEX_FILE="$tmp/index" GIT_OBJECT_DIRECTORY="$tmp/objects" GIT_ALTERNATE_OBJECT_DIRECTORIES="$common/objects"
case "$mode" in
pre-commit)
  if git rev-parse -q --verify HEAD >/dev/null; then git reset -q; fi
  git add -A -- "$@"
  "$hook" </dev/null >&2 ;;
msg)
  cat >"$tmp/MSG"
  "$hook" "$tmp/MSG" "$@" </dev/null >&2
  cat "$tmp/MSG" ;;
pre-push)
  "$hook" "$@" >&2 ;;
esac
```

- [ ] **Step 1: 失敗するテストを書く**

- `TestFindHooks`: `.husky/_/pre-commit`（755）と `commit-msg`（644）なら、Present は pre-commit だけ
- `TestFindHooksOutside`: `core.hooksPath` がリポジトリの外のフォルダで、そこに pre-commit があれば `*Refusal`。フックがなければ refusal にしない
- `TestHookExecArgs`: `HookExecArgs("sigil-p-1", "/workspace/p", "/h/p", "/h/p/.husky/_", "pre-commit", []string{".", ":(exclude,top).sigil-commit"})` が `exec -i -w /workspace/p sigil-p-1 sh -c <hookScript> sh pre-commit /workspace/p/.husky/_/pre-commit . :(exclude,top).sigil-commit` になる。`hookScript` に `/h/p` もフックの名前も含まれない
- `TestHookScriptReadOnlyGit`（`sh` と git があるときだけ）: 本物のリポジトリの `.git` を `chmod -R a-w` にし（後で戻す）、`sh -c hookScript sh pre-commit <hook> .` をホストで直接実行する。フックは `git add new.txt && git diff --cached --name-only > "$OUT"`。成功し、OUT に `new.txt` がある。msg モードで、フックが銘の末尾に `X` を足すと、標準出力に足したあとの銘が出る

- [ ] **Step 2: 失敗を確かめる** — Run: `go test ./internal/gitcommit/ -run Hook` Expected: FAIL

- [ ] **Step 3: 実装する**

- [ ] **Step 4: 通ることを確かめる** — 同じコマンドで PASS

- [ ] **Step 5: コミット（提案）** `フックをコンテナで実行する手順を足す (#13)`

---

### Task 6: push

**Files:**
- Create: `internal/gitcommit/push.go`, `internal/gitcommit/push_test.go`

**Interfaces:**
- Consumes: Task 2 の `Git`、Task 3 の `Repo`
- Produces:
  - `type Upstream struct { Remote, URL, MergeRef, RemoteSha string; Ahead int }`
  - `func FindUpstream(g Git, r Repo) (Upstream, bool, error)`: `branch.<name>.remote` と `branch.<name>.merge` がなければ `false`。URL は `remote get-url --push <remote>`。RemoteSha は `refs/remotes/<remote>/<merge の refs/heads/ を除いた名前>`（なければ 0 の並び）。Ahead は `rev-list --count <RemoteSha>..HEAD`（RemoteSha がなければ `rev-list --count HEAD`）
  - `func (u Upstream) PrePushInput(r Repo, head string) string`: `"<Branch> <head> <MergeRef> <RemoteSha>\n"`
  - `func Push(g Git, u Upstream, stdout, stderr io.Writer) error`: `push --no-verify <Remote> HEAD:<MergeRef>`。標準入出力は呼んだ側の端末にそのまま流す（SSH のパスフレーズのため）

- [ ] **Step 1: 失敗するテストを書く**

- `TestFindUpstreamNone`: upstream のないブランチで `false`
- `TestPushToBare`: 一時フォルダの bare リポジトリを origin にし、`push -u` で upstream を作る。新しいコミットのあと `FindUpstream` で `Ahead==1`、`MergeRef=="refs/heads/main"`。`Push` のあと bare の main が HEAD と同じ
- `TestPushSkipsPrePushHook`: `core.hooksPath` の pre-push が印を作るスクリプトでも、Push のあとに印がない

- [ ] **Step 2〜4:** Run: `go test ./internal/gitcommit/ -run 'Upstream|Push'` で FAIL → 実装 → PASS

- [ ] **Step 5: コミット（提案）** `upstream への push を足す (#13)`

---

### Task 7: 判断の画面（ui）

**Files:**
- Create: `internal/ui/answer.go`, `internal/ui/confirm.go`, `internal/ui/confirm_test.go`

**Interfaces:**
- Consumes: Task 2 の `Escape` / `EscapeLine`、既存の `Box`、`Ritual`、`fg` / `bold` / `dim`、`Width`
- Produces:
  - `func ReadAnswer(tty io.Reader) string`: 1 行読み、末尾の `\r\n` を除く
  - `func IsYes(s string) bool`: `y` / `Y` / `yes` / `YES`
  - `type FileRow struct { Status byte; Path, OldPath string; Added, Deleted int; Binary bool; Size int64 }`
  - `type WarnRow struct { Path, Reason string }`
  - `type HookRow struct { Name string; OK bool }`
  - `type CommitView struct { Branch, HeadShort, HeadSubject string; Pathspec []string; Files []FileRow; Warnings []WarnRow; Message string; Hooks []HookRow }`
  - `func (u *UI) CommitScreen(v CommitView)`: 設計書 17 章「確認の画面」1〜5 を出す（問いの行は出さない）。Plain でも同じ情報を記号と言葉で出す。件名が 72 文字を超えれば注意を添える（`TestCommitScreenLongSubject`）
  - `func (u *UI) CommitPrompt()`: `sigil: この内容で刻印する（コミットする）なら y、差分を見るなら d を入力してください [y/N/d] `
  - `func (u *UI) DiffPage(diff string) string`: `git diff` の素の出力を行ごとに `Escape` し、`+` は緑、`-` は赤、`@@` はシアン、`diff --git` の行はファイルの境目の罫線にした文字列を返す（Mono / Plain は色なし）
  - `type PushView struct { Remote, URL, Branch string; Ahead int }`
  - `func (u *UI) PushPrompt(v PushView)`: push 先の枠と `sigil: 外界へ解き放つ（push する）なら y を入力してください [y/N] `
  - `func (u *UI) Sealed(title string, lines ...string)`: 刻印のあと。Plain なら `sigil: ✔ <title>` と lines。それ以外は、すべて済んだ段階の `Ritual`（work はすぐ nil を返す）で明滅させ、続けて `Box`
  - `type ContractView struct { Files []string; Envs []string; Ports []string; Refs [][2]string }`（Refs は名前と参照。参照が空なら「（.env に直接書かれた値）」）
  - `func (u *UI) ContractScreen(v ContractView)`: Plain では今の `approval.Ask` と**1 文字も違わない**文言（問いの行を含む）を出す。それ以外は `⟡ 契約の確認` の枠、区分ごとの印と色、用語と意味の列をそろえる

- [ ] **Step 1: 失敗するテストを書く**

- `TestCommitScreenEscapes`: Path に `"a\x1b[2J.txt"`、Message に `"x"`、Warning の Reason にリンク先 `"‮"` を入れて Color で描く。出力から sigil の付けた SGR（`\x1b[` + 数字と `;` + `m`）を取り除くと、`\x1b` も U+202E も残らない。`^[[2J` と `<U+202E>` は含まれる
- `TestCommitScreenPlain`: Plain の出力に `\x1b` がない。`✚ 追加`、`⚠`、`ホストでは hooks を実行していません` を含む
- `TestCommitScreenMono`: Mono の出力に `\x1b[3` がない（色なし）
- `TestCommitScreenNarrow`: Cols=40 で、どの行も `Width` が 40 以下。長いパスは `…` で詰める
- `TestDiffPage`: `"+a\n-b\n@@ -1 +1 @@\n x\x1b\n"` → Color で `+a` が緑、`x^[` を含み、生の `\x1b` は sigil の SGR だけ
- `TestContractScreenPlainUnchanged`: Plain の ContractScreen の出力が、いまの `approval.Ask` の出力（問いの行まで）と完全に同じ。いまの文言をテストに写して固定する
- `TestIsYes`: `y Y yes YES` は真、`ye`、`n`、`""`、`" y"` は偽

- [ ] **Step 2〜4:** Run: `go test ./internal/ui/` で FAIL → 実装 → PASS

- [ ] **Step 5: コミット（提案）** `判断の画面を色と枠で描く (#13)`

---

### Task 8: 承認の画面を ui で描く

**Files:**
- Modify: `internal/approval/approval.go`（`Ask`）、`internal/approval/approval_test.go`、`internal/app/app.go:587`

**Interfaces:**
- Consumes: Task 7 の `ContractScreen`、`ReadAnswer`、`IsYes`
- Produces: `func Ask(tty io.Reader, u *ui.UI, files []string, c config.Config, refs []opwrap.Entry) bool`

- [ ] **Step 1: テストを直す** — 既存の `TestAsk` / `TestAskColumnsAligned` を、Plain の `ui.New(&out, ui.Options{Mode: ui.Plain})` を渡す形にする（期待する文言は変えない）。`TestAskColor` を足す: Color で `⟡ 契約の確認` と `持ち込むもの（ホストの環境変数）` を含み、`y` で真
- [ ] **Step 2: 失敗を確かめる** — Run: `go test ./internal/approval/` Expected: FAIL（コンパイルエラー）
- [ ] **Step 3: 実装する** — `Ask` は ContractView を作って `u.ContractScreen` を呼び、`ui.IsYes(ui.ReadAnswer(tty))` を返す。app は `newUI(d)` を渡す
- [ ] **Step 4: 通ることを確かめる** — Run: `go test ./...` Expected: PASS（app の承認のテストも含む）
- [ ] **Step 5: コミット（提案）** `承認の画面を色と枠で描く (#13)`

---

### Task 9: `sigil commit` の流れ

**Files:**
- Create: `internal/app/commit.go`, `internal/app/commit_test.go`
- Modify: `internal/app/deps.go`（`Cmd gitcommit.Runner`、`Pager func(content string) error`）、`internal/app/app.go`（振り分けと usage）、`internal/app/fake_test.go`（`newEnv` で `Cmd` と `Pager` を設定する）

**Interfaces:**
- Consumes: Task 1〜8 のすべて
- Produces:
  - `func commitCmd(d Deps, u *ui.UI, p project.Project, args []string) error`
  - `Deps.Pager` の本物: `less` を LookPath し、`less -R -S`、標準入力に content、標準出力は `os.Stderr`、環境に `LESSSECURE=1`。なければ content を標準エラーに書く
- 振り分け: `commit` は、プロジェクトを特定した直後、`stop` と同じ位置で扱う。ただし docker の LookPath より前にプロジェクトを特定する必要があるので、`run` の中で `commit` のときだけ、docker の LookPath の失敗をあとまで持ち越す（フックがあって docker が要るときに、同じ「docker が見つかりません」のエラーにする）
- 引数: `commit` の後ろが空ならすべて、`--` があればその後ろが pathspec。それ以外の引数はエラー（「使い方: sigil commit [-- <path>...]」）
- 流れ（設計書 17 章「流れ」の 1〜10 の順）:
  1. `d.IsTerminal()` でなければエラー。`Inspect` → `FindHooks` → `ReadMessage`（ErrNoMessage は `lore("銘がありません", …)`。案内に `.sigil-commit` に書くことを含める）。`.sigil-commit` が追跡されていれば（`ls-files --error-unmatch`）、`.gitignore` に入れるよう注意を出す（`TestCommitWarnsTrackedMessageFile`）
  2. フックがあれば、`client.Inspect(p.ContainerName)` が Running でなければ `loref("領域が開いていません", "sigil で領域を開いてから、もう一度実行してください")`
  3. pre-commit → TakeSnapshot → prepare-commit-msg / commit-msg（銘は `Normalize` し直す）。フックの実行は `u.Ritual("試練に挑みます", …, stages, work)` の中で行い、出力はためる。失敗したら `Escape` した出力を見せて `lore("試練に敗れました", …)`
  4. `CommitScreen` → `CommitPrompt` → 答え。`d` なら `git diff --no-color --no-ext-diff --no-textconv <Base> <Tree>` を `DiffPage` して `Pager` に渡し、画面からやり直す。肯定でなければ `lore("刻印を取りやめました", errors.New("中止しました"))`
  5. `Seal`（描画なし）→ `os.Remove(.sigil-commit)` → `Sealed("刻印しました", ブランチ, 短いハッシュ, 件名)`
  6. `FindUpstream`。なければ案内して nil。あれば `PushPrompt` → 答え → pre-push（あれば Ritual の中で）→ `Push`。push の失敗は `lore("解き放てませんでした", …)`（コミットは残っている旨を含める）
  - Snapshot の一時フォルダは `defer s.Cleanup()`
  - tty は承認と同じく `d.OpenTTY()`。1 回開いて、確認と push の両方の答えを同じ `bufio.Reader` から読む（`ReadAnswer` が `*bufio.Reader` も受けられるよう、引数は `io.Reader` のままで、呼ぶ側で包む）

- [ ] **Step 1: テスト環境を用意する**

`commit_test.go` に `newCommitEnv(t)`: `newEnv(t)` のプロジェクトで本物の `git init`（git がなければ skip。全体設定を読まない環境変数は Task 3 と同じ）。`d.Cmd` は、`git` なら本物の ExecRunner、`docker` なら記録して応答を返す偽物にする（`exec ...` の呼び出しは、引数の `<mode>` に応じて、成功／失敗と標準出力を返す）。`d.OpenTTY` は、Read の初回に `beforeAnswer` 関数を呼んでから、決めた答えを返す reader。

- [ ] **Step 2: 失敗するテストを書く**

- `TestCommitHappyPath`: 銘と変更を書き、tty `"y\n"`。コミットされ、`.sigil-commit` が消え、銘がそのまま。upstream がないので push を聞かない（標準エラーに `git push -u` の案内）
- `TestCommitNo`: `"n\n"` → コミットされない。`.sigil-commit` は残る。終了コード 1
- `TestCommitNotTerminal`: IsTerminal が偽 → エラー。git の commit-tree を呼ばない
- `TestCommitWorktreeChangedDuringPrompt`: `beforeAnswer` で a.txt を書き換え、`"y\n"`。コミットの a.txt は画面を出す前の内容
- `TestCommitDiffThenYes`: `"d\ny\n"` → Pager が 1 回呼ばれ、渡された内容に生の ESC がない（差分に ESC を含むファイルを用意する）。そのあとコミットされる
- `TestCommitHooksInContainer`: `.husky/_/pre-commit` と `commit-msg` を置き、`core.hooksPath=.husky/_`、コンテナは Running。docker の偽物に `exec -i -w <ContainerPath> <ContainerName> sh -c` で始まる呼び出しが pre-commit と msg の 2 回ある。msg の偽物が `"feat: changed"` を返すと、その銘でコミットされる。ホストで印のファイルが作られない（フックは印を作るスクリプト）
- `TestCommitHookFails`: pre-commit の偽物が失敗し、標準エラーに `"lint\x1b[31m"` を書く → コミットされない。画面に `lint^[[31m`
- `TestCommitHooksNeedContainer`: フックがあり、コンテナが Missing → 「領域が開いていません」。git の commit-tree を呼ばない
- `TestCommitRefusals`: include.path・MERGE_HEAD・gitlink・LFS のそれぞれで、tty を開かずにエラー
- `TestCommitPushFlow`: bare リポジトリに upstream を張り、`"y\ny\n"` → bare に届く。pre-push があれば docker の偽物に pre-push の呼び出しがあり、その標準入力が `PrePushInput` の 1 行
- `TestCommitPushDeclined`: `"y\nn\n"` → bare に届かない。コミットは残る。終了コード 0
- `TestCommitArgs`: `commit foo` → 使い方のエラー。`commit -- a.txt` → a.txt だけ

- [ ] **Step 3: 失敗を確かめる** — Run: `go test ./internal/app/ -run Commit` Expected: FAIL

- [ ] **Step 4: 実装する**

- [ ] **Step 5: 通ることを確かめる** — Run: `make check` Expected: test・vet・gofmt がすべて通る

- [ ] **Step 6: コミット（提案）** `sigil commit を足す (#13)`

---

### Task 10: 説明と実機での確認

**Files:**
- Modify: `README.md`、`internal/app/app.go`（usage に `sigil commit [-- <path>...]  コンテナで用意した銘で、確認してコミット・push〔刻印の儀〕`。Task 9 で済んでいれば不要）
- Create: `examples/sigil-commit.md`（コンテナの Claude Code に渡す説明のひな形。「コミットはできない。メッセージを `.sigil-commit` に書き、利用者に `sigil commit` を頼む」。プロジェクトの `CLAUDE.md` に写して使う）

- [ ] **Step 1: README に章を足す** — 使い方、`.sigil-commit` をプロジェクトの `.gitignore` に入れること、フックはコンテナで実行されホストでは実行されないこと、拒否される場合（作業ツリーから読み込まれる設定、マージなどの途中、入れ子のリポジトリ、LFS、プロジェクトの外のフック）、upstream がないと push しないこと
- [ ] **Step 2: ビルドする** — Run: `CGO_ENABLED=0 go build -trimpath -o bin/sigil .` Expected: 成功
- [ ] **Step 3: 実機で確かめる**（設計書 14 章の 4。`/dev/tty` が要るので、コマンドを示してユーザーに cmux のペインで実行してもらう。`PATH=/usr/local/bin:/usr/bin:/bin` を付ける）
  - 一時フォルダにテスト用プロジェクトと bare リポジトリを作り、`core.hooksPath=.husky/_` と、印を作る pre-commit / commit-msg / pre-push を置く
  - `SIGIL_IMAGE=sigil:dev-test ./bin/sigil` で領域を開き、中で `.sigil-commit` を書く
  - ホストで `./bin/sigil commit` を実行する。確かめること:
    - 印がコンテナの中でだけ作られる
    - フックが失敗するとコミットされない
    - 画面の見た目（Color / `NO_COLOR=1` / `SIGIL_PLAIN=1`）
    - push で bare に届く
  - lint-staged を入れたプロジェクトでも試し、`.git` が読み取り専用でもフックが動くかを報告する
- [ ] **Step 4: 後片付け** — 自分が作ったテスト用コンテナ、一時フォルダだけを消す
- [ ] **Step 5: コミット（提案）** `sigil commit の説明を足す (#13)`
