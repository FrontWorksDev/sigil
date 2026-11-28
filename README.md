# sigil

[![ci](https://github.com/FrontWorksDev/sigil/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/FrontWorksDev/sigil/actions/workflows/ci.yml)
[![vulncheck](https://github.com/FrontWorksDev/sigil/actions/workflows/vulncheck.yml/badge.svg?branch=main)](https://github.com/FrontWorksDev/sigil/actions/workflows/vulncheck.yml)
[![codecov](https://codecov.io/gh/FrontWorksDev/sigil/graph/badge.svg)](https://codecov.io/gh/FrontWorksDev/sigil)

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

### go install で入れる（別の端末など）

リポジトリを clone しなくても、Go（1.27 以上）があれば、リリースしたタグから入れられます。

```sh
CGO_ENABLED=0 go install -trimpath github.com/FrontWorksDev/sigil@latest   # 版を決めるなら @v0.6.0 など
which sigil                            # どこに置かれたか
sigil build
```

- `CGO_ENABLED=0` と `-trimpath` は、`make build` と同じ作り方にそろえるためです。`-trimpath` がないと、ビルドした場所のパス（ホームのパスを含む）がバイナリに埋め込まれます。
- 置き場所は `go env GOBIN` が設定されていればそこ、なければ `$(go env GOPATH)/bin`（ふつうは `~/go/bin`）です。PATH に入っていなければ足してください。`make install` の置き場所（`~/.local/bin`）とは違うので、両方に置くと PATH の先にある方が使われます。
- 手元の Go が 1.27 より古くても、Go が必要な版（`go.mod` の `go 1.27` 以上）を自動で取ってきてビルドします。
- 自分でビルドした版（`make install`）との違いは、`--version` にコミットが出ないことと、手元の作業ツリーではなくタグの中身から作られることです。機能は同じです。
- ソースは Go のモジュールプロキシ（proxy.golang.org）から取り、チェックサムデータベース（sum.golang.org）で照合されます。一度公開されたタグの中身は、あとから差し替えられません。

`sigil` は標準ライブラリだけで書かれた 1 つのバイナリです。`image/`（Dockerfile と、点検のプログラム sigil-check の Go のソース）はバイナリに埋め込まれているので、インストール後はこのリポジトリがなくても `sigil build` できます。

インストールした版は `sigil --version`（`-V` でも）で確かめられます。不具合を報告するときは、この 1 行を添えてください。

```console
$ sigil --version
sigil v0.4.0 (commit 57a801c, go1.27.4, darwin/arm64)
```

- 版は `make build` / `make install` のときに `git describe --tags --always` で埋め込みます。`go install ...@v0.6.0` で入れたときは、そのタグ名になります。`make` を通さずに `go build` したときは、Go がタグとコミットから作る版（`v0.6.1-0.20261126...+dirty` など）になり、それもなければ `dev` になります。
- コミットは、Go がビルドのときに埋め込む git の情報から取ります。手元に変更があるときは `-dirty` が付きます。`make install` し忘れていないかは、このコミットと `git log -1` を比べると分かります。

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
| `sigil stop` | このプロジェクトのコンテナを削除（ボリュームは残す。設定を変えたら一度これ） |
| `sigil purge` | このプロジェクトのコンテナとボリュームを、確かめてから削除（[詳細](#ボリュームと片付けsigil-purge)） |
| `sigil ls` | sigil のコンテナの一覧 |
| `sigil check` | 隔離が効いているか、コンテナ内で確認 |
| `sigil commit [-- <path>...]` | コンテナで用意したメッセージで、確認してコミット・push（[詳細](#コミットと-pushsigil-commit)） |
| `sigil build [args...]` | イメージを作る（ツールの更新もこれ）。引数は `docker build` へ |
| `sigil help` | 使い方 |
| `sigil --version` / `-V` | sigil の版 |

- プロジェクトは、git リポジトリの最上位です（git でなければカレントディレクトリ）。サブフォルダで実行すると、コンテナ内でも同じサブフォルダで動きます。
- ホームディレクトリそのものや、`/`、`/Users` では動きません（ホーム全体をマウントしないため）。`~/.ssh`、`~/.aws`、`~/.config`、`~/Library` などの中でも動きません。
- git リポジトリではないフォルダで、中に別のリポジトリがある場所（`~/src` など）では動きません。リポジトリのフォルダで実行してください。
- モノレポでは、どのサブフォルダから実行しても、リポジトリ全体が 1 つのコンテナになります。git submodule や git worktree の中では、コンテナ内の git は使えません（ホストの git は使えます）。
- 環境変数 `SIGIL_IMAGE` でイメージ名（既定 `sigil:latest`）、`SIGIL_CONTEXT` で `sigil build` のビルドコンテキストを変えられます。
  - `SIGIL_CONTEXT` で別の Dockerfile を使うときは、`sigil check` のために、`image/sigilcheck/` の `_test.go` 以外のファイルをそのビルドコンテキストの `sigilcheck/` に置き、`go.mod`（`module sigilcheck` と、リポジトリ直下の `go.mod` と同じ `go` の行の 2 行）を足して、`image/Dockerfile` と同じように `sigil-check-build` のステージでコンパイルしてください。
  - リポジトリの `image/` をそのまま `SIGIL_CONTEXT` に指定しても、ビルドできません（`image/sigilcheck/` に `go.mod` がないため。`go.mod` は `sigil build` が書き出すときに作ります）。
- `sigil build` は、毎回ツールを最新版に取り直します（土台のイメージは `--pull` で取り直し、Neovim・Claude Code・gh などのダウンロードはキャッシュを使いません）。apt で入れるものと PHP 拡張は、土台のイメージが更新されたときに作り直されます。すべてを作り直すときは `sigil build --no-cache` です。
  - 手元にしかない土台のイメージを使うとき（`--build-arg NODE_IMAGE=...` など）や、オフラインのときは `sigil build --pull=false` としてください。
  - ビルドのたびに前のイメージが名前のないイメージ（`<none>`）として残ります。ときどき `docker image prune` で片付けてください（ビルドのキャッシュは `docker builder prune`）。

### ボリュームと片付け（sigil purge）

`~/.cache`・`~/go`・`~/.cargo`・`~/.claude` は、プロジェクトごとのボリューム（`sigil-<フォルダ名>-<ハッシュ>-cache` / `-go` / `-cargo` / `-claude`）に置きます。ダウンロードしたパッケージやビルドのキャッシュは、コンテナを作り直しても残ります。

- プロジェクトごとに分けているのは、あるプロジェクトの依存パッケージやビルドスクリプトが、キャッシュや `~/go/bin` を通じて、ほかのプロジェクトのコンテナでコードを実行できないようにするためです。そのため、新しいプロジェクトでは、1 回目だけダウンロードとビルドに時間がかかります（MCP サーバーの `uvx` / `npx` も同じです）。
- `sigil stop` はコンテナだけを消し、ボリュームは残します。ボリュームも消すときは `sigil purge` です。消す前に一覧を出して確かめます（`y` で消します）。消したものは戻せません。
- `sigil purge` が消すのは、そのプロジェクトのために sigil が作ったボリュームだけです（ラベル `sigil.project` で見分けます）。
- `sigil purge` はコンテナを先に消すので、ほかのペインで動いている Neovim や Claude Code も終わります（保存していない編集は失われます）。コンテナが動いているときは、確認の一覧でそう知らせます。先にほかのペインを閉じてから実行してください。
- 確認を待つあいだに、ほかのペインがコンテナを作り直して使い始めたときは、ボリュームを消せずに止まります。
- Go・Rust のキャッシュは、プロジェクトごとに数 GB になることがあります。使用量は `docker system df -v` で確かめられます。
- 同じフォルダでも、別の書き方（大文字小文字が違うなど）で開くと、別のプロジェクトとして扱われ、`sigil purge` でも見つかりません。いつもと同じ書き方のパスから実行してください。
- `sigil purge` は、Claude Code の会話の履歴（`-claude`）も消します。
- Neovim のプラグイン（`sigil-nvim-data`、`sigil-nvim-state`）は、まだ全プロジェクトで共有しています。あるプロジェクトのコードが、ほかのプロジェクトで Neovim を起動したときに動くものを仕込める状態です。そこから、そのプロジェクトの `~/.claude` やトークンにも届きます（issue #7 で分ける予定です）。

#### 共有ボリュームからの移行

以前は `~/.cache`・`~/go`・`~/.cargo` も全プロジェクトで共有していました（`sigil-cache`・`sigil-go`・`sigil-cargo`）。

1. 新しい sigil を入れ、古い sigil のバイナリ（別の場所に入れたものなど）を消します。古い sigil で入ると、共有のボリュームがまた使われます。
2. 各プロジェクトで `sigil stop` してから、もう一度 `sigil` を実行します（古いコンテナのままだと「古い sigil で作られています」と止まります）。
3. `sigil ls` と `docker ps -a --filter label=sigil.layout=2` で、古いコンテナが残っていないことを確かめます（`sigil ls` で、各プロジェクトのコンテナを作り直したかも見ます）。
4. `docker volume rm sigil-cache sigil-go sigil-cargo` で、古い共有ボリュームを消します。中身は新しいボリュームに移さないでください（ほかのプロジェクトのコードが書き換えたものかもしれないため）。

共有している `sigil-nvim-*` も、すでに書き換えられている不安があれば作り直せます。すべてのコンテナを `sigil stop` してから `docker volume rm sigil-nvim-data sigil-nvim-state` を実行すると、次に入ったときに空のボリュームが作られます。Neovim のプラグインは入れ直しになります。

### Claude Code（ログインと神託）

`~/.claude`（Claude Code の設定・会話の履歴）は、プロジェクトごとのボリュームです。あるプロジェクトのコードが hooks やプラグインを仕込んでも、ほかのプロジェクトの Claude Code では動きません。ほかのプロジェクトの会話の履歴も読めません。

全プロジェクトで共通にしたいものは、ホストの `~/.config/sigil/claude/`（**神託**）に置きます。全コンテナの `/etc/claude-code`（Claude Code の管理用ディレクトリ）に、読み取り専用でマウントされます。

```
~/.config/sigil/claude/
  CLAUDE.md                      全体の指示
  .claude/skills/<名前>/SKILL.md  skills
  managed-settings.json          プラグイン（MCP サーバーはプラグインから）
```

```json
{
  "extraKnownMarketplaces": {
    "your-marketplace": { "source": { "source": "github", "repo": "your-org/your-marketplace" } }
  },
  "enabledPlugins": { "code-formatter@your-marketplace": true }
}
```

- 神託を書き換えたら、反映させたいプロジェクトで `sigil stop` してから入り直してください。神託のフォルダをあとから作ったときも同じです（コンテナを作るときにだけマウントされます）。
- プラグインは「どれを使うか」だけが共通で、本体は各プロジェクトが 1 回目にダウンロードします。
- MCP サーバーは、プラグインに入っているものを `enabledPlugins` で有効にすれば、全プロジェクトで使えます。`managed-settings.json` の `managedMcpServers` に直接書けるのは、`https://` の URL で接続する、トークンの要らないリモートのサーバーだけです（`command` で起動するものは、Claude Code が黙って捨てます）。
- トークンの要る MCP（GitHub の MCP など）は、使うプロジェクトの中で `claude mcp add` してください（下の「GitHub（gh）を使う」）。そのプロジェクトにだけ入ります。
- Claude Code の画面の中で `/plugin install` や `/mcp add` をしても、そのプロジェクトにしか入りません。全プロジェクトで使うものは、神託に書いてください。
- 神託のフォルダには、**リンクではなく実体のファイル**を置いてください。フォルダや中身がシンボリックリンクだったり、グループやほかのユーザーが書き込めたりすると、sigil は止まります（ホストの `~/.claude` を見せたり、コンテナの中の別のファイルを指したりしないように）。
- `managed-settings.json` は Claude Code の設定の中で一番優先され、すべてのプロジェクトに効きます。次のことに気をつけてください。
  - 秘密（トークンなど）を書かない。全コンテナから読めます。トークンの要るものは、使うプロジェクトの中で設定し、値はそのプロジェクトの `.sigil` で渡します。
  - hooks・MCP サーバーの `command` は、絶対パス（`/usr/local/bin/...`）で書く。PATH の先頭の `~/.local/bin` や `~/go/bin` には、そのプロジェクトのコードが置けます。ただし、`npx` のように `#!/usr/bin/env node` で始まるものは、中で `node` を PATH から探すので、絶対パスにしても、そのプロジェクトのコードが置いた `node` が動きえます（ほかのプロジェクトには広がりません）。
  - マーケットプレイスや MCP サーバーの場所に、ローカルのパスを書かない（GitHub などの取得元にする）。
  - `permissions.defaultMode` を `bypassPermissions` にしたり、広い `allow` を書いたりしない。信頼できないリポジトリを含む、全プロジェクトに効きます。

**ログイン**は、プロジェクトごとにし直さずに済むよう、トークンを全体設定から渡します。

1. ホストで `claude setup-token` を実行し、トークンを作ります（有効期限は 1 年）。
2. 全体設定（`~/.config/sigil/config`）に `env CLAUDE_CODE_OAUTH_TOKEN` と書き、値は `~/.config/sigil/secrets.env` に `keychain://...` か `op://...` で置きます（[キーチェーン](#キーチェーンから読み込む1password-がない端末)）。
3. 各プロジェクトで `sigil stop` してから入り直します。

- このトークンでできるのは、モデルの呼び出しだけです。claude.ai で登録したコネクタ（デスクトップアプリなどで設定した MCP）と Remote Control は使えません。手元で登録した MCP サーバーは使えます。
- 全プロジェクトに同じトークンが渡ります。トークンはそのプロジェクトのコード（npm のスクリプトや依存パッケージ）からも読めるので、どれか 1 つのプロジェクトが漏らせば、全プロジェクトのものが漏れたのと同じです。漏れたと思ったら claude.ai 側で取り消し、作り直してください。
- トークンはコンテナを作るときに渡されます。取り替えたら、すべてのプロジェクトで `sigil stop` してから入り直してください。

#### `sigil-claude` からの移行

以前は `~/.claude` を全プロジェクトで共有していました（`sigil-claude`）。

1. 神託とトークンを用意します（上）。
2. 動いている古いコンテナも含めて、各プロジェクトで `sigil stop` してから入り直します。
3. `sigil-claude` に入っていたログインは、全プロジェクトのコードから読めました。持ち出されているかもしれないので、claude.ai 側でそのログインを取り消してください。ボリュームを消しても無効にはなりません。
4. 過去の会話の履歴を見返すなどで残しておく場合は、`CLAUDE_CONFIG_DIR` に向けて使ったり、`settings.json`・`plugins/`・`.credentials.json` を新しいボリュームに写したりしないでください（書き換えられた hooks などが動くため）。要らなくなったら `docker volume rm sigil-claude` で消します。

### コンテナに渡すもの

| ファイル | 内容 |
|---|---|
| `<project>/.sigil` | このプロジェクトに渡す環境変数名とポート、読み取り専用でマウントするフォルダ。**内容が変わるたびに確認されます** |
| `<project>/.env` | 値（1Password の `op://` 参照、キーチェーンの `keychain://` 参照） |
| `~/.config/sigil/config` | 全プロジェクト共通の環境変数名とポート、読む専用キーチェーン、読み取り専用でマウントしてよい範囲（確認なし） |
| `~/.config/sigil/secrets.env` | 全プロジェクト共通の値（`op://` / `keychain://` 参照） |

`XDG_CONFIG_HOME` / `XDG_STATE_HOME` を設定しているときは、その下の `sigil/` を使います。絶対パスでないときは無視して、`~/.config` / `~/.local/state` を使います（プロジェクトの中の設定を読まないように）。

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
- `.sigil` と、`.env` で 1Password・キーチェーンから読み込む名前と参照は、変わるたびに確認されます。確認の画面には、名前ごとに読み込み元と参照先が表示されます。アプリの設定の行を変えただけでは確認されません。
- 同じ名前が `.env` と `secrets.env` の両方にあれば、`secrets.env` が優先されます（確認の画面に「全体の設定が優先されるため使わない」と出ます）。
- `.sigil` と `.env` は、通常のファイルにしてください（シンボリックリンクは読みません）。
- 設定はコンテナを**作るとき**にだけ使われます。変えたら `sigil stop` してから `sigil` を実行してください。
- ひな形は `examples/` にあります。`examples/env.example` は、プロジェクト直下に `.env` という名前でコピーして使います（`.env` はコミットされないように `.gitignore` で除外しています）。Neovim（LazyVim）をコンテナで使うための設定は `examples/nvim-sigil.lua` を見てください。

### 別のフォルダを読み取り専用で見る

別のリポジトリ（ライブラリのソースなど）を、参照のためにコンテナから読めるようにできます。

```
# ~/.config/sigil/config — マウントしてよい範囲（この全体設定でだけ書ける）
ro_root ~/src

# <project>/.sigil
ro ~/src/some-library    # /workspace/some-library に読み取り専用でマウント
```

- `ro` は、全体設定の `ro_root` の中にあるフォルダだけを指せます。`ro_root` がなければ `ro` はエラーになります。`.sigil` はリポジトリの中にあり、他人が書いたものかもしれないので、どこまで見せてよいかはホスト側の設定で決めます。`ro_root` には、他人のコードに見せてよいリポジトリの置き場だけを書いてください。
- ホーム、`~/.ssh`、`~/.config` などの秘密情報を含む場所や、プロジェクトの中は、`ro_root` の中でもマウントできません。`~/.config` などを dotfiles のフォルダへのシンボリックリンクにしている場合は、その dotfiles のフォルダ（と、それを含む `ro_root`）も使えません。
- 存在しない `ro_root`（外したディスクの中など）は飛ばされます。
- 確認の画面には、シンボリックリンクを解決したホスト側のパスが出ます。リンクの向きが変わると、また確認されます。
- **マウントしたフォルダの中の `.env` なども、コンテナから読めます。** 隠す仕組みはありません。
- パスは `~/` か `/` で始めます。空白を含むパスは書けません。マウント先の名前（フォルダ名）がプロジェクトやほかの `ro` と重なるとエラーになります。

### キーチェーンから読み込む（1Password がない端末）

1Password を使えない Mac では、macOS のキーチェーンから値を読み込めます。`.env` の書き方が違うだけで、`.sigil` への列挙や確認の画面は 1Password と同じです。sigil は `/usr/bin/security` で、**全体設定で指定した専用のキーチェーンだけ**を読みます（ログインキーチェーンは読みません）。

1. 専用のキーチェーンを作り、使わないときは自動でロックされるようにします。パスワードは引数に書かず、聞かれたら入力します。

   ```sh
   security create-keychain ~/Library/Keychains/sigil.keychain-db
   security set-keychain-settings -l -u -t 300 ~/Library/Keychains/sigil.keychain-db   # 5 分使わなければ・スリープでロック
   ```

   キーチェーンは `~/Library/Keychains/` の直下に置いてください。それ以外の場所や、名前が `login` で始まるものは使えません。作ったキーチェーンが検索リストに加わったかは、`security list-keychains` で確かめられます（環境によって加わることがあります）。

2. 項目を足します。シェルで `security add-generic-password ... -w 値` と打つと、値がほかのプロセスから `ps` で見え、シェルの履歴にも残ります。また、`-w` のあとにキーチェーンのパスを続けると、パスが値として読まれ、既定のキーチェーンに入ってしまいます。そこで `security -i`（対話モード）の中で打ちます。

   ```sh
   security -i
   security> add-generic-password -s wakatime -a sigil -w <値> /Users/<you>/Library/Keychains/sigil.keychain-db
   ```

   終わったら Ctrl-D で抜けます。対話モードでは `~` が展開されないので、パスは絶対パスで書きます。打った値は画面に表示され、端末のスクロールバックやペインのログに残るので、終わったら消してください。空白や引用符を含む値は、引用符で囲みます。

   - 同じ service（`-s`）の項目を複数作らないでください。どれが読まれるか決まりません。
   - 値は、印字できる文字の 1 行にしてください。制御文字などを含む値は、`security` が 16 進で出力するので、そのまま渡ってしまいます。
   - 読むたびに確認のダイアログを出したいときは、`-T ""` を付けて作ります（コンテナを作るたび、項目ごとにダイアログが出ます）。付けずに作った項目や、ダイアログで「常に許可」を選んだ項目は、解錠中なら、同じユーザーのほかのプロセスも `security` で確認なしに読めます。

3. 全体設定に、専用のキーチェーンを書きます（全体設定でだけ書けます。`.sigil` に書くとエラーです）。

   ```
   # ~/.config/sigil/config
   env WAKATIME_API_KEY
   keychain ~/Library/Keychains/sigil.keychain-db
   ```

4. `.env` か `secrets.env` に、`keychain://<service>` と書きます。

   ```sh
   WAKATIME_API_KEY=keychain://wakatime
   ```

- キーチェーンがロックされていれば、コンテナを作るときに macOS の解錠のダイアログが出ます（120 秒応答がなければ止まります）。
- 全体設定に `keychain` がないと、`keychain://` の名前は渡しません（ホストのシェルに同じ名前があっても、そちらは使いません）。
- 全体の `secrets.env` に同じ名前があると、そちらが優先されます。その値を読み込めないとき（`op` がない、`secrets.env` に `op://` がないなど）は、その名前は渡しません（ホストのシェルの値も使いません）。
- 1 つの `.env` に同じ名前を 2 回書かないでください（`op://` と `keychain://` を両方書くと、順序によらずキーチェーンの値が使われます）。
- 行末にコメントは書けません。パスに空白は使えません。
- 値を替えたら、`sigil stop` してから作り直してください（作ったときの値が使われ続けます）。

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

**GitHub MCP（リモート版）を使うときは、そのプロジェクトの中で登録してください。** `~/.claude` はプロジェクトごとなので、画面の中で登録したものは、そのプロジェクトにだけ入ります。神託（`managed-settings.json`）にトークンを書くと、全プロジェクトのコンテナから読めてしまいます。

## コミットと push（sigil commit）

コンテナの中の `.git` は読み取り専用なので、コンテナの Claude Code はコミットできません。そこで、コンテナでコミットメッセージを用意し、ホストの `sigil commit` が、内容を見せて確認したうえでコミット（と push）をします。コミットと push は、ホストの git と、あなた自身の設定・認証（`~/.ssh`、署名）で行います。

```sh
sigil commit                  # 変更のあるものすべて（追跡していない、無視されていないファイルを含む）
sigil commit -- src docs/a.md # 指定したものだけ（今いるフォルダからのパス）
```

1. コンテナの Claude Code が、プロジェクトの最上位の `.sigil-commit` にメッセージを書きます（1 行目が件名、空行、本文）。
2. ホストの端末で `sigil commit` を実行します。刻むファイル、確かめてほしいもの（CI、秘密らしいもの、フックなど）、メッセージが表示されます。`y` でコミット、`d` で全差分を見られます（ホストに `delta` があれば、設定は読ませずに delta で整形します）。
3. upstream があれば、push 先を見せたうえで、もう一度確認してから push します。

コンテナの Claude Code に渡す説明のひな形は `examples/sigil-commit.md` です。プロジェクトの `CLAUDE.md` に写して使ってください。

- **`.sigil-commit` をプロジェクトの `.gitignore` に入れてください。** sigil は `.gitignore` を書き換えません。入れていなくてもコミットからは必ず外れます。
- **ホストではフック（pre-commit、prepare-commit-msg、commit-msg、pre-push）を実行しません。** フックの中身はプロジェクトのコードなので、コンテナの中で実行します。フックがあるのにコンテナが動いていなければ、`sigil` で領域を開いてから、もう一度実行してください。
- `-- <path>` の指定は、今いるフォルダからのパスです。`*` や `[` などは glob ではなく、名前そのものとして読みます。プロジェクトの外を指すものと、`:` で始まる指定（pathspec の magic）は受け付けません。
- 画面に出すメッセージとファイル名は、制御文字を処理してから表示します。メッセージは、整形して見せた内容がそのままコミットされます。
- upstream（`@{u}`）がないブランチでは push しません。`git push -u <remote> <branch>` をご自分で実行してください。push 先を推測はしません。
- push の URL に認証情報（`user:token@`）があっても、画面には出さず、コンテナの pre-push のフックにも渡しません。

次の場合は、理由を出して終了します。手動でコミット・push してください。

- 作業ツリーの中のファイルから読み込まれる git の設定がある（`include.path` を含む）。コンテナが設定を書き換えて、ホストでコードを実行できてしまうため。
- マージ、cherry-pick、revert、rebase などの途中。
- サブモジュールがある（変更がなくても）。サブモジュールの中の設定はコンテナが書き換えられ、ホストの git がそれを読んで、コードを実行できてしまうため。
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

### CI（GitHub Actions）

PR と main への push で、テスト（Linux と macOS）・`go vet`・`gofmt`・darwin 向けのビルド・外部モジュールがないことの確認・govulncheck を実行します。govulncheck は週 1 回の定期実行でも動きます。詳細は設計書の 18 章です。

ワークフローが使う Actions は、[gh-actions-lock](https://github.com/github/gh-actions-lock) で `.github/workflows/actions.lock` に固定しています。`uses:` を変えたときは、ホストで次を実行し、ロックファイルの差分を読んでからコミットしてください。

```sh
gh extension install github/gh-actions-lock --pin v0.5.1   # 初回だけ
gh actions-lock               # ロックファイルを作り直す
gh actions-lock --verify-local  # すべての uses: がロックファイルにあるか
```
