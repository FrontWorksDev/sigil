# プロジェクトごとのボリューム 実装計画（issue #7 フェーズ 1）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 全プロジェクト共有の `sigil-cache`・`sigil-go`・`sigil-cargo` を、ラベル付きのプロジェクトごとのボリューム（`<コンテナ名>-cache` / `-go` / `-cargo`）に変え、それらを確認のうえ消す `sigil purge` を足す。

**Architecture:**
- ボリュームの名前・マウント先の一覧、docker の問い合わせ（あるか・ラベル・作る・消す）は `internal/docker/volume.go` に置く。`RunArgs` は `ProjectVolumes` の一覧からマウントを作る。
- `app.ensureContainer` は `docker run` の前に `Client.EnsureVolumes` を呼ぶ。`sigil stop` はボリュームを消さずに案内を出す。`sigil purge` は `internal/app/purge.go`。
- ラベルの照合は、どこでも `{{json .Labels}}` を `encoding/json` で読み、バイトの完全一致で比べる。

**Tech Stack:** Go 1.27.4、標準ライブラリのみ、`CGO_ENABLED=0`、docker CLI。

**Spec:** `docs/superpowers/specs/2026-11-19-dev-cli-design.md`（以下「設計書」）の 21 章。あわせて 4・8・9・13・14・16 章と、2026-11-27 の決定事項（21 章のもの）。

## Global Constraints

- 外部モジュール禁止、cgo 禁止。`make check`（test + vet + gofmt）が通ること。
- ユーザー向けのメッセージ・エラーは日本語で `sigil: ` から始め、標準エラーへ。失敗は終了コード 1。エラーは `lore` / `loref`（1 行目が用語、2 行目が普通の言葉）。案内は `info`。
- コメントは日本語で「なぜ」を書く。特にセキュリティ上の理由（CSV を避ける、完全一致、`-f` なし など）。
- ラベルのキーは `sigil.project`（`docker.ProjectLabel` をそのまま使う）。値はプロジェクトのホスト側パス `p.Root`。比較はバイトの完全一致（`TrimSpace` しない）。
- ボリュームのラベルは `docker volume create --label sigil.project=<パス> <名前>` で付ける。`docker run --mount ...volume-label=` は使わない。
- 名前とパスを画面に出すときは `ui.EscapeLine` を通す。
- `sigil.layout` は `"3"`。
- 新しい環境変数は読まない（`config.deniedEnvs` に足すものはない）。
- テストで本物の docker を動かさない。実機の確認では `sigil:dev-test` と一時フォルダのプロジェクトだけを使い、既存の `sigil-*` のコンテナ・ボリューム・`sigil:latest` に触れない。sigil を実行するときは `PATH=/usr/local/bin:/usr/bin:/bin` を付ける。
- コミットは**ユーザーに確認してから**（CLAUDE.md）。各タスクの「コミット」は提案のタイミング。コミット後の security-guidance のフックの指摘は、対応するか理由を書き残す。

## Review Focus

- パスの末尾が空白・改行のプロジェクト: 末尾のないパスのボリュームと同じものとみなさない（Task 1 の `TestVolumeProjectExactBytes`）。
- `docker volume ls --filter name=` の部分一致: `sigil-proj-33bf5961-cache-old` が出ても「ある」とみなさない（Task 1 の `TestVolumeExistsExactName`）。
- Docker Desktop が止まっている: `volume ls` が失敗したら「ない」として作りに行かず、コンテナも作らない（Task 2 の `TestEnsureVolumesDockerDown`）。
- 別のペインが確認待ちのあいだにコンテナを作り直した: `docker volume rm` は `-f` なしで、失敗は案内付きのエラー（Task 4 の `TestPurgeVolumeRmFailure`）。
- フォルダ名に双方向の書式文字: purge の一覧に `<U+202E>` の形で出る（Task 4 の `TestPurgeEscapesDisplay`）。

---

### Task 1: ボリュームの一覧と docker の問い合わせ（`internal/docker`）

**Files:**
- Create: `internal/docker/volume.go`
- Create: `internal/docker/volume_test.go`
- Modify: `internal/docker/args.go`（`LayoutVersion`、`RunArgs` の共有ボリュームの 3 行）
- Modify: `internal/docker/client.go`（`ReadOnlyMounts` のラベル読みを `containerLabels` に切り出す）
- Test: `internal/docker/args_test.go`、`internal/docker/client_test.go`

**Interfaces:**
- Produces:
  - `type Volume struct { Name, Target string }`
  - `func ProjectVolumes(container string) []Volume` — 順に `{container+"-cache", "/home/dev/.cache"}`、`{container+"-go", "/home/dev/go"}`、`{container+"-cargo", "/home/dev/.cargo"}`
  - `func (c Client) VolumeExists(name string) (bool, error)` — `docker volume ls --filter name=<name> --format {{.Name}}` の行に完全一致があるか。失敗は「docker に接続できません。Docker Desktop が起動しているか確認してください（%w）」（`Inspect` と同じ文）
  - `func (c Client) VolumeProject(name string) (value string, ok bool, err error)` — `docker volume inspect --format {{json .Labels}} <name>` を JSON で読み、`labels[ProjectLabel]` を返す。`null` はラベルなし（`ok=false`）
  - `func (c Client) CreateVolume(name, projectRoot string) error` — argv は `volume create --label sigil.project=<projectRoot> <name>`
  - `func (c Client) RemoveVolumes(names []string) error` — argv は `volume rm <names...>`（`-f` なし）
  - `func (c Client) ContainerProject(name string) (string, error)` — コンテナのラベル `sigil.project`（`containerLabels` を使う）
  - `func (c Client) containerLabels(name string) (map[string]string, error)`（非公開。`ReadOnlyMounts` と共用）

- [ ] **Step 1: 失敗するテストを書く**（`volume_test.go`、既存の `fakeRunner` を使う）

```go
func TestProjectVolumes(t *testing.T) {
	got := ProjectVolumes("sigil-proj-33bf5961")
	want := []Volume{
		{"sigil-proj-33bf5961-cache", "/home/dev/.cache"},
		{"sigil-proj-33bf5961-go", "/home/dev/go"},
		{"sigil-proj-33bf5961-cargo", "/home/dev/.cargo"},
	}
	// reflect.DeepEqual(got, want)
}
// TestVolumeExistsExactName: 出力 "sigil-proj-33bf5961-cache-old\n" → false。"x\nsigil-proj-33bf5961-cache\n" → true。
//   argv == ["volume","ls","--filter","name=sigil-proj-33bf5961-cache","--format","{{.Name}}"]
// TestVolumeExistsDockerDown: Runner がエラー → err != nil、メッセージに "docker に接続できません"
// TestVolumeProjectExactBytes: 出力 `{"sigil.project":"/p/a "}`+"\n" → value "/p/a "（末尾の空白を残す）。
//   `{"sigil.project":"/p/a\n"}` → "/p/a\n"。"null\n" → ok == false。`{}` → ok == false。壊れた JSON → err
// TestCreateVolumeArgs: projectRoot "/x/a,dst=/etc b=c\t日本" で argv が
//   ["volume","create","--label","sigil.project=/x/a,dst=/etc b=c\t日本","sigil-p-1-cache"]（ラベルは 1 要素）
// TestRemoveVolumesNoForce: argv == ["volume","rm","a","b"]、"-f" を含まない
// TestContainerProject: `{"sigil.project":"/p","sigil.layout":"3"}` → "/p"
```

`args_test.go` に足す:

```go
// TestRunArgsPerProjectVolumes: RunArgs の引数に "sigil-cache:"・"sigil-go:"・"sigil-cargo:" で始まる値がなく、
//   "-v" の後ろに s.Name+"-cache:/home/dev/.cache"、s.Name+"-go:/home/dev/go"、s.Name+"-cargo:/home/dev/.cargo" がある。
//   "sigil-nvim-data:"・"sigil-nvim-state:"・"sigil-claude:" は残っている。
// TestRunArgsLayout3: "--label" の後ろに "sigil.layout=3"
```

- [ ] **Step 2: 失敗を確かめる**

Run: `go test ./internal/docker/`
Expected: FAIL（`ProjectVolumes` などが未定義）

- [ ] **Step 3: `volume.go` を実装し、`RunArgs` の共有 3 行を `for _, v := range ProjectVolumes(s.Name) { args = append(args, "-v", v.Name+":"+v.Target) }` に置き換え、`LayoutVersion = "3"` にする**

コメントに書くこと: プロジェクトごとにする理由（設計書 21 章の表の要約）、`--mount` を使わない理由（CSV）、`volume inspect` で有無を判定しない理由、`-f` を付けない理由。`ReadOnlyMounts` は `containerLabels` を使う形に直す（振る舞いは変えない）。

- [ ] **Step 4: 通ることを確かめる**

Run: `go test ./internal/docker/`
Expected: PASS（既存の `TestRunArgsMounts` などが共有ボリュームの名前を前提にしていれば、21 章に合わせて直す）

- [ ] **Step 5: コミット（ユーザーに確認してから）**

```bash
git add internal/docker/
git commit -m "docker: キャッシュ類のボリュームをプロジェクトごとにし、ラベルを読み書きする (#7)"
```

---

### Task 2: 作るときにボリュームを用意する（`EnsureVolumes` と `ensureContainer`）

**Files:**
- Modify: `internal/docker/volume.go`、`internal/docker/volume_test.go`
- Modify: `internal/app/app.go`（`ensureContainer` のイメージの確認の後・`spec` を作る前）
- Modify: `internal/app/fake_test.go`（`newEnv` の既定の応答、`container` の既定の layout）
- Test: `internal/app/container_test.go`

**Interfaces:**
- Consumes: Task 1 のすべて
- Produces:
  - `type ForeignVolumeError struct { Name string }`（`Error()` は「ボリューム <Name> は、このプロジェクトのために sigil が作ったものではありません。中身を確かめてから docker volume rm で消してください」。名前は `ui` を import しないので、表示する側で `EscapeLine` する。docker パッケージでは名前だけを持つ）
  - `func (c Client) EnsureVolumes(container, projectRoot string) error` — `ProjectVolumes(container)` の順に、`VolumeExists` → なければ `CreateVolume` → `VolumeProject` が `(projectRoot, true)` でなければ `&ForeignVolumeError{Name}`。docker の失敗はそのまま返す

- [ ] **Step 1: 失敗するテストを書く**

`volume_test.go`:

```go
// TestEnsureVolumesCreatesMissing: ls が空、inspect が {"sigil.project":"/p"} → create が 3 回、
//   それぞれ ["volume","create","--label","sigil.project=/p",<name>]。err == nil
// TestEnsureVolumesReusesMatching: ls が名前を返す → create を呼ばない
// TestEnsureVolumesForeign: ls が名前を返し、inspect が `{"sigil.project":"/other"}` / "null" / `{"sigil.project":"/p "}`
//   → errors.As(err, &fe) && fe.Name == "<container>-cache"。2 つ目以降のボリュームに進まない
// TestEnsureVolumesDockerDown: ls がエラー → err、create を呼ばない
```

`container_test.go`:

```go
// TestCreatePreparesVolumesBeforeRun: 新しいコンテナを作ると、"docker volume create" が "docker run" より前に 3 回呼ばれ、
//   ラベルが "sigil.project="+e.proj（realTemp のパス）
// TestForeignVolumeStopsBeforeRun: inspect が `{}` を返す → 終了コード 1、"docker run" を呼ばない、
//   stderr に "領域の残響が別のものと混ざっています" と "<コンテナ名>-cache"
```

- [ ] **Step 2: 失敗を確かめる**

Run: `go test ./internal/docker/ ./internal/app/`
Expected: FAIL

- [ ] **Step 3: `EnsureVolumes` を実装し、`ensureContainer` から呼ぶ**

`ensureContainer` では、`client.ImageExists` の確認の直後に呼ぶ（承認と `op run` の後なので、拒否されたプロジェクトや inner に置き換わる前にボリュームを作らない）。`ForeignVolumeError` なら `loref("領域の残響が別のものと混ざっています", "ボリューム %s は、このプロジェクトのために sigil が作ったものではありません。中身を確かめてから docker volume rm で消してください", ui.EscapeLine(fe.Name))`、それ以外のエラーは `lore("領域の様子を確かめられません", err)`。

`fake_test.go` の `newEnv` に既定の応答を足す: `docker volume ls` は `""`（ない）、`docker volume inspect` は `{"sigil.project":"<e.proj>"}`（`json.Marshal` で作る）。既存のテストで `e.container("running", "2")` としているものは `"3"` に直す（古い layout のテストは `"2"` を使う）。

- [ ] **Step 4: 通ることを確かめる**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: コミット（ユーザーに確認してから）**

```bash
git add internal/
git commit -m "コンテナを作る前に、ラベルを確かめてプロジェクトごとのボリュームを用意する (#7)"
```

---

### Task 3: `sigil stop` の案内

**Files:**
- Modify: `internal/app/app.go`（`stop`）
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: `docker.ProjectVolumes`、`Client.VolumeExists`

- [ ] **Step 1: 失敗するテストを書く**

```go
// TestStopKeepsVolumes: コンテナあり、"docker volume ls" が "<name>-cache\n" を返す →
//   "docker volume rm" を呼ばない。stderr に "領域の残響は残しています。消すときは sigil purge"
// TestStopNoVolumesNoHint: volume ls が空 → 案内を出さない
// TestStopVolumeCheckFailureIsQuiet: volume ls がエラー → 終了コード 0（コンテナは消せている）、案内なし
```

- [ ] **Step 2: 失敗を確かめる**

Run: `go test ./internal/app/ -run TestStop`
Expected: FAIL

- [ ] **Step 3: `stop` の「領域 %s を解除しました」の後で、`ProjectVolumes` のどれかが `VolumeExists` なら `info(d, "領域の残響は残しています。消すときは sigil purge")`**

コンテナがない（「はありません」）ときも、ボリュームがあれば同じ案内を出す。確かめられないときは黙る（`stop` の目的は果たしているので）。

- [ ] **Step 4: 通ることを確かめる**

Run: `go test ./internal/app/`
Expected: PASS

- [ ] **Step 5: コミット（ユーザーに確認してから）**

```bash
git add internal/app/
git commit -m "sigil stop で、ボリュームが残っていることと消し方を知らせる (#7)"
```

---

### Task 4: `sigil purge`

**Files:**
- Create: `internal/app/purge.go`
- Create: `internal/app/purge_test.go`
- Modify: `internal/app/app.go`（`run` の振り分けに `case "purge"`、`usage` に 1 行）

**Interfaces:**
- Consumes: Task 1・2 のすべて、`Client.Inspect`、`Client.Remove`、`Deps.OpenTTY`、`ui.ReadAnswer`、`ui.IsYes`、`ui.EscapeLine`
- Produces: `func purge(d Deps, client docker.Client, p project.Project) error`

振り分けは `stop` と同じ場所（プロジェクトを特定したあと、`--` より前）。`sigil -- purge` はコンテナの中で実行される（既存の `--` の扱いのまま）。

`usage` に足す行（`sigil stop` の次）:

```
  sigil purge             このプロジェクトのコンテナとボリュームを、確かめてから削除〔領域の浄化〕
```

`usage` の `sigil stop` の行は「このプロジェクトのコンテナを削除（ボリュームは残す。.sigil を変えたら一度これ）〔領域解除〕」にする。

手順（設計書 21 章「`sigil purge`」のとおり）:
1. `Inspect` でコンテナの有無。あれば `ContainerProject` が `p.Root` と完全一致するときだけ対象。違えば `info(d, "注意: コンテナ %s は、このプロジェクトのものではないので消しません", EscapeLine(name))`。
2. `ProjectVolumes` それぞれ `VolumeExists` → `VolumeProject` が `(p.Root, true)` のものだけ対象。違えば同じ形の注意（「ボリューム %s は…」）。docker の失敗は `lore("領域の様子を確かめられません", err)`。
3. 対象がなければ `info(d, "領域 %s に浄化するものはありません", EscapeLine(p.ContainerName))` で nil。
4. 一覧を標準エラーに出す（`sigil: 次のものを消します。消したものは戻せません` に続けて、`  コンテナ  <name>`、`  ボリューム  <name>` を 1 行ずつ、`  プロジェクト  <p.Root>`。どれも `EscapeLine`）。続けて `浄化しますか？ [y/N] `。
5. `OpenTTY` が失敗 → `loref("浄化を確かめられません", "対話できるターミナルで実行してください")`。`IsYes(ReadAnswer(tty))` でなければ `info(d, "浄化しませんでした")` で nil。
6. コンテナが対象なら `Remove`。
7. 各ボリュームのラベルを `VolumeProject` で確かめ直し、合うものだけ `RemoveVolumes`。失敗 → `loref("領域を浄化できませんでした", "ボリューム %s を削除できませんでした。ほかのペインで sigil を使っていないか確かめてください: %w", 名前を ", " でつないで EscapeLine, err)`。
8. `info(d, "領域 %s を浄化しました", EscapeLine(p.ContainerName))`。

- [ ] **Step 1: 失敗するテストを書く**（`purge_test.go`。`newEnv` の既定は「コンテナなし・ボリュームなし」にして、テストごとに応答を足す。`e.tty` に答えを入れる）

```go
// TestPurgeNothing: コンテナもボリュームもない → 終了コード 0、OpenTTY を呼ばない（e.ttyOpened == 0）、
//   stderr に "浄化するものはありません"
// TestPurgeNoTTY: ボリュームあり、e.ttyErr != nil → 終了コード 1、"docker rm"・"docker volume rm" を呼ばない
// TestPurgeAnswerNo: e.tty = "n\n" → 終了コード 0、何も消さない、"浄化しませんでした"
// TestPurgeYes: コンテナ（ラベル一致）とボリューム 3 つ（ラベル一致）、e.tty = "y\n" →
//   "docker rm -f <name>" が "docker volume rm" より前。volume rm の argv は
//   ["volume","rm",<name>-cache,<name>-go,<name>-cargo] で "-f" なし。stderr に "浄化しました"
// TestPurgeSkipsForeign: <name>-go のラベルが "/other"、コンテナのラベルが "/other" →
//   volume rm の argv に <name>-go がない。"docker rm" を呼ばない。stderr に注意が 2 つ
// TestPurgeIgnoresPrefixMatch: volume ls（name= の部分一致）が "<name>-cache-x\n" だけを返す →
//   対象なし（"浄化するものはありません"）
// TestPurgeVolumeRmFailure: volume rm がエラー → 終了コード 1、stderr に "ほかのペインで sigil を使っていないか"
// TestPurgeRechecksLabelBeforeRemove: 1 回目の inspect は一致、確認のあとの inspect（onFunc で回数を数える）は "/other" →
//   そのボリュームを rm に渡さない
// TestPurgeEscapesDisplay: プロジェクトのフォルダ名に "‮" を含める → stderr に "<U+202E>" が出て、生の U+202E は出ない
//   （Edit ツールで書くと実際の文字になることがあるので、Go の文字列では必ず "‮" のエスケープで書く）
// TestDashDashPurgeRunsInContainer: e.run("--", "purge") → Exec の argv の末尾が "purge"、"docker volume rm" を呼ばない
```

- [ ] **Step 2: 失敗を確かめる**

Run: `go test ./internal/app/ -run 'Purge'`
Expected: FAIL（`purge` が未定義、または振り分けがない）

- [ ] **Step 3: `purge.go` を実装し、振り分けと `usage` を足す**

- [ ] **Step 4: 通ることを確かめる**

Run: `make check`
Expected: test・vet・gofmt がすべて通る

- [ ] **Step 5: コミット（ユーザーに確認してから）**

```bash
git add internal/app/
git commit -m "sigil purge: 確かめてから、このプロジェクトのコンテナとボリュームを消す (#7)"
```

---

### Task 5: Dockerfile のコメント、README、設計書の残り

**Files:**
- Modify: `image/Dockerfile`（gh の節の「共有の sigil-cache の中になるので」の 2 行。`~/.cache` はプロジェクトごとになったが、ログをディスクに残さないために tmpfs を残す、と書き直す）
- Modify: `internal/docker/args.go`（`--tmpfs` のコメントも同じ趣旨に）
- Modify: `README.md`
- Modify: `CLAUDE.md`（「全プロジェクト共通のボリュームを足すときは…」の行に、今の共通ボリュームは `sigil-claude`・`sigil-nvim-*` だけで、キャッシュ類はプロジェクトごと（設計書 21 章）と添える）

README に書くこと（設計書 21 章「README」のとおり）:
- `sigil purge` の使い方と `sigil stop` との違い
- 移行: 全プロジェクトで `sigil stop` → 入り直す。`docker ps -a --filter label=sigil.layout=2` で古いコンテナがないことを確かめてから `docker volume rm sigil-cache sigil-go sigil-cargo`（中身は移さない）。古い sigil のバイナリを消す
- `sigil-claude`・`sigil-nvim-*` の作り直し方（ログインとプラグインの入れ直しになる）
- ディスクの使用量と `docker system df -v`
- 大文字小文字など別の書き方でフォルダを開くと別のプロジェクトになること

- [ ] **Step 1: 上の 4 ファイルを直す**
- [ ] **Step 2: `make check` と `CGO_ENABLED=0 go build -trimpath -o bin/sigil .` が通る**（`image/embed_test.go` が Dockerfile を見ているので、テストも通ること）
- [ ] **Step 3: コミット（ユーザーに確認してから）**

```bash
git add image/Dockerfile internal/docker/args.go README.md CLAUDE.md docs/
git commit -m "README と設計書: プロジェクトごとのボリュームと sigil purge (#7)"
```

---

### Task 6: 実機での確認（設計書 21 章「実機での検証」）

**Files:** なし（結果を報告する。問題があれば該当のタスクに戻る）

- [ ] **Step 1: `SIGIL_IMAGE=sigil:dev-test PATH=/usr/local/bin:/usr/bin:/bin ./bin/sigil build`**
- [ ] **Step 2: 一時フォルダ（scratchpad の下）に `git init` したプロジェクト A・B を作り、21 章の 1〜4・6 を Claude の Bash で行う**（`docker volume inspect` は A・B の名前だけ。6 で作ったラベルなしのボリュームは確かめたあと名前を指定して消す）
- [ ] **Step 3: 21 章の 5（`sigil purge`）は `/dev/tty` が要るので、コマンドをユーザーに示して cmux のペインで実行してもらう**（`n` → 何も消えない、`y` → A だけ消える、B と既存の `sigil-*` が残る）
- [ ] **Step 4: 後片付け**: B のコンテナとボリューム（`sigil purge` をユーザーに、または名前を確かめて `docker rm -f` / `docker volume rm`）、`sigil:dev-test`、一時フォルダ。既存の `sigil-*` には触れない
- [ ] **Step 5: 結果を報告し、`/security-review` を提案する**（CLAUDE.md の「PR の前」）
