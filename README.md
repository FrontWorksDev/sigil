# sigil

> seal your code, guard your keys

npm / PyPI などのサプライチェーン攻撃や悪意あるリポジトリから、ホストの鍵を守るための CLI です。

- プロジェクトのコードを動かすもの（Neovim の LSP、Claude Code、npm / cargo / go / composer / uv、テスト、開発サーバー）は、**すべてコンテナの中**で動かします。
- 鍵（1Password、SSH、AWS）と `git commit` / `git push` は**ホストに残します**。
- コンテナには、そのプロジェクトのフォルダと、明示的に許可したものだけを渡します。

仕様と設計判断は `docs/superpowers/specs/` にあります。

## インストール

Go 1.27.4 を asdf で入れてから、ビルドしてインストールします。

```sh
asdf install golang 1.27.4    # .tool-versions で固定しています
make test
make install                  # ~/.local/bin/sigil に置きます
sigil build                   # sigil:latest イメージを作ります
```

`sigil` は標準ライブラリだけで書かれた 1 つのバイナリです。`image/`（Dockerfile と sigil-check）はバイナリに埋め込まれているので、インストール後はこのリポジトリがなくても `sigil build` できます。

## dev（devbox）からの移行

以前は `dev` という名前でした。名前・設定ファイル・コンテナ・ボリューム・イメージを、すべて `sigil` にそろえたので、次の手順で移行してください。

1. `make install` で `sigil` を入れます。`~/.local/bin/dev` は消えます（互換は残していません）。
2. 全体設定を移します: `mv ~/.config/devbox ~/.config/sigil`、`mv ~/.config/sigil/devbox ~/.config/sigil/config`
3. 各プロジェクトの `.devbox` を `.sigil` に名前を変えます（`.devbox` だけがあると、sigil は止まって知らせます）。
4. Neovim の設定で `vim.env.DEVBOX` を見ていれば、`vim.env.SIGIL` に変えます（`examples/nvim-sigil.lua`）。
5. 古い資源を消します。sigil からは見えないので、手で消してください。
   - コンテナ: `docker ps -a --filter label=devbox.project` で確かめて `docker rm -f`
   - ボリューム: `devbox-nvim-data`、`devbox-nvim-state`、`devbox-claude`、`devbox-cache`、`devbox-go`、`devbox-cargo`
   - イメージ: `devbox:latest`
   - 承認記録: `~/.local/state/devbox/`
6. `sigil build` で `sigil:latest` を作ります。

承認記録の場所が変わるので、各プロジェクトで最初の 1 回だけ、確認がもう一度出ます。Neovim のプラグインと Claude Code のログインも、最初の 1 回だけやり直しになります。

## 使い方

cmux のペインを開き、プロジェクトのフォルダで実行します。2 つ目以降のペインは、同じコンテナに入ります。

```sh
sigil nvim .      # エディタ
sigil claude      # Claude Code
sigil             # シェル（開発サーバーなど）
```

| コマンド | 動作 |
|---|---|
| `sigil` | プロジェクトのコンテナに入る（なければ作る）。`bash -l` |
| `sigil <command...>` | コンテナ内で `<command...>` を実行 |
| `sigil -- <command...>` | `stop` や `build` など、サブコマンドと同名のコマンドをコンテナ内で実行 |
| `sigil stop` | このプロジェクトのコンテナを削除（設定を変えたら一度これ） |
| `sigil ls` | sigil のコンテナの一覧 |
| `sigil check` | 隔離が効いているか、コンテナ内で確認 |
| `sigil commit [-- <path>...]` | コンテナで用意したメッセージで、確認してコミット・push（[詳細](#コミットと-pushsigil-commit)） |
| `sigil build [args...]` | イメージを作る（ツールの更新もこれ）。引数は `docker build` へ |
| `sigil help` | 使い方 |

- プロジェクトは、git リポジトリの最上位です（git でなければカレントディレクトリ）。サブフォルダで実行すると、コンテナ内でも同じサブフォルダで動きます。
- ホームディレクトリそのものや、`/`、`/Users` では動きません（ホーム全体をマウントしないため）。`~/.ssh`、`~/.aws`、`~/.config`、`~/Library` などの中でも動きません。
- git リポジトリではないフォルダで、中に別のリポジトリがある場所（`~/src` など）では動きません。リポジトリのフォルダで実行してください。
- モノレポでは、どのサブフォルダから実行しても、リポジトリ全体が 1 つのコンテナになります。git submodule や git worktree の中では、コンテナ内の git は使えません（ホストの git は使えます）。
- 環境変数 `SIGIL_IMAGE` でイメージ名（既定 `sigil:latest`）、`SIGIL_CONTEXT` で `sigil build` のビルドコンテキストを変えられます。

### コンテナに渡すもの

| ファイル | 内容 |
|---|---|
| `<project>/.sigil` | このプロジェクトに渡す環境変数名とポート。**内容が変わるたびに確認されます** |
| `<project>/.env` | 値（1Password の `op://` 参照） |
| `~/.config/sigil/config` | 全プロジェクト共通の環境変数名とポート（確認なし） |
| `~/.config/sigil/secrets.env` | 全プロジェクト共通の値（`op://` 参照） |

```
# .sigil / config の書式（シェルとして実行はされません）
env DATABASE_URL     # ホストの環境変数を同じ名前で渡す。値は書かない
port 5173            # 127.0.0.1:5173 -> コンテナ 5173
port 8080:80         # 127.0.0.1:8080 -> コンテナ 80
```

- `.env` や `secrets.env` に `op://` があり、`op` が入っていれば、`op run` 経由で値を読み込みます。値がコマンドラインに載ることはありません。
- 1Password から読み込むのは、`.env` のうち、値が `op://` 参照で、名前を `.sigil` か全体設定で `env` として列挙した行だけです。sigil はその行だけで作り直したファイルを `op run` に渡すので、`.env` のほかの行が sigil や docker の動作に影響することはありません。
- `op://` 参照ではない行（`VITE_API_URL` などのアプリの設定）は sigil は扱いません。アプリがコンテナ内で `.env` を直接読みます。
- `DOCKER_*`、`PATH`、`HOME`、`XDG_*`、`GIT_*` など、sigil や docker の動作を変えられる名前は渡せません。
- `.sigil` と、`.env` で 1Password から読み込む名前と参照は、変わるたびに確認されます。確認の画面には、名前ごとに 1Password の参照先が表示されます。アプリの設定の行を変えただけでは確認されません。
- `.sigil` と `.env` は、通常のファイルにしてください（シンボリックリンクは読みません）。
- 設定はコンテナを**作るとき**にだけ使われます。変えたら `sigil stop` してから `sigil` を実行してください。
- ひな形は `examples/` にあります。`examples/env.example` は、プロジェクト直下に `.env` という名前でコピーして使います（`.env` はコミットされないように `.gitignore` で除外しています）。Neovim（LazyVim）をコンテナで使うための設定は `examples/nvim-sigil.lua` を見てください。

### GitHub（gh）を使う

コンテナには `gh` が入っています。issue や PR を扱うには、**プロジェクトごとに作った fine-grained PAT** を `GH_TOKEN` として渡します。

コンテナに渡したトークンは、Claude Code だけでなく、プロジェクトのコード（npm のスクリプト、依存パッケージ、テストなど）からも読めます。安全さは、トークンに許す権限で決まります。

1. GitHub の Settings → Developer settings → Personal access tokens → **Fine-grained tokens** で作ります（classic PAT はリポジトリを絞れないので使いません）。
   - **Repository access**: Only select repositories で、そのプロジェクトのリポジトリ 1 つだけ
   - **Permissions**:
     - Issues: Read and write
     - Pull requests: Read and write
     - Contents: **Read-only**（push もマージもできないように。マージには Contents の書き込みが要ります）
     - Metadata: Read-only（自動で付きます）
   - **Expiration**: 30〜90 日
2. トークンを 1Password に入れます。
3. **そのプロジェクトの** `.sigil` と `.env` にだけ書きます。全体設定（`~/.config/sigil/config`）には書きません。書くと、全プロジェクトのコンテナに渡ってしまいます。

   ```
   # .sigil
   env GH_TOKEN
   ```
   ```
   # .env
   GH_TOKEN=op://Dev/myapp-github/token
   ```
4. `sigil stop` してから `sigil` を実行し、確認の画面で `GH_TOKEN` を承認します。
5. `sigil check` で確かめます。「外界の鍵」の欄に、トークンの種類と、期限まであと何日かが出ます（値は出ません）。classic PAT だと ✗、期限がない・90 日を超える・あと 7 日以内だと注意（!）になります。

- `gh auth login` はしません。`gh` は `GH_TOKEN` を環境変数から読むので、ログインは要りません（`~/.config/gh` にトークンを書かせないため）。
- `main` などにはブランチ保護を付けておくことを勧めます。
- コンテナの中の `.git` は読み取り専用なので、コミットと `git push` はホストで行います。`gh pr create` は、ブランチを push したあとに使えます。
- 権限が広すぎないか（Contents の書き込みなど）は、`sigil check` では確かめません（書き込みを試さないと分からないため）。作ったら一度、コンテナの中で PR のマージが拒否されることを確かめてください。

**GitHub MCP（リモート版）は勧めません。** トークンを Claude Code の設定（`~/.claude`）に書く必要があり、その置き場所（ボリューム `sigil-claude`）は全プロジェクトのコンテナで共有しています。プロジェクトごとに絞ったトークンが、ほかのプロジェクトのコンテナからも読めてしまいます。

## コミットと push（sigil commit）

コンテナの中の `.git` は読み取り専用なので、コンテナの Claude Code はコミットできません。そこで、コンテナでコミットメッセージを用意し、ホストの `sigil commit` が、内容を見せて確認したうえでコミット（と push）をします。コミットと push は、ホストの git と、あなた自身の設定・認証（`~/.ssh`、署名）で行います。

```sh
sigil commit                  # 変更のあるものすべて（追跡していない、無視されていないファイルを含む）
sigil commit -- src docs/a.md # 指定したものだけ（今いるフォルダからのパス）
```

1. コンテナの Claude Code が、プロジェクトの最上位の `.sigil-commit` にメッセージを書きます（1 行目が件名、空行、本文）。
2. ホストの端末で `sigil commit` を実行します。刻むファイル、確かめてほしいもの（CI、秘密らしいもの、フックなど）、メッセージが表示されます。`y` でコミット、`d` で全差分を見られます。
3. upstream があれば、push 先を見せたうえで、もう一度確認してから push します。

コンテナの Claude Code に渡す説明のひな形は `examples/sigil-commit.md` です。プロジェクトの `CLAUDE.md` に写して使ってください。

- **`.sigil-commit` をプロジェクトの `.gitignore` に入れてください。** sigil は `.gitignore` を書き換えません。入れていなくてもコミットからは必ず外れます。
- **ホストではフック（pre-commit、prepare-commit-msg、commit-msg、pre-push）を実行しません。** フックの中身はプロジェクトのコードなので、コンテナの中で実行します。フックがあるのにコンテナが動いていなければ、`sigil` で領域を開いてから、もう一度実行してください。
- `-- <path>` の指定は、今いるフォルダからのパスです。プロジェクトの外を指すものと、`:` で始まる指定（pathspec の magic）は受け付けません。
- 画面に出すメッセージとファイル名は、制御文字を処理してから表示します。メッセージは、整形して見せた内容がそのままコミットされます。
- upstream（`@{u}`）がないブランチでは push しません。`git push -u <remote> <branch>` をご自分で実行してください。push 先を推測はしません。
- push の URL に認証情報（`user:token@`）があっても、画面には出さず、コンテナの pre-push のフックにも渡しません。

次の場合は、理由を出して終了します。手動でコミット・push してください。

- 作業ツリーの中のファイルから読み込まれる git の設定がある（`include.path` を含む）。コンテナが設定を書き換えて、ホストでコードを実行できてしまうため。
- マージ、cherry-pick、revert、rebase などの途中。
- 入れ子のリポジトリ（gitlink）の追加・変更がある。
- LFS を使っている。
- フックの場所がプロジェクトの外にある（全体設定の `core.hooksPath` など）。
- 別の worktree（`git worktree add`）や、`--separate-git-dir` のリポジトリ。`.git` がプロジェクト直下の本物のフォルダである必要があります。
- コミットするものがない、メッセージが空、`.sigil-commit` がシンボリックリンクまたは 64KiB を超える。

## このリポジトリを開発するときの注意

`sigil` は、ホストで動く**唯一の信頼された道具**です。1Password と docker を操作します。ほかのプロジェクトのコードはコンテナに閉じ込めますが、このリポジトリのコードはホストで実行されます。

- このリポジトリをコンテナの中（`sigil nvim .` や `sigil claude`）で編集した場合は、**ホストで `git diff` を確認してから** `make build` / `make install` してください。コンテナの中で書き換えられたコードが、そのままホストで動くことになるためです。
- 外部の Go モジュールは追加しません（追加すると、そのコードがホストで動くため）。
- テストは Docker・git・op・端末がなくても動きます。

```sh
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt の確認
make build    # bin/sigil
```
