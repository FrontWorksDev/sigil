# Claude Code の設定をプロジェクトごとにする 実装計画（issue #7 フェーズ 2）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `~/.claude` をプロジェクトごとのボリューム `<コンテナ名>-claude` にし、ホストの `~/.config/sigil/claude/` を、確かめたうえで `/etc/claude-code` に読み取り専用でマウントする。あわせて、相対パスの `XDG_*` を無視する。

**Architecture:**
- 共通の設定のフォルダの確かめ（作成時と起動前）は `internal/project/claude.go`（純粋関数とファイルシステムだけ。docker は呼ばない）。
- `docker.RunSpec.ClaudeShared` が空でなければ `RunArgs` が `-v` を足す。ラベル `sigil.claude` は常に付ける。
- `app.ensureContainer` は `docker run` の直前に確かめ、`useExisting` は停止中のコンテナを起動する前にラベルのパスを確かめ直す。
- 初回の画面を省く `.claude.json` は Dockerfile で置く（sigil のコードは変えない）。

**Tech Stack:** Go 1.27.4、標準ライブラリのみ、`CGO_ENABLED=0`、docker CLI。

**Spec:** `docs/superpowers/specs/2026-11-19-dev-cli-design.md` の 22 章と 6 章（`XDG_*`）、2026-11-27 の決定事項（22 章のもの）。21 章のボリュームの仕組みをそのまま使う。

## Global Constraints

- 外部モジュール禁止、cgo 禁止。`make check` が通ること。
- メッセージ・エラーは日本語で `sigil: ` から始め、`lore` / `loref`。表示するパスは `ui.EscapeLine` を通す。ファイルシステムのエラーは `fsReason` の形（リンクの先のパスを出さない）。
- コメントは日本語で「なぜ」を書く。
- ラベルのキー: `sigil.claude`（`docker.ClaudeLabel`）。値は解決したパス、なければ空文字。
- マウント: `-v <解決したパス>:/etc/claude-code:ro`。`--mount` は使わない。
- ボリューム: `<コンテナ名>-claude` → `/home/dev/.claude`。`sigil-claude` は渡さない。`sigil.layout` は `"4"`。
- 新しい環境変数は読まない（`XDG_*` は既に `deniedPrefixes` にある）。
- `image/Dockerfile` に `ENV` は足さない。
- 実機の確認は `sigil:dev-test` と一時フォルダ、一時の `XDG_CONFIG_HOME` だけ。ホストの `~/.config/sigil` と `~/.claude` に触れない。sigil の実行には `PATH=/usr/local/bin:/usr/bin:/bin`。
- コミットはユーザーに確認してから。

## Review Focus

- `~/.config` がシンボリックリンクの dotfiles 構成: リンクの先の `sigil/claude` でも通り、リンクの先の `gh` などには届かない（Task 2 の `TestClaudeSharedThroughConfigSymlink`）。
- フォルダはあるが空: エラーにせずマウントする（Task 2 の `TestClaudeSharedEmptyDir`）。
- 作成時にフォルダがなく、あとで作った: 起動前の確かめは何もしない（ラベルが空）が、マウントされないことを案内する（Task 4 の `TestExistingWithoutClaudeSharedHints`）。
- フォルダ名に表示されない文字: エラーに `%q` で出す（Task 2 の `TestClaudeSharedUnprintable`）。
- 深い階層のファイル（深さ 9 以上）: そこより下はたどらずエラー（Task 2 の `TestClaudeSharedTooDeep`）。

---

### Task 1: `XDG_CONFIG_HOME` / `XDG_STATE_HOME` は絶対パスだけ使う

**Files:**
- Modify: `internal/app/app.go`（`newDirs`）
- Test: `internal/app/app_test.go`

- [ ] **Step 1: 失敗するテスト**

```go
// TestRelativeXDGIgnored: e.vars["XDG_CONFIG_HOME"] = "rel/config"、e.vars["XDG_STATE_HOME"] = "rel/state" で、
//   プロジェクトの中に rel/config/sigil/config（"env LEAK\n"）を置き、e.vars["LEAK"]="x" → docker run の引数に "LEAK" がない。
//   ホームの .config/sigil/config（"env HOMECFG\n"）と e.vars["HOMECFG"]="y" → "HOMECFG" がある。
// TestEmptyXDGIgnored: XDG_CONFIG_HOME="" でも既定を使う（同じ形）。
```

- [ ] **Step 2: `go test ./internal/app/ -run XDG` → FAIL**
- [ ] **Step 3: `newDirs` で `filepath.IsAbs` でない値（空を含む）は既定に置き換える**（コメントに 6 章の理由）
- [ ] **Step 4: `go test ./internal/app/` → PASS**
- [ ] **Step 5: コミット（確認してから）** `XDG_CONFIG_HOME / XDG_STATE_HOME は絶対パスのときだけ使う (#7)`

---

### Task 2: 共通の設定のフォルダの確かめ（`internal/project/claude.go`）

**Files:**
- Create: `internal/project/claude.go`、`internal/project/claude_test.go`

**Interfaces:**
- Produces:
  - `func ResolveClaudeShared(dir, home, sigilConfig string, extra ...string) (resolved string, warnings []string, err error)` — `dir` がなければ `("", nil, nil)`。あれば 22 章「共通の設定」の確かめをすべて行い、解決したパスを返す。`warnings` はハードリンクの注意（相対パスで 1 件ずつ、`ui` は使わず素の文字列）。`sigilConfig` は `$XDG_CONFIG_HOME/sigil`、`extra` は守る場所（`dd.state`）。
  - `func VerifyClaudeShared(path, home, sigilConfig string, extra ...string) error` — 起動前の確かめ。先頭から 1 段ずつ `Lstat`（`VerifyMounts` と同じ）、リンク・ない・フォルダでない・持ち主・`g+w`/`o+w`・守る場所。変わっていれば `ErrMountChanged` を包む。中はたどらない。
- 守る場所の判定（両方の関数で共用する非公開の `checkClaudeProtected(resolved, home, sigilConfig string, extra []string) error`）:
  - `home` そのもの・`home` を含む → エラー
  - `home/.claude`、`home/.claude.json`、`home/.ssh`、`.aws`、`.gnupg`、`.docker`、`Library`、`extra` の各場所（リンクを解決した実体も）について、同じ・中・含む → エラー
  - `home/.config`（と実体）の中にあるなら、`sigilConfig`（と実体）の中でなければエラー（`~/.config/gh` などを見せない）

- [ ] **Step 1: 失敗するテスト**（一時フォルダの本物のファイルシステムで。`home` は一時フォルダ）

```go
// TestClaudeSharedMissing: dir がない → ("", nil, nil)
// TestClaudeSharedEmptyDir: 空のフォルダ → resolved == EvalSymlinks(dir)、err == nil
// TestClaudeSharedOK: CLAUDE.md・.claude/skills/x/SKILL.md・managed-settings.json（0644、フォルダ 0755） → OK
// TestClaudeSharedRejects（表）: それぞれ err != nil
//   - dir がシンボリックリンク（先は一時フォルダの実体）
//   - dir がファイル
//   - dir が 0775（g+w）/ 0757（o+w）
//   - 中のファイルが 0666
//   - 中にシンボリックリンク（.claude/skills/x -> 別の場所）
//   - 上の階層: sigilConfig を home/.claude へのリンクにする（resolved が home/.claude の中）
//   - 解決したパスに ":" を含む
// TestClaudeSharedThroughConfigSymlink: home/.config -> base/dotfiles/config のリンクで、
//   dotfiles/config/sigil/claude は OK。dotfiles/config/sigil/claude を dotfiles/config/gh へのリンクにした場合
//   （= sigilConfig の外）→ エラー（dir 自体がリンクなので弾かれるのとは別に、sigil を gh にリンクした場合も確かめる）
// TestClaudeSharedHardlinkWarns: 中に Nlink=2 のファイル → err == nil、len(warnings) == 1
// TestClaudeSharedTooDeep: 深さ 9 のフォルダ → エラー
// TestClaudeSharedUnprintable: フォルダ名に "\x1b" → err の文字列に生の ESC がない
// TestVerifyClaudeShared: 作成時に通ったパスで OK。そのあと途中をリンクに差し替え → errors.Is(err, ErrMountChanged)。
//   消した → エラー。chmod g+w → エラー。
```

持ち主の確かめは `fi.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid())`（テストでは自分の持ち物になるので、違う場合のテストは書かない）。

- [ ] **Step 2: `go test ./internal/project/ -run ClaudeShared` → FAIL**
- [ ] **Step 3: 実装**（中のたどりは `filepath.WalkDir` ではなく、深さを数える再帰で `os.Lstat` / `os.ReadDir`。リンクはたどらない。エラーには `checkPrintable` と `fsReason` を使う）
- [ ] **Step 4: `go test ./internal/project/` → PASS**
- [ ] **Step 5: コミット（確認してから）** `Claude Code の共通の設定のフォルダを確かめる (#7)`

---

### Task 3: docker の引数とボリューム

**Files:**
- Modify: `internal/docker/args.go`、`internal/docker/volume.go`、`internal/docker/client.go`
- Test: `internal/docker/args_test.go`、`internal/docker/volume_test.go`、`internal/docker/client_test.go`

**Interfaces:**
- Produces:
  - `const ClaudeLabel = "sigil.claude"`、`LayoutVersion = "4"`
  - `RunSpec.ClaudeShared string`（解決したパス。空ならマウントしない）
  - `ProjectVolumes` が 4 つ目に `{container+"-claude", "/home/dev/.claude"}` を返す
  - `func (c Client) ClaudeShared(name string) (string, error)` — `containerLabels` から `ClaudeLabel` の値（なければ空）

- [ ] **Step 1: 失敗するテスト**

```go
// TestRunArgsClaudeShared: ClaudeShared="/x/sigil/claude" → "-v" に "/x/sigil/claude:/etc/claude-code:ro"、
//   "--label" に "sigil.claude=/x/sigil/claude"。空 → "/etc/claude-code" を含む -v がなく、ラベルは "sigil.claude="。
// TestRunArgsPerProjectVolumes を直す: "sigil-claude:" がなく、s.Name+"-claude:/home/dev/.claude" がある。
// TestProjectVolumes を直す: 4 つ。TestRunArgsLayout3 → TestRunArgsLayout4。
// TestClaudeSharedLabel: `{"sigil.claude":"/x"}` → "/x"、`{}` → ""。
```

- [ ] **Step 2: `go test ./internal/docker/` → FAIL**
- [ ] **Step 3: 実装**（`RunArgs` の共有の `-v sigil-claude:...` を消す。ラベルは `ReadOnlyLabel` と同じく常に付ける理由をコメントに）
- [ ] **Step 4: `go test ./internal/docker/` → PASS**（app のテストは Task 4 で直す）
- [ ] **Step 5: コミットは Task 4 と一緒**（app のテストが layout 4 で落ちるため。Ruling として台帳に残す）

---

### Task 4: app の組み込み（作成時と起動前）

**Files:**
- Modify: `internal/app/app.go`（`ensureContainer`、`useExisting`）
- Modify: `internal/app/fake_test.go`（`container` の既定の layout を `docker.LayoutVersion` に）と、`"3"` で作っている既存のテスト
- Test: `internal/app/claude_test.go`（新規）

**Interfaces:**
- Consumes: Task 2 の 2 関数、Task 3 の `RunSpec.ClaudeShared`・`Client.ClaudeShared`

- `ensureContainer`: `recheckMounts` のあと、`EnsureVolumes` の前に `project.ResolveClaudeShared(filepath.Join(dd.config, "claude"), home, dd.config, dd.state)`。エラー → `loref("神託が届きません", "%s を Claude Code の共通の設定として渡せません（%w）", ui.EscapeLine(dir), err)`。`warnings` は `info(d, "注意: %s はハードリンクです。リンク先の中身も全コンテナから読めます", ui.EscapeLine(w))`。結果を `spec.ClaudeShared` に。
- `useExisting`: 停止中のとき、`verifyExistingMounts` の後に `client.ClaudeShared(name)` を読み、空でなければ `project.VerifyClaudeShared`。失敗 → `loref("神託が歪められました", "%s を起動しません（%w）。sigil stop してから、もう一度実行してください", ...)`（`ErrMountChanged` でなければ題を「神託を確かめられません」）。ラベルが空で、`dd.config/claude` があれば `info(d, "注意: この領域は神託なしで展開されています。使うときは sigil stop してから入り直してください")`（動いているコンテナでも出す）。

- [ ] **Step 1: 失敗するテスト**（`claude_test.go`。`e.home` の下に `.config/sigil/claude` を作る）

```go
// TestClaudeSharedMounted: フォルダあり → runArgs に "<resolved>:/etc/claude-code:ro"
// TestClaudeSharedAbsentNoMount: フォルダなし → "/etc/claude-code" を含まない
// TestClaudeSharedSymlinkStops: フォルダがリンク → 終了コード 1、docker run なし、stderr に "神託が届きません"
// TestClaudeSharedCheckedBeforeVolumes: リンクのとき "docker volume create" も呼ばない
// TestExistingStoppedVerifiesClaudeShared: 停止中のコンテナのラベル sigil.claude が、今はリンクになったパス → 終了コード 1、
//   "docker start" なし、stderr に "神託が歪められました"
// TestExistingStoppedClaudeSharedOK: ラベルのパスがそのまま → start する
// TestExistingWithoutClaudeSharedHints: ラベル空、フォルダあり → 案内を出し、入る（終了コード 0）
// TestClaudeVolumeCreated: docker volume create に <name>-claude がある
```

`e.container` の `container inspect` の応答に `sigil.claude` を足す引数を用意する（`e.containerWithLabels(state, labels map[string]string)` を足し、`container` はそれを使う）。

- [ ] **Step 2: `go test ./internal/app/ -run 'Claude'` → FAIL**
- [ ] **Step 3: 実装と、既存のテストの layout を `"4"` に**
- [ ] **Step 4: `make check` → PASS**
- [ ] **Step 5: コミット（確認してから）** `~/.claude をプロジェクトごとにし、共通の設定を /etc/claude-code に読み取り専用で渡す (#7)`（Task 3 も含む）

---

### Task 5: イメージと sigil-check

**Files:**
- Modify: `image/Dockerfile`（Claude Code のインストールの `RUN` の後ろに 1 つ）
- Modify: `image/embed_test.go`
- Modify: `image/sigilcheck/check.go`（`barrier`）、`image/sigilcheck/check_test.go`

- Dockerfile: インストーラーの後に `RUN printf '%s' '{"hasCompletedOnboarding":true}' > /home/${USERNAME}/.claude/.claude.json && chmod 644 ...`（`USER` は dev のままなので持ち主は dev）。コメントに 22 章「初回の画面」の理由と、インストーラーより後に置く理由。
- `barrier`: `/etc/claude-code` が `isDir` なら `writeItem(s.tryWrite("/etc/claude-code"), "神託（Claude Code の共通設定）は読み取り専用", "神託（Claude Code の共通設定）に書き込める", "神託（Claude Code の共通設定）に書き込めるか確かめられない")`。

- [ ] **Step 1: 失敗するテスト**

```go
// embed_test: TestDockerfileSeedsClaudeOnboarding — Dockerfile に "hasCompletedOnboarding" があり、
//   その行が "claude.ai/install.sh" の行より後ろ。
// check_test の TestBarrier に 3 行: /etc/claude-code が EROFS → "✓ 神託（Claude Code の共通設定）は読み取り専用"、
//   書ける → "✗ 神託（Claude Code の共通設定）に書き込める"、EACCES → "✗ ...確かめられない"。ないとき → 項目なし。
//   golden を使うテストがあれば、ないときの出力が変わらないことを確かめる。
```

- [ ] **Step 2: `go test ./image/...` → FAIL**
- [ ] **Step 3: 実装**
- [ ] **Step 4: `make check` → PASS**
- [ ] **Step 5: コミット（確認してから）** `イメージに初回の画面を省く .claude.json を置き、sigil check で共通設定を点検する (#7)`

---

### Task 6: README と設計書のほかの章

**Files:** `README.md`、`docs/superpowers/specs/2026-11-19-dev-cli-design.md`（8 章の引数の一覧と `sigil.layout`、11 章・21 章の「共有のまま残る `sigil-claude`」）、`CLAUDE.md`（共通のボリュームは `sigil-nvim-*` だけ、と直す）

README に書くこと（22 章「README」と「ログイン」「既存のコンテナと移行」のとおり）:
- 共通の設定の置き方（例: `CLAUDE.md`、`.claude/skills/<名前>/SKILL.md`、`managed-settings.json` に `managedMcpServers` と `extraKnownMarketplaces` / `enabledPlugins` の例）、リンクではなく実体を置くこと、`managed-settings.json` の注意 4 点、画面の中で入れたものはそのプロジェクトだけ
- ログイン: `claude setup-token`、全体設定の `env CLAUDE_CODE_OAUTH_TOKEN` と `secrets.env` の `keychain://` / `op://`。トークンの有効期限と権限は公式の資料で確かめて書く。全プロジェクトに同じトークンが渡ること、取り替えたら作り直すこと、claude.ai のコネクタは使えないこと
- 移行: 古いコンテナを止める、`sigil-claude` のログインを claude.ai 側で取り消す、`sigil-claude` を残す場合の注意（`CLAUDE_CONFIG_DIR` に向けない、写さない）、要らなくなったら消す
- 「GitHub MCP（リモート版）は勧めません」の節を、今の状態（`~/.claude` はプロジェクトごと、共通の設定は全コンテナから読める）に合わせて直す
- `XDG_CONFIG_HOME` は絶対パスで設定すること

- [ ] **Step 1: 直す**
- [ ] **Step 2: `make check` と `CGO_ENABLED=0 go build -trimpath -o bin/sigil .`**
- [ ] **Step 3: コミット（確認してから）** `README と設計書: Claude Code の共通の設定とログイン、移行 (#7)`

---

### Task 7: 実機での確認（22 章「実機での検証」）

- [ ] **Step 1:** `SIGIL_IMAGE=sigil:dev-test` でビルドし、イメージの `/home/dev/.claude` に `.claude.json`（`hasCompletedOnboarding` だけ）しかないことを確かめる
- [ ] **Step 2:** 一時の `XDG_CONFIG_HOME` に共通の設定を置き、プロジェクト A・B で 22 章の 1・2・4・5 と、停止中のコンテナのリンク差し替えを確かめる（Claude の Bash で）
- [ ] **Step 3:** `/dev/tty` が要るもの（`claude` の起動でテーマの画面が出ないこと、`/memory`・skills・`/mcp` に効いていること、`sigil purge`）は、コマンドを示して利用者に cmux のペインで確かめてもらう
- [ ] **Step 4:** 後片付け（自分が作ったコンテナ・ボリューム・`sigil:dev-test`・一時フォルダだけ）
- [ ] **Step 5:** 最後のコードレビューと `/security-review` を提案する
