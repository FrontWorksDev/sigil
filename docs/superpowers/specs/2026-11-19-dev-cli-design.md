# sigil CLI 設計書

- 作成日: 2026-11-19
- 状態: 承認済み・実装済み
- 元になった文書: `docs/SPEC.md`（この設計書に統合し、削除済み）

この文書だけで `sigil` を実装・検証できるようにしている。仕様を変えるときはこの文書を更新し、末尾の「決定事項」に理由を残す。

---

## 1. 目的（なぜ作るか）

npm / PyPI などのサプライチェーン攻撃や、悪意あるリポジトリから、ホストの鍵を守る。

- プロジェクトのコードを動かすもの（Neovim の LSP、Claude Code、npm / cargo / go / composer / uv、テスト、開発サーバー）は**すべてコンテナの中**で動かす。
- 鍵（1Password、SSH、AWS）と `git commit` / `git push` は**ホストに残す**。
- コンテナには、そのプロジェクトのフォルダと、明示的に許可したものだけを渡す。

利用者の環境:
- macOS（Apple Silicon）、ターミナルは cmux、エディタは Neovim（LazyVim）。Claude Code をよく使う。
- 1 つのプロジェクトに対して cmux のペインを複数開き、`sigil nvim .`、`sigil claude`、`sigil`（開発サーバー用）をそれぞれ実行する。2 つ目以降は同じコンテナに入る。

もとはシェル版（`~/.local/share/devbox/bin/dev.sh`。コマンド名は `dev`）を使っていた。これを Go の単一バイナリで置き換え（v0.1.0）、その後、名前を `sigil` に改めた（決定事項を参照）。

### ゴール
- シェル版と同じ使い勝手で、テストで裏付けられた Go 版の `sigil`。
- 隔離の約束（下の「やってはいけないこと」）を、コードとテストの両方で守る。

### やらないこと
- 既存のコンテナとの互換。シェル版のコンテナは、Go 版の導入後に作り直す（古いものは layout ラベルで検出して `sigil stop` を促すだけ）。
- Linux や Windows のホスト対応。
- `op` を使う流れの実機検証（単体テストで担保する）。

### やってはいけないこと（技術的に動いても失敗とみなす）
- ホーム、`~/.ssh`、`~/.aws`、Docker ソケットをコンテナにマウントする。
- シークレットの値をコマンドライン引数、ログ、エラーメッセージに載せる。
- リポジトリに置かれた `.sigil` によって、確認なしにホストの環境変数がコンテナに渡る。
- 外部の Go モジュールに依存する（このバイナリはホストで動く、唯一の信頼された道具なので）。

## 2. 前提として用意されているもの

```
image/Dockerfile      ベースイメージ（Neovim / Claude Code / Node / Go / Rust / PHP / Python）
image/sigil-check    コンテナ内で隔離を点検するスクリプト（イメージに組み込まれる）
examples/             設定ファイルのひな形
```

`image/` の 2 ファイルはまだ実際の docker でビルドしたことがない。検証の中で問題があれば直してよいが、変更点と理由は報告すること。

## 3. 技術的な制約

- **Go 1.27.4**（2026-11 時点の最新）。リポジトリ直下の `.tool-versions` に `golang 1.27.4` と書いて asdf で固定する。`go.mod` は `go 1.27`。
- Go の標準ライブラリのみ。cgo なし（`CGO_ENABLED=0`）。macOS（arm64 / amd64）で動く 1 つのバイナリ。
- `image/` は `//go:embed` でバイナリに埋め込み、`sigil build` のときに一時フォルダへ書き出してビルドコンテキストにする（`sigil-check` は実行権限 0755）。
- ユーザー向けのメッセージとエラーは日本語。先頭に `sigil: ` を付けて標準エラーに出し、終了コード 1。
- コードのコメントは日本語で、「なぜそうしているか」を書く。

## 4. コマンド

| コマンド | 動作 |
|---|---|
| `sigil` | プロジェクトのコンテナに入る（なければ作る）。コマンドは `bash -l` |
| `sigil <command...>` | コンテナ内で `<command...>` を実行 |
| `sigil -- <command...>` | 下のサブコマンドと同名のコマンドを、コンテナ内で実行するため |
| `sigil stop` | このプロジェクトのコンテナを `docker rm -f` |
| `sigil ls` | sigil のコンテナ一覧（`docker ps -a --filter label=sigil.project`、名前・状態・プロジェクトのパスを表で） |
| `sigil check` | コンテナ内で `sigil-check` を実行 |
| `sigil build [args...]` | イメージをビルド。追加の引数は `docker build` に渡す |
| `sigil commit [-- <path>...]` | コンテナで用意したコミットメッセージで、確認のうえホストの git でコミットし、push するかを別に確かめる（17 章） |
| `sigil help` / `-h` / `--help` | 使い方を表示 |

- `build`、`ls`、`help` はプロジェクトの外でも動く。それ以外はプロジェクトを特定してから動く。
- `commit` はホストで動くコマンドで、コンテナの中では実行しない。docker は、コンテナでフックを実行するときだけ使う。
- `sigil stop` で対象のコンテナがないときは「<名前> はありません」と出して終了コード 0（シェル版と同じ）。
- `sigil ls` の書式: `--format 'table {{.Names}}\t{{.Status}}\t{{.Label "sigil.project"}}'`。

環境変数:
- `SIGIL_IMAGE`: イメージ名（既定 `sigil:latest`）
- `SIGIL_CONTEXT`: 設定されていれば、埋め込みの代わりにこのフォルダを `sigil build` のビルドコンテキストにする

## 5. プロジェクトの特定

1. カレントディレクトリ（シンボリックリンク解決済み）から `git rev-parse --show-toplevel` でリポジトリの最上位を探す。git リポジトリでなければ、カレントディレクトリそのもの。結果もシンボリックリンクを解決する。
2. **次の場所は拒否する**（エラーで終了し、docker を一切呼ばない）:
   - `/`、`/Users`、`/home`、ホームディレクトリそのもの
   - ホームディレクトリを含むフォルダ（ホームがその配下にある）
   - 秘密情報を含む場所（`~/.ssh`、`~/.aws`、`~/.gnupg`、`~/.docker`、`~/.config`、`~/Library`、`$XDG_CONFIG_HOME/sigil`、`$XDG_STATE_HOME/sigil`）の中、またはそれを含むフォルダ
   - git リポジトリではないフォルダで、直下に `.git` を持つフォルダがあるもの（その `.git` が書き込める状態でマウントされるため）。git リポジトリの中（モノレポなど）なら対象外
3. 比較は文字列ではなく、ファイルの同一性（`os.SameFile`）で行う。macOS の APFS は大文字小文字を区別しないので、`/users/<name>` も `/Users/<name>` と同じ場所として扱う。存在しないパスは、大文字小文字を無視した文字列で比べる。

### コンテナ名

シェル版と同じ規則にする。

```
sigil-<safe>-<key>
  safe: フォルダ名の各バイトのうち [A-Za-z0-9_.-] 以外を "-" に置き換え、先頭 40 バイトに切り詰めたもの
  key:  プロジェクトのフルパス（末尾改行なし）の SHA-256 の 16 進表記の先頭 8 文字
```

テストベクトル:

| プロジェクトのパス | コンテナ名 |
|---|---|
| `/tmp/t/home/proj` | `sigil-proj-33bf5961` |
| `/tmp/t/home/my proj` | `sigil-my-proj-42cb34c6` |

### コンテナ内のパス

`/workspace/<フォルダ名>`。
- WakaTime などはフォルダ名からプロジェクト名を判定するので、フォルダ名はそのまま使う（スペースや日本語もそのまま）。
- ただし `:` か `,` を含む場合は docker の `-v` と衝突するので、上の `safe` を使う。
- ホスト側のパス（親フォルダを含む）に `:` があると、`-v` で正しくマウントできないので、フォルダ名を変えるよう求めるエラーで終了する（決定事項を参照）。

カレントディレクトリがプロジェクトのサブフォルダなら、コンテナ内でも対応するサブフォルダで実行する（例: ホストの `<root>/web/src` → `/workspace/<name>/web/src`）。

プロジェクトの外や、名前が前方一致するだけの別フォルダ（`/src/app` に対する `/src/application`）では、プロジェクトの最上位にする。

## 6. 設定ファイル

| ファイル | 内容 | 確認 |
|---|---|---|
| `<project>/.sigil` | このプロジェクトに渡す環境変数名とポート | 内容が変わるたびに確認する |
| `<project>/.env` | 値（1Password の `op://` 参照、キーチェーンの `keychain://` 参照） | — |
| `$XDG_CONFIG_HOME/sigil/config`（既定 `~/.config/sigil/config`） | 全プロジェクト共通の環境変数名とポート、読む専用キーチェーン（19 章）、`ro` でマウントしてよい範囲（20 章） | 不要（ホストのファイル） |
| `$XDG_CONFIG_HOME/sigil/secrets.env` | 全プロジェクト共通の値 | — |

`sigil` は `.env` と `secrets.env` を中身まで解析しない。`op://` を含むかどうかを調べるだけで、値は表示しない。

### `.sigil` / `sigil` の書式

```
# コメント（# 以降は無視）
env DATABASE_URL        # ホストの環境変数を同じ名前で渡す。値は書かない
port 5173               # 127.0.0.1:5173 -> コンテナ 5173
port 8080:80            # 127.0.0.1:8080 -> コンテナ 80
ro ~/src/some-library   # /workspace/some-library に読み取り専用でマウント（20 章。.sigil でだけ書ける）
```

- **シェルとして実行しない**。1 行ずつ、空白で区切って読むだけ。
- 1 行はちょうど 2 語。違えばエラー。空行とコメントだけの行は読み飛ばす。
- `env` の名前は `^[A-Za-z_][A-Za-z0-9_]*$`。次は拒否する: `SSH_AUTH_SOCK`、`PATH`、`HOME`、および `DOCKER_`、`XDG_`、`SIGIL_`、`GIT_`、`OP_`、`LD_`、`DYLD_` で始まる名前。
- プロジェクトの `.sigil` と `.env` は、シンボリックリンクを拒否する（`~/.netrc` などへのリンクで、ホストの秘密を読ませないため）。全体設定は dotfiles の管理でリンクにされることがあるので許す。どのファイルも、通常のファイル以外（FIFO、デバイス）と 64KiB を超えるものは拒否する。
- `port` は `^[0-9]{1,5}(:[0-9]{1,5})?$`。
- `keychain <パス>` は全体設定でだけ書ける（19 章）。プロジェクトの `.sigil` に書けばエラー。
- `ro <パス>` はプロジェクトの `.sigil` でだけ書ける（20 章）。全体設定に書けばエラー。
- `ro_root <パス>` は全体設定でだけ書ける（20 章）。`.sigil` に書けばエラー。
- `.sigil` に NUL があればエラー（承認のハッシュの区切りに使うため。20 章）。
- `env` / `port` / `keychain` / `ro` / `ro_root` 以外はエラー。
- エラーメッセージは `<ファイル>:<行番号>: <内容>` の形。ファイルはフルパスで示す。
- プロジェクトの設定と全体の設定は合わせて使う。環境変数名の重複は 1 つにまとめる（最初に出たものの順を保つ）。
- 列挙された変数がホストで設定されていなければ、注意を出して渡さない。

### `.sigil` の承認

リポジトリに勝手に置かれた `.sigil` で、ホストの環境変数が黙って渡らないようにするための仕組み。

- 対象: プロジェクトの `.sigil` と、`.env` のうち 1Password から読み込む「名前と参照の組」（7 章で選んだもの）。組を対象にするのは、承認した名前が、どの 1Password 項目から値を取るかを後から差し替えられないようにするため。`.env` の生の内容ではないので、アプリの設定の行を変えても確認し直さない。
- 記録場所: `$XDG_STATE_HOME/sigil/approved/<key>`（既定 `~/.local/state/sigil/approved/<key>`）。`<key>` はコンテナ名の key と同じ。
- 中身: `"sigil\0" + .sigil + "\0env\0" + <組から作った env ファイル>` の SHA-256 の 16 進表記（64 文字）と改行。組がなければ env ファイルの部分は空。比べるときは前後の空白と改行を無視する。
- コンテナを**新しく作るとき**に、`.sigil` か組があって記録と一致しなければ確認する:
  - 渡す環境変数名とポートの一覧（`.sigil` の分）を表示する。全体設定は別に渡される旨も表示する。
  - 組の名前ごとに、1Password の参照先（`op://vault/item/field`）を表示する。
  - 入力は `/dev/tty` から読む（標準入力ではない）。開けなければエラーで終了する。
  - `y` / `Y` / `yes` / `YES` 以外は中止。中止したら docker を呼ばない。
  - 承認したら記録を書く（フォルダ 0700、ファイル 0600）。
- 全体設定は確認しない。
- `op run` から起動し直された側（`SIGIL_INNER`）でも、記録との照合は行う（確認はしない）。一致しなければエラー。`SIGIL_INNER` を外から設定されても、承認を回避できないようにするため。

## 7. 1Password（op run）

`op run --env-file` は、`op://` の参照だけでなく、ファイル中のすべての `KEY=VALUE` を、起動し直した sigil 自身の環境変数にする。プロジェクトの `.env` をそのまま渡すと、次の問題がある。
- `.env` に `DOCKER_HOST` や `PATH` を書かれると、sigil が攻撃者の docker に秘密を送ったり、偽の docker を実行したりしてしまう。
- sigil が確かめたあとで `.env` を差し替えられると、確かめていない内容が使われる。
- sigil と op run で `.env` の解釈（改行、引用符など）がずれると、別の名前が設定されうる。

そこで、プロジェクトの `.env` からは、次をすべて満たす行だけを「1Password から読み込む名前と参照の組」として選ぶ（値は参照のときだけ読み、それ以外は保持しない）:
- 値が `op://` 参照である。参照ではない行（`VITE_API_URL` などのアプリの設定）は、アプリがコンテナ内で `.env` を直接読むので、sigil は扱わない。
- 名前が設定（`.sigil` / 全体設定）で列挙されている。列挙されていなければ、注意を出して使わない。
- 名前が 6 章の拒否する名前ではない。拒否する名前なら、注意を出して使わない。

参照に表示されない文字（ESC などの制御文字、U+202E などの書式文字、幅のない文字。`unicode.IsPrint` が偽のもの）・引用符・バックスラッシュ・`$`・バッククォートがあれば、行番号付きのエラーで終了する（確認の画面を偽装させない／op run の解釈を変えさせないため）。日本語と、半角・全角の空白は使える。

選んだ組だけで env ファイル（`NAME="op://..."` の行）を作り直し、本人だけが読める一時フォルダ（`$TMPDIR/sigil-op-*`、0700。ファイルは 0600）に置いて、それを op run に渡す。一時フォルダの場所は環境変数 `SIGIL_OP_TMP` で起動し直した側に伝え、そちらで消す（`$TMPDIR/sigil-op-*` の形のときだけ）。

全体の `secrets.env` はホストのファイルなのでそのまま渡すが、6 章の拒否する名前があればエラーにする。

`op://` を読み込むものがあるのに `op` が PATH にないときは、その旨を注意として出して続行する。

コンテナを**新しく作るとき**、`op` が PATH にあり、選んだ組があるか、全体の `secrets.env` に `op://` があれば、`op run` 経由で自分自身を起動し直す。

```
op run --no-masking --env-file=$TMPDIR/sigil-op-*/env --env-file=<config>/secrets.env -- <自分の実行ファイルの絶対パス> <元の引数...>
```

- `--env-file` は、読み込むものがあるファイルだけを、**プロジェクト → 全体の順**で付ける。後のファイルが優先されるので、プロジェクトの `.env` で全体のシークレットを上書きされないようにするため。
- `--no-masking` は必須。付けないと出力が加工され、Neovim などの対話的なプログラムが壊れる。
- 環境変数 `SIGIL_INNER=1` を付けて起動する。起動し直した側では、承認の確認と `op run` をしない（無限ループ防止）。
- `syscall.Exec` でプロセスを置き換える。**置き換えたあとの処理は続けない**ことをコードで明示する（テストで Exec を偽物にしたとき、そのままコンテナ作成に進まないように）。
- 確認（6 章）は `op run` より前に行う。1Password のロックを解除する前に、何を渡すかを見せるため。

## 8. コンテナの作成

- イメージ（`docker image inspect`）がなければ、「`sigil build` を実行してください」というエラーで終了する。
- `.git` がなければ、「ホスト側で git init / clone してください」と注意を出して続行する。

`docker run` の引数（順序は問わないが、最後の直前に `-w`、最後にイメージ名）:

```
run -d --init
--name <コンテナ名>
--hostname sigil
--label sigil.project=<プロジェクトのホスト側パス>
--label sigil.layout=2
--cap-drop=ALL
--security-opt=no-new-privileges
-v <project>:<コンテナ内パス>
-v <project>/.git:<コンテナ内パス>/.git:ro          # .git がある場合のみ
-v <home>/.config/nvim:/home/dev/.config/nvim:ro    # ホストにある場合のみ
-v <ro のホスト側パス>:/workspace/<名前>:ro          # .sigil の ro ごと（20 章）
-e SIGIL_PROJECT_DIR=<コンテナ内パス>
-e GIT_CONFIG_COUNT=1
-e GIT_CONFIG_KEY_0=safe.directory
-e GIT_CONFIG_VALUE_0=<コンテナ内パス>
-v sigil-nvim-data:/home/dev/.local/share/nvim
-v sigil-nvim-state:/home/dev/.local/state/nvim
-v sigil-claude:/home/dev/.claude
-v sigil-cache:/home/dev/.cache
-v sigil-go:/home/dev/go
-v sigil-cargo:/home/dev/.cargo
--tmpfs /home/dev/.cache/gh:rw,noexec,nosuid,nodev
-e <NAME>                          # 設定で渡す環境変数ごと。値は付けない
-p 127.0.0.1:<host>:<container>    # ポートごと。必ず 127.0.0.1
-w <コンテナ内パス>
<イメージ名>
```

理由（コードのコメントに残すこと）:
- `.git` を読み取り専用にするのは、ホストの git が実行する hooks や config を、コンテナから書き換えさせないため。
- Neovim の設定を読み取り専用にするのは、ホストの Neovim も同じファイルを読むため。
- ホーム、`~/.ssh`、`~/.aws`、Docker ソケットは**絶対にマウントしない**。
- `-e NAME` に値を付けないのは、docker が `sigil` のプロセスの環境から値を読むので、値がコマンドライン（`ps` で見える）に載らないため。
- `~/.cache/gh` に tmpfs を重ねるのは、gh のキャッシュ（`gh run view --log` の Actions のログなど）が非公開のリポジトリのデータで、共有の `sigil-cache` に置くとほかのプロジェクトのコードから読めるため（issue #12）。

`docker run` の標準出力（コンテナ ID）は捨て、標準エラーは表示する。

### WakaTime

作成後、コンテナに `WAKATIME_API_KEY` が渡っていれば、コンテナ内に `~/.wakatime.cfg` を作る。キーはコンテナ内の環境変数から読み、ホストのコマンドラインには載せない。

```
docker exec <name> sh -c '[ -n "${WAKATIME_API_KEY:-}" ]'                 # 成功したら次へ
docker exec <name> sh -c 'umask 077; printf "[settings]\napi_key = %s\n" "$WAKATIME_API_KEY" > "$HOME/.wakatime.cfg"'
```

## 9. 既存のコンテナ

- **状態の判定は終了コードで行う**。`docker container inspect` は、コンテナが存在しないときも、docker が使えないときも、**空行を出力して**失敗する。出力で判定すると存在すると誤判定し（シェル版で実際に起きた不具合）、失敗をすべて「ない」とすると Docker Desktop が止まっているときに誤った案内になる。
  - そこで `docker ps -a --filter name=<name> --format '{{.Names}}\t{{.State}}\t{{.Label "sigil.layout"}}'` を使う。名前が完全に一致する行がなければ「ない」。コマンドが失敗したら、「docker に接続できません。Docker Desktop が起動しているか確認してください」で終了する（`sigil stop` も同じ）。回帰テストを書くこと。
- ラベル `sigil.layout` が `2` でなければ、「このコンテナは古い sigil で作られています。sigil stop してから、もう一度実行してください」で終了する。
- 停止していれば `docker start`。
- 既存のコンテナに入るときは、設定の読み込み・承認・`op run` を行わない（作成時に渡したものが使われる）。

## 10. コンテナ内での実行

```
docker exec <-it または -i> -e TERM=xterm-256color -e COLORTERM=truecolor -w <作業フォルダ> <name> <command...>
```

- 標準入力と標準出力がどちらも端末なら `-it`、そうでなければ `-i`。
- `sigil check` で、ホストが色を止めているときだけ `-e NO_COLOR=1` を足す（16 章）。
- `TERM` を固定するのは、cmux（Ghostty 系）の独自の端末情報がコンテナ内にないため。
- `syscall.Exec` で docker に置き換える（Neovim や Claude Code に端末をそのまま渡すため）。docker は `exec.LookPath` で絶対パスにしてから渡す。

## 11. sigil build

```
docker build --build-arg USER_UID=<os.Getuid()> --build-arg USER_GID=<os.Getgid()> -t <image> [追加の引数...] <ビルドコンテキスト>
```

標準出力・標準エラーとも表示する（演出するときは 16 章のとおり）。終わったら一時フォルダを消す（`SIGIL_CONTEXT` を使ったときは何も消さない）。

### イメージに入れるもの

ツールは基本的にビルドの時点の最新版（決定事項を参照）。

- Neovim（公式リリース）、Claude Code（公式のネイティブインストーラー）、Node.js（最新の LTS）、Go、Rust、PHP（公式 `php:cli-trixie` が土台）、Composer、tree-sitter CLI
- **Python**: uv で最新の安定版を `/opt/uv-python` に入れ、`/usr/local/bin/python3`（と `python`）から指す。pip は同梱されているが、EXTERNALLY-MANAGED の印があるので venv の外では使えない（`pip install --user` も拒否される）。パッケージは venv（`uv venv` / `python3 -m venv`）か `uvx` で入れる。root の `~/.local/bin` にリンクを作らないよう `--no-bin` を付ける。
- Python のマイナー版は、イメージを作り直したときの最新に変わる。そのとき、前の版で作った venv（`python3 -m venv` は `/usr/local/bin/python3` を、`uv venv` は `/opt/uv-python/cpython-3.x-…` を指す）は動かなくなるので、作り直す（`uv sync` などは自動で作り直す）。
- **uv / uvx**: 公式イメージ `ghcr.io/astral-sh/uv` からコピーする。MCP サーバーの多くは `uvx <package>` で起動する。`uvx` のキャッシュは `~/.cache/uv`（`sigil-cache` で永続化済み）。このキャッシュは全プロジェクトで共有していて、あるプロジェクトのコードがキャッシュの中の環境を書き換えると、ほかのプロジェクトの `uvx` で実行されてしまう（issue #7 で扱う）。
- **wakatime-cli**: GitHub のリリースから取り、同じリリースの `checksums_sha256.txt` で照合して `/usr/local/bin/wakatime-cli` に置く。vim-wakatime は PATH の `wakatime-cli` を使い、Python による自動ダウンロードをしなくなる。
- `~/.local` と `~/.wakatime` は永続化しない（`uv tool install` で入れたものや、プロジェクトの指定で uv がダウンロードした別の版の Python（`~/.local/share/uv/python`）は、コンテナを作り直すと消え、次に使うときにまたダウンロードされる）。Claude Code の本体はイメージの `~/.local/bin/claude` にあるので、永続化するとイメージの更新が反映されなくなる。
- Neovim の Python プロバイダ（pynvim）は入れない。
- **LazyVim が使うツール**（issue #11）: apt から `lua5.1` / `liblua5.1-0-dev` / `luarocks`（lazy.nvim の rocks）、`libsqlite3-dev`（snacks.picker の履歴。FFI で `libsqlite3.so` を読む）、`bat`（`batcat` に `bat` のリンクを張る）、`git-delta`。lazygit は GitHub のリリースから最新を取り、同じリリースの `checksums.txt` で照合する。
- **差分の表示**: `/etc/gitconfig` に `core.pager = delta --navigate`、`interactive.diffFilter = delta --color-only` を書く（navigate を `[delta]` の節に書くと lazygit から呼ばれた delta にも効き、lazygit の中では動かずに見出しの印だけが出るため、git のページャーの引数にする）。lazygit の設定は `/etc/xdg/lazygit/config.yml`（`git.diffRenderers` に `delta --{{colorScheme}} --paging=never`）。どちらも root 所有。
- 画像の表示（snacks.image、`magick` / `chafa` など）、ゴミ箱（`trash-cli`）、`xdg-open`、Ruby / Perl / Node の Neovim プロバイダは入れない。プロバイダの警告を消す設定は `examples/nvim-sigil.lua` に載せる。
- **gh**（issue #12）: GitHub のリリースから最新を取り、同じリリースの `gh_<版>_checksums.txt` で照合して `/usr/local/bin/gh` に置く。新しい版の知らせは `GH_NO_UPDATE_NOTIFIER=1` で止める（更新はイメージの作り直しで行う）。トークンはイメージにも全体設定にも入れず、プロジェクトの `.sigil` / `.env` で `GH_TOKEN` として渡す（手順は README）。`gh` の設定（`~/.config/gh`）と拡張機能（`~/.local/share/gh`）は共有ボリュームに置かれないので、プロジェクトをまたいで alias や拡張機能が動くことはない。キャッシュ（`~/.cache/gh`）は共有の `sigil-cache` の中になるので、コンテナごとの tmpfs を重ねる（8 章）。
- `sigil-check` の「ツール」の一覧に、`python3` / `uv` / `gh` / `wakatime-cli` / `lua5.1` / `luarocks` / `lazygit` / `bat` / `delta` を含める。
- **`sigil-check` の GitHub のトークンの点検**（issue #12）: `GH_TOKEN` か `GITHUB_TOKEN` があるときだけ、「外界の鍵 ── GitHub のトークン」の欄を出す。値は表示しない。
  - 種類を先頭の文字列で判定する。`github_pat_`（fine-grained PAT）は ✓、`ghp_`（classic PAT）と `gho_`（OAuth のトークン）はリポジトリを絞れないので ✗、それ以外は注意。
  - 期限は、`gh api -i /rate_limit`（回数の制限に数えられない）の応答ヘッダー `github-authentication-token-expiration` から読み、あと何日かを出す。8〜90 日なら ✓、期限がない・90 日を超える・7 日以内なら注意、切れていれば ✗。401 なら ✗。問い合わせられないときは注意にとどめる。トークンは環境変数で `gh` に渡し、コマンドラインに載せない。
  - 注意（`!`）は失敗に数えず、最後の 1 行に件数を添える。
  - 権限そのもの（Contents の書き込みなど）は確かめない（決定事項を参照）。

## 12. 構成（builder の裁量で変えてよいが、テストしやすい分け方を保つこと）

```
.tool-versions              golang 1.27.4
go.mod                      module sigil / go 1.27
main.go                     app.Run の結果で終了コードを決めるだけ
image/
  Dockerfile, sigil-check
  embed.go                  package sigilimage。//go:embed で 2 ファイルを持ち、WriteContext(dir) で書き出す
internal/
  config/    .sigil / sigil の解析と統合（純粋関数）
  project/   プロジェクトの特定、危険なパスの拒否、コンテナ名、コンテナ内パス、作業フォルダ
  docker/    run / exec / build / ls の引数組み立てと、終了コードによる状態判定
  approval/  承認記録の読み書きと確認
  secret/    1Password・キーチェーンの参照の解析、優先順位、op run の argv、キーチェーンのパスの確かめと security の出力の解釈
  gitcommit/ sigil commit（設定の点検、メッセージの読み込み、差分の分類、コミットと push の手順）
  ui/        演出と、確認の画面の描画
  app/       サブコマンドの振り分けと全体の流れ
Makefile    build / install（~/.local/bin/sigil）/ test / vet
README.md
```

- `go:embed` は親フォルダを参照できないので、`image/` 自体を小さなパッケージにする。書き出すのは埋め込んだ 2 ファイルだけなので、`embed.go` はビルドコンテキストに入らない。
- 外部とのやりとりは `app.Deps` にまとめて差し替えられるようにする: コマンドの実行（docker / git。標準出力と終了コードを返す）、`Exec`、`LookPath`、`Getenv` / `Environ`、`Getwd`、`UserHomeDir`、`/dev/tty` を開く関数、端末かどうかの判定（`os.ModeCharDevice` で、標準ライブラリだけで判定）、`Executable`。
- 本物の `Exec` が成功すると戻ってこない。偽物の `Exec` は nil を返すので、呼んだ直後に必ず `return` する。

## 13. テストの要件

Docker・git・op・端末がなくても `go test ./...` が通ること。少なくとも次を確認する。

- 設定の解析: 正常系、各種の不正な行（コマンド置換 `$(id)` を含む）、拒否リスト、行番号付きのエラー、重複のまとめ
- 危険なパスの拒否（このとき docker を呼ばない）
- コンテナ名がテストベクトルと一致する
- コンテナ内パス（スペース、`:`、`,`、日本語）と作業フォルダの対応（前方一致だけの別フォルダを含む）
- `docker run` の引数: 隔離のためのオプションがある、ホームや秘密情報のフォルダをマウントしていない、値付きの `-e` がない（`SIGIL_` / `GIT_CONFIG_` を除く）、ポートが 127.0.0.1
- 存在しないコンテナで docker が空行を出す場合の判定（回帰テスト）
- 承認: 端末なし → 中止、`n` → 中止して docker を呼ばない、`y` → 作成して記録、記録済み → 聞かない、内容が変わる → また聞く
- `op run`: 引数とファイルの順序、`SIGIL_INNER=1`、置き換えたあとにコンテナを作らない、`op://` がなければ使わない、`SIGIL_INNER` のときは使わない
- 未設定の環境変数は渡さない
- キーチェーン: 19 章の「テスト」
- 読み取り専用のマウント: 20 章の「テスト」
- 既存のコンテナ: 実行中は再利用、停止中は start、古い layout はエラー
- `stop`、`-- stop`、`check`、`build` の引数
- 埋め込んだビルドコンテキストが書き出され、`sigil-check` に実行権限がある
- `sigil commit`（17 章）:
  - メッセージのファイル: シンボリックリンク・特殊なファイル・大きすぎるファイル・不正な UTF-8・双方向の書式文字を拒否し、リンク先を読まない。制御文字は取り除かれ、画面にもコミットにも残らない
  - 作業ツリーの中を指す `core.hooksPath` があっても、その hook はホストで実行されない（一時フォルダに作った本物の git リポジトリで、hook が印のファイルを作らないことを確かめる）
  - 作業ツリーの中から読み込まれる git の設定、進行中のマージなど、HEAD のサブモジュール、gitlink の追加・変更、LFS があれば拒否し、コミットしない（サブモジュールの拒否は、入れ子の設定の filter がホストで実行されないことまで確かめる）
  - 確認の画面を出したあとで作業ツリーが変わっても、コミットされるのは画面に出した tree である
  - `n` → コミットしない、端末なし → 中止、HEAD が途中で動いた → コミットしない（index も変えない）
  - upstream がなければ push を聞かない。push を断ればしない
  - 差分・ファイル名・メッセージ・フックの出力に含まれる ESC などが、画面ではエスケープとして出ない
  - フック: あればコンテナで実行し、失敗したらコミット（push）しない。フックの引数・pathspec をシェルの文字列に埋め込まない

## 14. 実機での検証

単体テストのあと、ホストの本物の docker で次を確認する。

ユーザーの既存の Docker 資源は消さない:
- `sigil-*` のコンテナ・ボリュームと、`sigil:latest` イメージは削除も上書きもしない。
- 使うのは `sigil:dev-test` と、一時フォルダのテスト用プロジェクトだけ。

1. `SIGIL_IMAGE=sigil:dev-test ./bin/sigil build` が通る（`image/` に問題があればここで直す）。
2. 一時フォルダに `git init` したテスト用プロジェクトを作り、`SIGIL_IMAGE=sigil:dev-test` で次を確認する:
   - `sigil check` がすべて ✓（sigil-check の結果を報告する）
   - `sigil -- nvim --version`、`sigil -- node --version`、`sigil -- go version`、`sigil -- cargo --version`、`sigil -- php --version`、`sigil -- python3 --version`、`sigil -- uv --version`、`sigil -- wakatime-cli --version`、`sigil -- claude --version`
   - 2 回目の実行で同じコンテナに入る
   - サブフォルダから実行すると、対応するフォルダで動く
   - `sigil stop` で消える
3. `.sigil` に `env SIGIL_TEST_VAR` を書き、ホストで `SIGIL_TEST_VAR=hello` を設定してコンテナを作る → コンテナ内で値が見える。承認のプロンプトは `/dev/tty` が必要なので、ユーザーに cmux のペインで実行・入力してもらう。
4. `sigil commit`（17 章）: テスト用プロジェクトに、作業ツリーの中を指す `core.hooksPath`（husky と同じ形）と、印のファイルを作る pre-commit / commit-msg / pre-push のフックを置き、コンテナの中でメッセージのファイルを書いてから、ホストで `sigil commit` を実行する（`/dev/tty` が必要なので、ユーザーに cmux のペインで実行してもらう）。
   - 印のファイルがコンテナの中でだけ作られ、ホストでは作られない
   - フックが失敗するとコミットされない
   - 読み取り専用の `.git` のままで、フックの中の `git add` などが動く（lint-staged を入れた場合も試し、`git stash` などで `.git` に書こうとして失敗しないかを報告する）
   - push は、テスト用に一時フォルダに作った bare リポジトリに対して行う
5. 読み取り専用のマウント: 20 章の「実機での検証」。
6. 後片付け: 自分が作ったテスト用コンテナ、`sigil:dev-test` イメージ、一時フォルダだけを消す。`sigil-*` の共有ボリュームは既存のものなので消さない。

`op` を使う流れ（7 章）は実機では確かめず、単体テストで担保する。キーチェーン（19 章）は、一時フォルダに作ったテスト用のキーチェーンで実機でも確かめる（19 章の「実機での検証」）。

## 15. 完了条件

- `go test ./...`、`go vet ./...`、`gofmt -l .` が通る。
- `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64` と `amd64` でビルドできる。
- 14 章の検証結果を報告している。
- `README.md`（日本語）に次を書いている:
  - インストール（Go 1.27.4 を asdf で入れる、`make install`）
  - `dev`（devbox）からの移行: 設定ファイル・コンテナ・ボリューム・イメージの名前が変わったことと、その移し方・片付け方。
  - 使い方
  - このリポジトリ自体をホストで動かすことの注意: コンテナ内で編集したら、ホストで `git diff` を確認してからビルドする。
- `Makefile` に `build` / `install`（`~/.local/bin/sigil`）/ `test` / `vet` がある。
- `docs/SPEC.md` を削除し、仕様はこの設計書に一本化されている。
- 18 章の CI が PR で通る。

## 16. 表示（演出）

issue #3。待ち時間のある処理で何が起きているかを見せ、使っていて楽しい見た目にする。描画は `internal/ui` に置き、標準ライブラリと ANSI エスケープだけで書く。

### 表示モード

| モード | 条件（上から順に判定） | 表示 |
|---|---|---|
| Plain | 標準エラーが端末でない / `SIGIL_PLAIN` が空でも `0` でもない / `TERM=dumb` | 演出なし。今までどおりの素のテキスト |
| Mono | `NO_COLOR` が空でない | 動きはあるが、色・太字などの SGR を出さない |
| Color | それ以外 | すべて |

- 端末の大きさは `TIOCGWINSZ` で問い合わせる（cgo なし）。演出は標準エラーの端末で、`ls` / `help` は標準出力の端末で測る。描き始めたあとの大きさの変更には追従しない（決定事項を参照）。幅 75 未満、または高さが魔法陣に足りないときは、魔法陣を省いて 1 行のスピナーにする。
- 外周の文字は既定でルーン文字（古フサルク）。`SIGIL_GLYPHS=greek` でギリシャ文字にする（ルーン文字のないフォント向け）。
- `SIGIL_PLAIN` / `SIGIL_GLYPHS` は `config.deniedEnvs` に入れる。`NO_COLOR` と `TERM` は入れない（決定事項を参照）。

### 守ること

- 演出は標準エラーにだけ出す。標準出力には何も足さない（`sigil -- go test ./... | ...` を壊さないため）。
- **`syscall.Exec`（docker exec / op run）の直前に、必ず端末を元に戻す**（行の消去、SGR のリセット、カーソルの表示）。Neovim や Claude Code の画面を壊さないため。描画中に SIGINT / SIGTERM を受けたときも、元に戻してから本来の動作（終了）にする。無視された状態で起動されたシグナル（`nohup` など）は受け取らない（無視を外さない）。
- 描画中は、docker の標準エラーをバッファにためて、描画を終えてから表示する（画面が崩れないように）。
- 既存のコンテナに入るときに、待ち時間を足さない。
- 判断の画面（`.sigil` の承認、`sigil commit` の確認）は、色・枠・記号で見やすくするが、動きは付けない。動きは、答えを受け取ったあとの処理にだけ付ける。入力を待つ行を描き直すと、打った文字とぶつかって崩れるため。また、画面を少しずつ出すと待ち時間が増えるため。
  - 意味を色だけで伝えない。`+` / `-`、`⚠` などの記号と言葉を必ず添える（Mono や色覚の違いでも読めるように）。
  - 用語には、今までどおり実際の意味を添える。
- 画面に出すプロジェクト名などから、制御文字を取り除く（フォルダ名で端末を操作されないように）。
- コンテナが書いた内容（差分、ファイル名、コミットメッセージ、フックの出力）は、制御文字を見える形（`^[`、`<U+202E>` など）に置き換えてから、sigil 自身が色を付ける。git などが付けた色（`--color` の出力）はそのまま流さない。コンテナの ESC が混ざると、偽の表示を作れるため。

### 場面

- **コンテナの作成**: バナー（`SIGIL`、キャッチコピー「seal your code, guard your keys」）→ 左に回り続ける魔法陣、右に段階の一覧 → 完了の瞬間に魔法陣が白く明滅 → 結果の枠 → 「領域へ入ります」。
  - `docker run`（と WakaTime の設定）を別の goroutine で動かし、終わるまで描き続ける。待ち時間を足すことはしない。ただし、完了の明滅（3 回、約 0.5 秒）の分だけは待つ（決定事項を参照）。
  - 段階: 契約を読み解く → 禁書庫から召喚する → 禁忌の扉を閉ざす → 権能を剥奪する → 結界を張る → 領域を展開する。最後以外は、描き始めた時点で済んでいる（✔）。op run を使っていなければ「禁書庫から召喚する」は省略の表示。前半が一瞬で済む見え方は、実機で見てから見直す。
  - 失敗したら、その段階に ✗ を付け、ためていた docker の標準エラーを出してから、エラーにする。
- **停止中のコンテナ**: 1 行のスピナー（領域の再展開）のあと、入る 1 行。
- **既存のコンテナに入る**: 待たずに 1 行だけ出す（`⟡ 領域 <name> に入ります`）。
- **`sigil build`**: バナー → 魔法陣と段階の一覧（素材を集める → 紋章を鋳造する `[k/M] <手順>` → 紋章を刻む）と進み具合のバー → 完了の 1 行（経過時間つき）。
  - `--progress=plain` を足して出力を読む（`docker.BuildWatcher`）。Dockerfile の中身には依存せず、手順の数がいちばん多いステージを最終ステージとみなし、その済んだ手順（`DONE` / `CACHED`）の数で進み具合を出す。
  - docker のログはすべてためておき、成功したら捨て、失敗したらそのまま全部見せる。
  - 1 ステージの Dockerfile では手順にステージ名が付かない（`[2/3] RUN ...`）ので、名前がなくても読む。
  - 利用者が `--progress` / `-q` を指定したとき、`DOCKER_BUILDKIT` が偽（docker と同じく `strconv.ParseBool` で読み、`0` / `false` など）のときは、演出せず今までどおりそのまま流す。
- **`sigil ls`**: 標準出力が端末のときだけ、`docker ps` をタブ区切りで読み、状態ごとに印と色を付けて出す（● 展開中 / ○ 停止中 / ◐ 休止中）。パイプやリダイレクトでは、今までどおり `docker ps` の表をそのまま出す。
- **`sigil help`**: 標準出力が端末のときだけ、説明の前にバナーを付ける。
- **`sigil check`**: 整形は `sigil-check` の側で行う。見出しは世界観の言葉に普通の言葉を添え（例: `■ 禁忌の扉 ── ホストの秘密情報が見えないこと`）、最後に「結界は完全です」/「結界に綻びがあります」と、項目の数を出す。色は、標準出力が端末で `NO_COLOR` がないときだけ付ける。ホストが Color でないとき（`NO_COLOR`、`SIGIL_PLAIN`、パイプ）は、`sigil check` に限って `docker exec -e NO_COLOR=1` を渡す（値は秘密ではないので、コマンドラインに載せてよい）。

### 用語

| 実際の処理 | 用語 |
|---|---|
| コンテナ | 領域 |
| コンテナを作る / 既存のコンテナに入る / 停止中のコンテナを起動 | 領域展開 / 領域に入る / 領域の再展開 |
| `sigil stop` / `sigil ls` / `sigil check` / `sigil build` | 領域解除 / 展開中の領域 / 結界の点検 / 紋章の鋳造 |
| 隔離（cap-drop、マウントの制限） | 結界 |
| イメージ | 紋章 |
| `.sigil` / `.env` の承認 | 契約 |
| 渡す環境変数 / ポート | 持ち込むもの / 門 |
| 読み取り専用でマウントするフォルダ（20 章） | 写本 |
| 1Password（op run） | 禁書庫（「禁書庫から召喚する」） |
| `~/.ssh` などを渡さない / 権限を落とす | 禁忌の扉を閉ざす / 権能の剥奪 |
| 古い layout のコンテナ | 旧き紋章の領域 |
| GitHub のトークン（`sigil check` の見出し） | 外界の鍵 |
| `sigil commit` / コミット | 刻印の儀 / 刻印 |
| コミットメッセージ（とそのファイル） | 銘 |
| フック（コンテナで実行） | 試練 |
| push / push 先 | 解き放つ / 外界 |

分かりやすさを優先する場面:

- **エラー**: 1 行目は世界観の言葉、2 行目に普通の言葉での説明と対処を書く（`loreError`）。2 行目は今までのメッセージそのままで、`sigil: ` の幅だけ字下げする。すでに言葉が添えてあるエラーを外側で包むときは、内側の言葉を優先する。
  ```
  sigil: 紋章がありません
         イメージ sigil:latest がありません。先に sigil build を実行してください
  ```
- **注意と案内**: 用語だけで書き、実際の意味を括弧で添えることはしない（例: `禁書庫から召喚します`、`領域 <name> を解除しました`）。括弧の中が本当の理由になる場合は、文に組み込む（例: `注意: op が見つからないので、禁書庫を開けません。op:// の参照は読み込みません`）。
- **承認の画面**: 枠で囲み、見出し（`⟡ 契約の確認`）と、区分ごとの印と色を付ける。動きは付けない。用語の横に実際の意味を必ず添え、値の列をそろえる（例: `持ち込むもの（ホストの環境変数）  DATABASE_URL`）。答えを読むところは今までどおり（`/dev/tty` から 1 行）。承認したあとは、今までどおり領域展開の演出に続く。Plain では今までどおりの素のテキスト、Mono では色を付けない。
- **`--help`**: 普通の説明を主にし、用語は〔〕で添える程度にする。用語の対応表は載せない（メッセージの側で、用語には必ず意味が添えてあるので冗長になる）。
- 承認の記録を「聖櫃」と呼ぶ案は、使わない（検討中のため）。

## 17. sigil commit

issue #13。コンテナの `.git` は読み取り専用（8 章）なので、コンテナの Claude Code はコミットできない。コンテナが用意したメッセージで、ホストの sigil が、利用者の確認のうえでコミットと push をする。コミットと push は、ホストの git と、利用者自身の設定・認証（`~/.ssh`、署名）で行う。コンテナが書けるのは、作業ツリーとメッセージのファイルだけ。

### 前提: 作業ツリーの中にあるものは、すべて信頼できない

コンテナは作業ツリーを自由に書き換えられ、`sigil commit` の確認の最中にも動いている。そこで次を守る。

- **ホストでフックを実行しない**。husky などは `.git/config` の `core.hooksPath` を作業ツリーの中（`.husky/_` など）に向けているので、ホストの git がそのまま実行すると、コンテナが仕込んだコードがホストで動く。`.git/hooks/` に置かれたフックも、中から作業ツリーのコード（`node_modules`、`.pre-commit-config.yaml` など）を呼ぶ形が多いので、同じく実行しない。フックは代わりにコンテナで実行する（下の「フック」）。
- **見せたものと、コミットするものを一致させる**。確認のあとで `git add` すると、そのあいだにコンテナが書いた変更が紛れ込む。先に tree を確定して見せ、その tree をコミットする。
- **コンテナが書いた内容は、画面に出す前に制御文字を処理する**（16 章）。

### 流れ

1. **前提を確かめる**。標準入力と標準出力が端末であること。プロジェクトが git リポジトリで、HEAD がブランチを指していること（まだコミットがないブランチでもよい）。満たさなければエラー。
2. **拒否する条件を調べる**（下の「拒否する条件」）。1 つでも当てはまれば、理由を出して終了する。
3. **銘（メッセージのファイル）を読む**（下の「メッセージのファイル」）。
4. **pre-commit の試練**: フックがあれば、コンテナで実行する。失敗したら、フックの出力を見せて終了する。
5. **tree を確定する**。本人だけが読める一時フォルダ（`$TMPDIR/sigil-commit-*`、0700）に、本物の index を写し（更新時刻も引き継ぐ。最初のコミットで HEAD がないときは写さず、空の index から始める。利用者が add 済みの別のファイルが紛れ込まないように）、`GIT_INDEX_FILE` でそれを指して、`git reset -q HEAD -- :/`（HEAD にそろえる。ファイルの状態の記録は残るので、全部を読み直さずに済む。パスを付ける形にするのは、付けない `git reset` が本物の `ORIG_HEAD` と reflog に書くため）→ `git add -A -- <add の pathspec>` → 銘のファイルを一時 index から戻す（HEAD があれば `git reset -q HEAD -- ':(top,literal).sigil-commit'`、なければ `git rm --cached -q --ignore-unmatch -- ':(top,literal).sigil-commit'`）→ `git write-tree` を行う。pre-commit のフックが作業ツリーを整形していれば、整形後の内容がここで入る。銘のファイルを `add` の除く指定（`:(exclude)`）で外さないのは、`.gitignore` 済みのファイルを pathspec で名指しすると、除く指定でも `git add` が「無視されたパス」として失敗するため（README で `.gitignore` に入れるよう勧めているので、普段の使い方そのもの）。戻す手順は、無視されていても、追跡されていても（HEAD の内容のまま）、なくても失敗しない。
6. **prepare-commit-msg / commit-msg の試練**: フックがあれば、コンテナで実行する。フックが銘を書き換えたら、3 と同じ検査をやり直す。
7. **確認の画面を出す**（下の「確認の画面」）。答えは `y` / `n` / `d`。`d` なら全差分をページャーで見せ、もう一度聞く。`y` / `Y` / `yes` / `YES` 以外は中止（コミットしない）。
8. **刻印する**。`git commit-tree <tree> [-p HEAD] -F -`（銘は標準入力から渡す）で commit を作り、`git update-ref -m "commit (sigil): <件名>" <ブランチ（refs/heads/<name>）> <新> <旧>` で、HEAD が 1 のときから動いていないことを条件にブランチを進める。動いていれば（別の端末でコミットされたなど）、エラーにして index も変えない。そのあと本物の index で `git reset -q -- <add の pathspec> ':(exclude,top).sigil-commit'` を行い、コミットした分を HEAD にそろえる（pathspec の外の、利用者が add してあった状態は残す）。`reset` は `.gitignore` の影響を受けないので、ここでは除く指定を使う。銘のファイルはコミットしていないので、利用者が add してあった銘のファイルの変更を消さないため。
9. **銘のファイルを消す**。
10. **push を確かめる**（下の「push」）。

### コマンドの引数

- `sigil commit`: 変更のあるものすべて（追跡していない、無視されていないファイルを含む）をコミットする。add の pathspec は `:(top)`（最上位の全体）。銘のファイルは add のあとで戻す（5）。`top` を付けるのは、コンテナの pre-commit で `add` するときも、cwd に左右されないため（`:(top).` は `.` という名前として読まれ、何にも一致しないので使わない）。
- `sigil commit -- <path>...`: 指定したものだけ。add の pathspec は `:(top,literal)<path>...`（最上位からのパスが `.` なら `:(top)`）。`literal` を付け、glob として読まない（`a[b].txt` が `ab.txt` まで拾うと、確認の画面の範囲と違うファイルが入るため）。ホストの `add`、コンテナの pre-commit の `add`（と、そのあとの銘のファイルを戻す手順）、刻印後の本物の index の `reset`（こちらは除く指定を足す。8）のすべてで同じものを使う（コードでは `AddPathspec` 1 か所で作る）。確認の画面には magic を付けない素のパスを出す。これは端末の前にいる利用者が打つものなので、信じてよい。**コンテナが指定したファイルの一覧は使わない**（`.github/workflows` などを紛れ込ませられないように）。
- `<path>` は git と同じく、今いるフォルダ（リンクを解いたもの）からのパスとして読み、最上位からのパスに直して使う（ホストの git は `-C <root>` で呼ぶため）。プロジェクトの外を指すもの（`../..` など）はエラー。
- `:` で始まる指定（pathspec の magic、`:(top)` や `:!` など）は受け付けない。銘のファイルを外す指定や、今いるフォルダからという決まりをすり抜けられるため。
- 最上位からのパスが銘のファイルそのもの（大文字と小文字は区別しない）なら、「銘のファイルで、コミットには含めません」とエラーにする（黙って外すと「刻むものがありません」などの紛らわしい結果になるため）。
- 銘のファイルは、どちらの場合も必ずコミットから外す。追跡されていたら注意を出す（刻印のあとに消すので、作業ツリーに追跡されたファイルの削除が残ることも添える）。
- push の URL（`git remote get-url --push`）は、画面に出すときも、コンテナの pre-push のフックに渡すときも、URL の形なら userinfo（`user:token@`）を取り除く。scp の形（`git@host:path`）の user は秘密ではないので残す。本物の push はリモートの名前で行う。

### ホストの git の呼び出し

すべての呼び出しに次を付ける。どれも、作業ツリーから来た内容でホストのコマンドを動かさないため。

```
git -C <root> -c core.hooksPath=/dev/null -c core.fsmonitor=false -c submodule.recurse=false -c diff.ignoreSubmodules=all ...
```

- `commit-tree` / `update-ref` はフックを実行しないが、ほかの呼び出しのため、それでも付ける（`-c` は子の git にも引き継がれる）。
- 差分の表示は `--no-color --no-ext-diff --no-textconv -z` で取る。textconv と外部の diff は、コンテナが書いた内容をホストのプログラムで処理することになるため。ファイル名は `-z` で読み、git による引用（`core.quotePath`）に頼らず、sigil が処理する。
- `commit-tree` は `commit.gpgSign` を見るので、署名の設定はそのまま効く。署名のときに pinentry や 1Password の確認が出ることがあるので、8 の手順は演出を描かずに行う。
- `git add` は、利用者の全体設定にある filter（`.gitattributes` で指定されたもの）を実行する。これは利用者が入れた道具なので許す（LFS は下のとおり拒否する）。
- これらの git の実行は、`app.Deps` を通して差し替えられるようにする（環境変数と標準入力を渡せるもの。`GIT_INDEX_FILE` と銘のため）。

### 拒否する条件

- **作業ツリーの中から読み込まれる設定がある**。`git config --show-origin --list` の読み込み元に、作業ツリーの中（`.git` の外）のファイルがあれば拒否する（`[include] path = ../.gitconfig` など）。コンテナが設定を書き換えられると、`core.sshCommand`、`gpg.program`、`credential.helper` などで、ホストでコードを実行できる。`-c` で 1 つずつ打ち消すことはできない。`include.path` と `includeIf.*.path` は、値を解決して（先頭の `~/` はホーム、相対パスは書かれたファイルのあるフォルダから）、作業ツリーの中（`.git` の外）を指すなら、ファイルがまだなくても拒否する（のちに作られると読み込まれるため）。見かけのパスと、リンクを解いたパスのどちらかが中なら拒否する。
- **`.git` が `<Root>/.git` の本物のフォルダでない**。GitDir と CommonDir がどちらも `<Root>/.git`（リンクを解いて比べる）で、それが `lstat` でフォルダ（リンクや gitfile でない）でなければ拒否する。リンクされた worktree と `--separate-git-dir` は対応しない（手動でコミットしてもらう）。`.git` が作業ツリーの中を指す gitfile だと、「`.git` の中の設定は安全」という前提が崩れるため。Root が `git rev-parse --show-toplevel` と違うときは、呼び出しの誤りとしてエラーにする。
- **進行中の操作がある**。`MERGE_HEAD`、`CHERRY_PICK_HEAD`、`REVERT_HEAD`、`rebase-merge/`、`rebase-apply/` があれば拒否する。`commit-tree` では、マージの親などを正しく残せないため。
- **HEAD にサブモジュール（gitlink）がある**。2 の段階で、`add` より前に `git ls-tree -r -z --full-tree HEAD` を調べ、mode 160000 のものが 1 つでもあれば、変更がなくても「サブモジュール（gitlink）を含むリポジトリには対応していません。手動でコミットしてください」と出して拒否する。5 の `git add -A` は、サブモジュールの作業ツリーを調べるために、その中で git（`status`）を動かす。その子の git は入れ子のリポジトリの設定と `.gitattributes` を読むが、それはコンテナが書ける場所にある（`sub/.git/config` を書く、`sub/.git` の gitfile を差し替える）。`filter.<x>.clean` などがホストで実行され、`-c` の上書きでは防げない（キーの名前をコンテナが決められるため。git 2.56.1 で再現した）。
- **gitlink の追加・変更がある**。確定した tree と HEAD の差分に mode 160000 の追加・変更があれば拒否する。コンテナが作った入れ子のリポジトリ（`sub/.git/config`）は、のちにホストの git が中の設定（fsmonitor など）を読むきっかけになる。
- **LFS を使っている**。確定した tree に入るパス（削除を除く）を `git check-attr -z --stdin filter` に渡し、`lfs` になるものがあるか、作業ツリーの最上位に `.lfsconfig` があれば、「LFS のリポジトリには対応していません。手動でコミット・push してください」と出して終了する。LFS の実体は pre-push のフック（ホストの `git lfs`）で送られるので、フックを止めると届かない。ホストで `git lfs push` を呼ぶと、作業ツリーの `.lfsconfig` を読むことになる。`check-attr` は 2 回呼び、どちらかが `lfs` なら拒否する。1 回目は `--source=<確定した tree>` で、tree に入っている属性を読む（`add` のあとにコンテナが作業ツリーの `.gitattributes` を書き換えても、コミットする tree で判断するため）。2 回目は作業ツリー（と一時の index）で読む（`.gitignore` 済みで tree に入らない `.gitattributes` も、`add` のときには git が従っていたため）。
- **フックの場所がプロジェクトの外にある**（下の「フック」）。
- **コミットするものがない**（確定した tree が HEAD と同じ）。「刻むものがありません」と出して終了コード 1。

### メッセージのファイル

- 場所: プロジェクトの最上位の `.sigil-commit`。コンテナの Claude Code が書く。利用者のプロジェクトの `.gitignore` に入れてもらう（README とひな形に書く）。sigil は `.gitignore` を書き換えない。
- 読み方: `O_NOFOLLOW` で開いてから `Fstat` で確かめる。シンボリックリンク、通常のファイル以外、64KiB を超えるものは拒否する（確かめてから開くまでの差し替えも防ぐ）。上限を超えて読まない。
- 検査: 正しい UTF-8 であること。双方向の書式文字（U+202A〜U+202E、U+2066〜U+2069、U+200E、U+200F、U+061C）があれば拒否する（画面の見た目と、コミットされる文字の並びを変えられるため）。
- 整形: 先頭の BOM と CR を取り除き、`\n` と `\t` 以外の制御文字（C0、DEL、C1）を取り除く。`#` で始まる行は git のようには消さない（見せたとおりにコミットするため）。前後の空行を落とす。整形のあとで空なら、「銘がありません」とエラー。
- **コミットするのは、整形して画面に出した内容**。メモリから標準入力で渡し、ファイルを読み直さない。

### フック

フックの中身はプロジェクトのコードなので、コンテナで実行する。コンテナが結果を偽っても、フックは品質の関所なので、セキュリティ上は何も失わない。

- **対象**: pre-commit、prepare-commit-msg、commit-msg、pre-push。post-commit などは実行しない（関所ではないため）。
- **場所**: ホストで、設定ファイルの `core.hooksPath`（相対パスは作業ツリーの最上位から。空の値は最上位。`~user/` と `%(prefix)/` は対応しない）から求めたフォルダ。なければ `.git/hooks`。`git rev-parse --git-path hooks` は使わない（sigil は常に `-c core.hooksPath=/dev/null` を付けて git を呼ぶので、答えが変わってしまうため）。プロジェクトの中（`.git/hooks` を含む）なら、コンテナ内のパスに読み替える。プロジェクトの外（全体設定の `core.hooksPath` など）はコンテナから見えないので、フックがあれば拒否する。
- **あるかどうか**: ホストで、そのフォルダにその名前の実行できるファイルがあるかで判定する。1 つもなければ docker を使わない。
- **コンテナの状態**: フックがあるのにコンテナが実行中でなければ、「sigil で領域を開いてから、もう一度実行してください」とエラー。
- **実行のしかた**: `docker exec -i -w <コンテナ内の最上位> <name> sh -c '<sigil が持つ固定のスクリプト>' sh <フックのパス> <引数...>`。フックのパス・pathspec・リモート名などは位置引数で渡し、**シェルの文字列に埋め込まない**。
  - コンテナの `.git` は読み取り専用なので、スクリプトは `mktemp` で一時の index と object のフォルダを作り、`GIT_INDEX_FILE`、`GIT_OBJECT_DIRECTORY`、`GIT_ALTERNATE_OBJECT_DIRECTORIES=<.git の objects>` を設定してから、本物の index を写して `git reset -q` → `git add -A -- <add の pathspec>` → 銘のファイルを戻す（5 と同じ。HEAD の有無はスクリプトの中で判断する）を行い、フックを実行する。フックの中の `git add` などは、この一時の場所に書く。
  - prepare-commit-msg / commit-msg: 銘を標準入力で渡し、スクリプトが一時ファイルに書いてからフックに渡す。フックの出力は標準エラーに回し、フックのあとの銘を標準出力に出す。ホストはこれを 3 と同じく検査する。
  - pre-push: 引数は `<リモート名> <URL>`、標準入力は `<local ref> <local sha> <remote ref> <remote sha>` の 1 行。新しい commit はホストの `.git/objects` にあり、コンテナにも見えている。
- **出力**: 描画中はためておき、成功したら捨て、失敗したら制御文字を処理して全部見せる（`sigil build` と同じ）。
- 確認の画面に、実行したフックの結果（✔ / ✗）と、「ホストでは hooks を実行していません」を出す。

### 確認の画面

標準エラーに出す。枠と色で飾るが、動きは付けない（16 章）。上から次の順。

1. 見出しの枠: `⟡ 刻印の儀 ── コミットの確認`、ブランチ、HEAD の短いハッシュと件名、pathspec（指定したときだけ）。
2. **刻むもの（コミットに含まれるファイル）**: 確定した tree と HEAD の差分の、すべてのファイル。状態ごとの印・言葉・色（`+ 追加` 緑、`~ 変更` 黄、`- 削除` 赤、`→ 名前の変更` シアン）と、増減の数とバー（`+12 -3 ████▌`）。バイナリは大きさを出す。判断の画面では、内容（パス、銘、理由、push 先、参照）を切り詰めず、端末の幅で折り返す。
3. **⚠ 確かめてほしいもの**（あるときだけ。赤い枠で、ファイルごとに理由を添える）:
   - CI（`.github/`、`.gitlab-ci.yml`、`.circleci/`）: 「GitHub などで実行されます」
   - 秘密らしいもの（`.env`、`.env.*`、`*.pem`、`*.key`、`id_rsa*`、`id_ed25519*`）
   - git の動作を変えるもの（`.gitignore`、`.gitattributes`、`.gitmodules`、`.lfsconfig`）
   - フック（フックのフォルダ、`.husky/`、`.pre-commit-config.yaml`、`lefthook.yml`）: 「ほかの人の環境や CI で実行されます」
   - sigil の設定（`.sigil`）
   - 実行権限が付くもの、シンボリックリンク（リンク先を添える）、1MiB を超えるもの
4. **銘（コミットメッセージ）**: 件名を太字、本文を字下げして出す。件名が 72 文字を超えれば注意を添える。
5. **試練（フック）**: 実行したものの ✔ / ✗。「ホストでは hooks を実行していません」。
6. 問い: `sigil: この内容で刻印する（コミットする）なら y、差分を見るなら d を入力してください [y/N/d]`。答えは `/dev/tty` から 1 行読む。

- `d` の全差分は、ホストに `delta` があり、色を付けるモード（`NO_COLOR` も `SIGIL_PLAIN` もなく、標準エラーが端末）なら delta で整形する。まず行ごとに制御文字を見える形に置き換え（改行とタブは残す）、`delta --no-gitconfig --paging=never --line-numbers` の標準入力に渡す（絶対パスで呼び、tty は引き継がず、環境変数は `DELTA_FEATURES=`（空）を子プロセスにだけ足す）。`--no-gitconfig` は、利用者の git config の `[delta]` を読ませないため。`DELTA_FEATURES` を空で上書きするのは、`DELTA_FEATURES=hyperlinks` などが `--no-gitconfig` でも効くため（実機で確認）（`hyperlinks` が有効だと、コンテナが決めたファイル名が OSC 8 のリンクになり、macOS で `.command` などをクリックすると実行されうる）。出力は delta 自身の SGR と `ESC[K` だけのはずなので、それ以外の制御（OSC・BEL・C1 制御・不正な UTF-8 など）を含んでいたら捨てて、組み込みの表示に戻す（`less -R` は OSC 8 を生で通すため）。delta が見つからない・失敗したときも、下の組み込みの表示に戻す（失敗したとき・出力を捨てたときは `sigil: 注意:` の行を 1 行出す）。Plain / Mono でも組み込みの表示を使う。
- 組み込みの表示は、制御文字を処理したうえで sigil が色を付ける（追加は緑、削除は赤、hunk の見出しはシアン、ファイルの境目は枠）。ページャーは `less -R -S`（標準入力から渡し、出力は標準エラーの端末）。`-R` を付けてよいのは、残っているエスケープが sigil の付けたものだけだから。`less` がなければ、そのまま出す。
- Plain では、記号と言葉だけの素のテキスト。Mono では色を付けない。

### 刻印のあと

- 刻印（8）は、署名の確認が端末に出ることがあるので、描画せずに行う。済んだら、完了の明滅（作成時と同じ、約 0.5 秒）とともに結果の枠を出す: `✔ 刻印しました`、ブランチ、短いハッシュ、件名。
- フックをコンテナで実行するあいだ（4、6、push の前）は、実際の待ち時間なので、魔法陣と段階の一覧（またはスピナー）を描く。段階の例: 試練に挑む（pre-commit）→ 紋様を写し取る（tree の確定）→ 銘を検める（commit-msg）。

### push

- upstream（`@{u}`）がなければ聞かない。「upstream がないので解き放ちません。`git push -u <remote> <branch>` を手動で実行してください」と案内する。push 先を推測しない。
- あれば、push 先を見せる: リモート名、push の URL（`git remote get-url --push`。制御文字を処理する）、ブランチ（`branch.<name>.merge`）、送る commit の数。
- `sigil: 外界へ解き放つ（push する）なら y を入力してください [y/N]`。`/dev/tty` から読む。
- 送る commit の数、pre-push の標準入力の local sha、push するものは、すべて刻印したコミットの id にそろえる（HEAD は読まない）。
- `y` なら、pre-push のフックがあればコンテナで実行し、失敗したら push しない。続けて `git ... push --no-verify --recurse-submodules=no <remote> <刻印したコミットの id>:<merge の ref>` を実行する。`HEAD` を送ると、確認や pre-push のあいだに HEAD が動いたとき、利用者が見ていないコミットまで送ってしまうため。`--recurse-submodules=no` は、利用者の全体設定（`push.recurseSubmodules`）があっても、サブモジュールの中で git を動かさないため。SSH のパスフレーズなどを端末で聞かれることがあるので、描画はせず、git の出力はそのまま流す（相手は利用者が設定したリモートで、コンテナではない）。
- 断られたら push せず、コミットは残す。push に失敗しても、コミットは残し、その旨を出して終了コード 1。

## 18. CI（GitHub Actions）

issue #5。sigil はホストで動く唯一の信頼された道具なので、CI もサプライチェーン攻撃を前提に組む。

### ワークフロー

| ファイル | きっかけ | すること |
|---|---|---|
| `.github/workflows/ci.yml` | main への push、`pull_request` | テスト・静的検査・ビルド・外部モジュールがないことの確認・Codecov への送信 |
| `.github/workflows/vulncheck.yml` | main への push、`pull_request`、週 1 回の `schedule`、`workflow_dispatch` | govulncheck だけ |

- **ci.yml の test**: `ubuntu-latest` と `macos-latest` で回す（大文字小文字を区別しないファイルシステムなど、macOS でしか意味を持たないテストがあるため）。
  - `go test -race -coverprofile=coverage.out ./...`、`go vet ./...`、`gofmt -l .` の出力が空であること。
  - `-race` は cgo を使うが、テストのときだけ。sigil 本体のビルドは `CGO_ENABLED=0` のまま（3 章）。
- **ci.yml の build**: Linux のランナーから `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64` と `amd64` で `go build -trimpath` する。
- **外部モジュールがないこと**: `go list -m all` の出力が `sigil` だけであることを確かめる。`go.mod` を grep するより、Go 自身の解釈で見るほうが確実なため。
- **Codecov**: ubuntu の `coverage.out` だけを送る。`CODECOV_TOKEN` は Secrets に入れる。フォークからの PR には Secrets が渡らないので、トークンなしでの送信（公開リポジトリで使える tokenless upload）に任せる。`pull_request_target` は使わない（フォークのコードに Secrets を渡すため）。Dependabot の PR も `CODECOV_TOKEN` が空になるが、フォークではないのでトークンなしの送信に切り替わらず、Codecov に断られる。Actions の更新だけなので、カバレッジは要らないものとし、`fail_ci_if_error: false` で CI は通す。codecov-action は実行時に Codecov の CLI を別にダウンロードし、これはロックファイルで固定されないので、action の入力で CLI の版も固定する。この版は Dependabot では上がらないので、手で上げる（四半期に 1 回を目安にする）。README にバッジを付ける（公開リポジトリなので、表示用のトークンは付けない）。
- **govulncheck**: `go install golang.org/x/vuln/cmd/govulncheck@<版>` で版を固定して入れ（`@latest` にしない）、`govulncheck ./...` が脆弱性を見つけたら失敗させる。darwin/arm64・darwin/amd64・linux を対象に調べる（アーキテクチャごとのコードにだけある脆弱性を取りこぼさないため）。ランナーの中だけで使い、ホストにも `go.mod` にも入れない（外部モジュールを入れない方針は sigil 本体のコードの話で、CI の道具は当たらない）。対処は `.tool-versions` と `go.mod` の Go の版を上げること。脆弱性はコードを変えなくても後から公表されるので、定期実行でも動かす。govulncheck の版は Dependabot では拾えないので、手で上げる（四半期に 1 回を目安にする）。
- **Go の版**: `actions/setup-go` に `go-version-file: .tool-versions` を渡し、手元と同じ版にする。`GOTOOLCHAIN=local` にして、`go.mod` を理由に別の版のツールチェーンをダウンロードさせない。

### 守ること

- **`permissions: contents: read`** をワークフローの先頭に書き、書き込み権限を渡さない。
- `actions/checkout` は `persist-credentials: false` にする（後の手順にトークンを残さない）。
- どのジョブにも `timeout-minutes: 15` を付ける（テストが FIFO や git の入力待ちで止まったときに、既定の 360 分までランナーを使い続けないため）。
- 使う Actions は `actions/checkout`、`actions/setup-go`、`codecov/codecov-action` に限る。別のリポジトリの再利用ワークフローとコンテナイメージは使わない（ロックファイルで固定されないため）。

### Actions の固定（gh-actions-lock）

GitHub 公式の gh の拡張 `github/gh-actions-lock` で、ワークフローが使う Actions を、間接的に使うもの（composite action の中など）も含めて、正確なコミットに固定する。固定した結果は `.github/workflows/actions.lock` に記録し、コミットする。

- **ロックファイルは手元（ホスト）で作る**。ワークフローを足したときと、`uses:` を変えたときに `gh actions-lock` を実行する。コミットの前に `gh actions-lock --verify-local`（オフラインで、すべての `uses:` がロックファイルにあるかを見る）を通す。拡張はホストに版を固定して入れる（`gh extension install github/gh-actions-lock --pin <版>`）。
- ロックファイルに載ったワークフローは、YAML のタグではなく、ロックされたコミットで動く（docs.github.com「Workflow dependency lockfiles」）。そのため YAML の `uses:` は読みやすいタグ（`v7.0.1` など）のまま書く。
- **CI では拡張を入れない**（`--verify` のワークフローを作らない）。実行を止めるのは、ジョブの開始時のランナー側の確認で、CI の `--verify` は、それより先に分かりやすく落とすだけの補助になるため。
- ロックファイルに載っていないワークフローの実行は、リポジトリのポリシー「Require lockfile」（Settings → Actions → Policies）で止める。個人のアカウントの公開リポジトリで使えるかは、ドキュメントに書かれていないので、設定画面で確かめる。使えず、ロックされたコミットで動くことも確かめられなければ、YAML の `uses:` もコミットの SHA で固定する（`actions/checkout@<40 文字の SHA> # v7.0.1`）。
- **ロックファイルの差分は必ず読む**。`v4` のような動くタグが古いコミットに戻されても検出されない（gh-actions-lock の Limitations）。`--accept-moved` は使わない。
- 更新は Dependabot（`github-actions` だけ）に PR を作らせる。`cooldown`（`default-days: 7`）で、出てから 7 日たった版だけを提案させる。Dependabot は、ロックファイルに載ったワークフローのエントリも更新する（ドキュメントによる）。取り込む前にロックファイルの差分を読む。更新されていなければ、その PR のブランチで、ホストで `gh actions-lock --relock` を実行する。

### GitHub 側の設定（利用者が行う）

- Codecov にリポジトリを登録し、`CODECOV_TOKEN` を Secrets に入れる。
- main ブランチの保護で、ci.yml と vulncheck.yml のジョブの成功をマージの条件にする。
- Settings → Actions → Policies で「Require lockfile」のポリシーを作り、Active にする（使えるかを確かめる）。

## 19. キーチェーン（issue #25）

- 状態: 承認済み（2026-11-24）・実装済み

### 目的

シークレットを 1Password（`op run`、7 章）からしか注入できない。1Password を使えない端末でも、macOS のキーチェーンから同じように注入できるようにする。使い方の流れ（`.sigil` への列挙、承認の画面、コンテナへ `-e NAME` で渡す）は 1Password と同じにし、違うのは `.env` / `secrets.env` に書く参照だけにする。

対象は、GUI でログインしている Mac で sigil を使う場合。SSH で入ったときなど、解錠のダイアログを出せない場合は、エラーで止まればよい（止まったままにならないこと）。

### やらないこと

- Bitwarden などほかの提供元。`bw` には `op run` に当たる仕組みがなく、セッショントークンの扱いが要る。Secrets Manager の `bws run` は別の製品。必要になったら別の issue にする。
- 全体設定の 1 行で、端末ごとに提供元を切り替える仕組み（下の「考えた方法」の B）。
- ログインキーチェーンなど、専用のキーチェーン以外を読むこと。
- キーチェーンや項目を sigil が作る・書き換える・ロックすること（登録は利用者が `security` で行い、README で案内する。ロックは利用者が設定した自動ロックに任せる）。
- プロジェクトごとに読める項目を制限する仕組み（下の「受け入れるリスク」）。
- キーチェーンのパスを環境変数で上書きする仕組み（作らないので、`config.deniedEnvs` に足すものもない）。

### やってはいけないこと

- リポジトリが書ける `.env` によって、専用のキーチェーン以外の項目（ログインキーチェーンの gh のトークンなど）が読まれる。
- 値やキーチェーンのパスワードが、コマンドライン引数・ログ・エラーメッセージに出る。
- `.env` や `.sigil` によって、sigil が実行する `security` 以外のコマンドや、`security` の別の動作（項目の書き換え・削除・書き出し）を起こせる。
- キーチェーンから読むはずだった名前に、別の値（ホストのシェルの同じ名前の値、`keychain://...` という文字列）が入ってコンテナに渡る。
- `.sigil` がなくても、キーチェーンの参照だけで承認を飛ばせる。

### 考えた方法

| | 方法 | 採否 |
|---|---|---|
| A | 参照のスキームで提供元を分ける（`op://...` / `keychain://...`）。全体設定には、読む専用キーチェーンだけを書く | **採用** |
| B | 参照を提供元によらない形（`secret://openai`）にし、全体設定で端末の提供元を選ぶ | 見送り。1Password の vault / item / field とキーチェーンの項目名を対応づける仕組みが要り、大きくなる。`.env` と `secrets.env` はどちらも端末ごとのファイルなので、A でも端末ごとの使い分けはできる |
| C | 値を取り出すコマンドを設定に書ける汎用の提供元 | 見送り。プロジェクトの `.sigil` から書けると、ホストで任意のコマンドを実行される。全体設定だけに限っても、今回の目的には大きすぎる |

### 1Password との違い（前提の変化）

1Password では `op run` が値を取りに行き、起動し直した sigil は環境変数として受け取るだけだった。キーチェーンには `op run` に当たる仕組みがなく、cgo を使わないので Security.framework も呼べない。そこで **sigil が `/usr/bin/security` を実行し、その標準出力から値を受け取る**。値がメモリに載ること自体は 1Password でも同じ（起動し直した sigil の環境変数にある）。変わるのは、sigil が自分で外部コマンドを実行して値を読みに行く点で、この章の制限で守る。

### 書き方

`.env`（プロジェクト）と `secrets.env`（全体）:

```sh
OPENAI_API_KEY=op://Dev/OpenAI/credential
WAKATIME_API_KEY=keychain://wakatime
```

- 形は `keychain://<service>`。account は書かない（専用のキーチェーンなので、service だけで足りる。同じ service の項目を複数作らないよう README で案内する）。
- `<service>` は `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`。`-` で始まるもの（`security` のオプションとして読まれる）、`/`、空白、引用符などは、行番号付きのエラーで終了する。
- 行末のコメント（`KEY=keychain://x  # ...`）は扱わない（今の `.env` の読み方と同じ。コメントは行頭の `#` だけ）。書けば service の検査でエラーになる。
- 1 つのファイルに `op://` と `keychain://` を混ぜて書ける。
- どちらでもない値の行は、今までどおりアプリの設定として sigil は扱わない（7 章）。

全体設定（`~/.config/sigil/config`）:

```
keychain ~/Library/Keychains/<名前>.keychain-db
```

- **全体設定でだけ書ける**。プロジェクトの `.sigil` に書けば、行番号付きのエラーで終了する（リポジトリに探す先を変えられないように）。
- 2 回以上書けばエラー。
- 値は絶対パスか、`~/` で始まるパス。`~/` は、sigil がプロジェクトの特定に使うのと同じホーム（`UserHomeDir`）に展開する。`~` だけ、`~user/`、相対パスはエラー。2 語の書式なので、空白を含むパスは書けない（README に書く）。
- ファイルの存在と置き場所は、設定を読むときではなく、キーチェーンの参照を読む直前に確かめる（キーチェーンを使わないプロジェクトで止めないため）。

### キーチェーンのパスの確かめ方

`security` を呼ぶ直前に、次をすべて確かめる。どれかに当てはまらなければ、エラーで終了してコンテナを作らない。

- `filepath.EvalSymlinks` でリンクを解いたパスが、`<ホーム（リンクを解いたもの）>/Library/Keychains/` の**直下**にある。専用のキーチェーンをプロジェクトの中や dotfiles の下など、コンテナにマウントされうる場所に置かれると、コンテナがファイルを持ち出してオフラインで総当たりしたり、差し替えて解錠のパスワードを入力させたりできるため。
- ログインキーチェーンを指していない。ファイル名を小文字にして `login` で始まるもの（`Login.keychain-db` や、パスワードを変えたあとに残る `login_renamed_1.keychain-db` を含む）を拒否する（APFS は大文字小文字を区別しないため）。あわせて、`~/Library/Keychains/login.keychain-db` / `login.keychain` と同じファイル（`os.SameFile`。ハードリンクなど）も拒否する。ログインキーチェーンのファイルがないときは、比べる相手がないだけなので拒否の理由にしない。
- ファイルが存在する。`security` に存在しないパスを渡したときに検索リスト（ログインキーチェーン）へ落ちるかは、実機の検証（下の手順 5）で決着がつかなかったので、sigil は `security` を呼ぶ前に必ず存在を確かめる。
- 通常のファイルで、持ち主が sigil を実行しているユーザーである。
- `security` には、**リンクを解いたパス**を渡す（確かめたものと使うものを同じにする）。
- `security` は、`/` を含まない名前を `~/Library/Keychains/` からの相対名として解決するが、sigil は必ず絶対パスを渡すので、その解決は起きない。

### 名前と参照の組の選び方

7 章の選び方を、キーチェーンの参照にも同じように使う。

- プロジェクトの `.env`: 値が `op://` か `keychain://` で、設定で列挙された名前で、拒否する名前ではない行だけを選ぶ。それ以外は注意を出して使わない。
- 全体の `secrets.env`: 拒否する名前があればエラー（今までどおり）。キーチェーンの参照は、設定で列挙された名前のものだけを読む（使わない項目のために解錠を求めないため）。

### 同じ名前が複数にあるとき

**全体の `secrets.env` を優先する**（7 章の op run の順序と同じ。プロジェクトの `.env` で全体のシークレットを上書きされないように）。全体の `secrets.env` の行は、`op://`・`keychain://`・生の値のどれでも、プロジェクトの `.env` の参照に勝つ。名前ごとに勝つものを先に決め、その提供元から読む。

| 全体 `secrets.env` | プロジェクト `.env` | 使うもの |
|---|---|---|
| `op://` | `keychain://` | 全体の 1Password。キーチェーンは読まない |
| `keychain://` | `op://` | 全体のキーチェーン |
| 生の値 | `keychain://` | 全体の生の値。キーチェーンは読まない |
| なし | `keychain://` | プロジェクトのキーチェーン |

- プロジェクトの分の op run の env ファイル（7 章）には、勝ったものが `op://` の組だけを入れる。
- 全体の `secrets.env` は今までどおりそのまま op run に渡す。`keychain://` の行は op run が文字列のまま環境変数にするが、そのあとでキーチェーンの値で置き換えるか、置き換えられなければ渡さない（下の「渡すかどうか」）。
- 全体の `secrets.env` の値（生の値・`op://`）が環境に入るのは、op run で起動し直し、そのとき `secrets.env` を op run に渡した（`op://` を含む）ときだけ。`op` がない端末や、`secrets.env` に `op://` がないときは、全体の値は読み込まれない。そのとき、全体に負けたプロジェクトの名前は渡さない（下の「渡すかどうか」）。

### 読み出し

```
/usr/bin/security find-generic-password -s <service> -w <リンクを解いた専用キーチェーンのパス>
```

- **絶対パスで実行する**（`LookPath` を使わない。PATH の乗っ取りを避けるため）。
- 引数は別々の argv として渡し、シェルを通さない。
- 項目ごとに 1 回実行する。
- 子プロセスの扱い:
  - 環境は最小にする。少なくとも sigil 自身の環境（inner では 1Password のシークレットが入っている）は引き継がない。実機で確かめたところ、`security` は空の環境でも動いたので、環境は空にする。
  - 作業フォルダは `/`、標準入力は `/dev/null`。
  - `SysProcAttr{Setsid: true}` で制御端末から切り離す（`security` が `/dev/tty` に直接パスワードを求めたり、演出とぶつかったりしないように）。
  - タイムアウトは 120 秒。過ぎたら止めて、エラーで終了する（解錠のダイアログに誰も応えないときに、止まったままにならないように）。
- 標準出力は 64KiB まで読む。超えたら、切り詰めずにエラーにする。末尾の改行を 1 つだけ取り除いたものを値とする。値に NUL があればエラー（環境変数に入れられないため）。
- 標準エラーはそのまま表示しない。制御文字・書式文字を取り除き、改行を空白にまとめて 1 行にし、短く切ってから表示する。終了コードは出してよい。標準出力（値）は、エラーにも `%v` にも含めない。
- 項目がない、キーチェーンのパスの確かめでだめだった、`security` が失敗した・時間切れのときは、エラーで終了してコンテナを作らない（`op run` も参照を解決できなければ止まるので、それにそろえる）。
- 全体設定に `keychain` がないのに、読むべきキーチェーンの参照があるときは、注意を出して続行する。その名前は**渡さない**（下の「渡すかどうか」）。

### 渡すかどうか

今は、設定で列挙された名前を、sigil 自身の環境にあるかどうか（`LookupEnv`）で絞って `-e NAME` にしている（8 章）。キーチェーンの値は sigil 自身の環境には入れないので、次のように判断する。

- 勝ったものが `keychain://` の名前は、**読めた値があるときだけ**渡す。読めなかった（`keychain` の設定がない）ときは、ホストのシェルに同じ名前があっても、inner の環境に `keychain://...` の文字列があっても渡さない。利用者がキーチェーンに置いた権限の狭いトークンの代わりに、シェルの権限の広いトークンが渡ることがないように。
- プロジェクトの `.env` の参照が全体の `secrets.env` の生の値・`op://` に負けた名前は、**全体の値を読み込めたとき**（op run から起動し直した側で、`secrets.env` を op run に渡したとき）だけ `LookupEnv` で判断する。読み込めなかったときは、「注意: X は全体の secrets.env が優先されますが、その値を読み込めなかったので渡しません」と出して渡さない。`LookupEnv` を見ると、全体の値ではなくホストのシェルの値が渡ってしまうため（全体の勝者が `keychain://` のときは、上のキーチェーンの扱いになる）。
- それ以外の名前は、今までどおり `LookupEnv` で判断する。
- WakaTime の判定（`WAKATIME_API_KEY` を渡すか）も、同じ判断の結果を使う。
- 読んだ値は、`docker run` を実行するコマンドの環境に、sigil 自身の環境（`Environ()`）の**後ろに** `NAME=値` として足す（Go の `exec` は、同じ名前が複数あると後のものを使う）。`docker run` の引数は今までどおり `-e NAME` だけ。sigil の環境（`os.Setenv`）は変えない。

### 順序

```
設定を読む → 承認（6 章） → [op:// があれば op run で起動し直す（7 章）] → キーチェーンから読む → docker run
```

- 承認の画面のあとで読む。解錠を求める前に、何を渡すかを見せるため（7 章と同じ考え方）。承認で `n` なら `security` を呼ばない。
- op run で起動し直したときは、起動し直した側（inner）で読む。
- 読むのは、承認のハッシュを計算したのと同じ読み込みの内容から選んだ組。読み直さない。`SIGIL_INNER` を外から設定され、op run を経ていないときも同じ。
- 既存のコンテナに入るときは、読まない（9 章。作成時の値が使われる）。

### ロックされているとき

専用のキーチェーンは、自動ロック（`security set-keychain-settings -l -u -t <秒>`）を設定して使う前提にする（README で案内する）。sigil は読み終えたあとにロックし直さない（利用者の判断。自動ロックの時間内に別のプロジェクトのコンテナを作るときは、パスワードを聞かれない）。

実機で確かめたところ、ロックされているときの `security find-generic-password` は macOS の解錠ダイアログを出し、パスワードを入れると値を返した。

- ダイアログにそのまま任せる（タイムアウトは上のとおり）。ダイアログを待つあいだは演出を描かない（17 章の刻印と同じ考え方）。
- エラーになったとき（SSH など、ダイアログを出せないときを含む）は、「`security unlock-keychain <パス>` を実行してください」と知らせて終了する。sigil がパスワードを受け取ったり、`-p` で渡したりはしない。

### 承認の対象と画面

- **承認の対象があるか**の判定に、キーチェーンの組を含める。今の `approve()` は「`.sigil` も 1Password の組もない」ときに確認を飛ばすので、`.sigil` がなく、`.env` に全体設定で列挙された名前のキーチェーンの参照しかないリポジトリでも、必ず確認を出す。
- 承認のハッシュ（6 章）は、**優先順位を決める前の**プロジェクトの組（1Password とキーチェーンの両方）から作る。`.env` に出た順に、`NAME="op://..."` / `NAME="keychain://<service>"` の行にする。op run に渡す env ファイル（勝ったものだけ）とは別に作る。
  - こうすると、1Password の組だけのときは今の `RenderEnv` と同じバイト列になり、既存の承認記録はそのまま使える。全体の `secrets.env` を編集しただけで、プロジェクトの承認が変わることもない。
- 全体設定の `keychain` のパスはハッシュに入れない（ホストのファイル。6 章の全体設定と同じ扱い）。
- 確認の画面では、組ごとに提供元を分けて表示する（例: `1Password  op://Dev/OpenAI/credential` / `キーチェーン  keychain://wakatime`）。全体の `secrets.env` に負けて使われない組は、そのことが分かるように添える（例: 「全体の設定が優先されるため使わない」）。文言と見た目は 16 章の決まりに従い、builder が決める。
- 作成時の段階「禁書庫から召喚する」の Note は、使った提供元にする（`1Password` / `キーチェーン` / `1Password・キーチェーン`）。

### 受け入れるリスク

- **専用キーチェーンの項目は、承認さえ通れば、どのプロジェクトの `.env` からも読める**。1Password の vault と同じ性質で、守るのは承認の画面（参照先の service 名を必ず表示する）。プロジェクトごとに読める service を全体設定で許可する仕組みは、必要になったら別の issue にする。
- **解錠中は、ホストで動くほかのプロセスも確認なしで読める**（`-T` を付けずに作った項目は `/usr/bin/security` が信頼済みになるため）。コンテナの中のコードからは届かないので、sigil の守る範囲（1 章）の外。項目のアクセス制御（`-T ""` で毎回確認させる）を README の既定にするかは、実機の結果で決める。
- **値は `docker inspect` で見える形でコンテナの設定に残る**。1Password と同じ。キーチェーンで値を替えても既存のコンテナには反映されないので、README に「値を替えたら `sigil stop` して作り直す」と書く。

### 構成

- `internal/opwrap` を、スキームで参照を分ける形に広げる（例: `internal/secret`。名前は builder が決める）。参照の検証はスキームごとに行う。
- `security` の実行は `app.Deps` の関数フィールドにし、テストでは偽物を使う（実物のキーチェーンに触れない）。キーチェーンのパスの確かめ（リンクの解決、持ち主）も差し替えられるようにする。
- 段階を分ける: 1) スキームで分けるだけのリファクタ（挙動は変えない） 2) キーチェーン対応。

### テスト（13 章に足す）

- 参照の解析: `keychain://` の正常系、`-` で始まる・`/` や空白や制御文字を含む service、行末のコメントを拒否する
- 全体設定の `keychain`: `.sigil` に書けばエラー、2 回書けばエラー、相対パス・`~`・`~user/` はエラー、`~/` の展開
- キーチェーンのパス: `~/Library/Keychains/` の直下でない（リンクを解いた先で判断）、`login.keychain-db`、通常のファイルでない、持ち主が違う → `security` を呼ばない。`security` に渡すのはリンクを解いたパス
- `security` の argv が `find-generic-password -s <service> -w <パス>` で、実行するのが `/usr/bin/security` である。子プロセスの環境に sigil の環境（1Password のシークレットを模した値）が入らない
- 出力が 64KiB を超える・NUL を含む → エラー。時間切れ → エラー
- 読んだ値が `docker run` の引数に現れず、docker のコマンドの環境にだけある。ホストに同じ名前があってもキーチェーンの値が使われる
- `keychain` の設定がない → 注意を出し、その名前は、ホストに同じ名前があっても、inner の環境に `keychain://...` があっても渡さない
- WakaTime の判定がキーチェーンの値で正しく働く
- 同じ名前の優先順位（上の表の 4 通り）
- 全体の生の値・`op://` に負けた名前は、全体の値を読み込めないとき（`op` がない、`secrets.env` に `op://` がない）、ホストに同じ名前があっても渡さず、注意を出す。`secrets.env` を op run に渡した inner では渡す
- 承認: `.sigil` なし・キーチェーンの組だけで確認が出る。`n` → `security` も docker も呼ばない。キーチェーンの組の追加・差し替えで聞き直す。1Password の組だけのときのハッシュが今の `RenderEnv` と同じバイト列。全体の `secrets.env` だけを変えてもハッシュは変わらない
- op run から起動し直した側でキーチェーンを読み、起動し直す前の側では読まない
- 項目がない・`security` が失敗 → docker を呼ばない。エラーに値も標準エラーの制御文字も出ない

### 実機での検証（14 章に足す）

テスト用のキーチェーンを作って使う。利用者のキーチェーン（ログインキーチェーンや、利用者が作った専用キーチェーン）は開かない・読まない。テスト用のキーチェーンの作成・項目の追加・ロック・削除は、Claude が `security` で行ってよい（利用者の判断）。解錠のダイアログや承認の画面は、利用者に cmux のペインで見てもらう。

1. `security list-keychains` の結果を控える。
2. `~/Library/Keychains/sigil-dev-test-<ランダム>.keychain-db` に、テスト用のパスワードでキーチェーンを作り、テスト用の値の項目を足す（テスト用の値なので、引数に載せてよい）。置き場所の制限があるので、一時フォルダではなくここに作る。
3. テスト用の全体設定（`XDG_CONFIG_HOME` を一時フォルダに向ける）に `keychain <テスト用キーチェーン>` を書き、`SIGIL_IMAGE=sigil:dev-test` のテスト用プロジェクトの `.env` に `keychain://` の参照を書いて、コンテナの中で値が見えることを確かめる。
4. キーチェーンをロックしてから作り直し、ロックされているときの挙動を確かめて、上の「ロックされているとき」を決める。
5. 存在しないパス（`~/Library/Keychains/` の直下の、ないファイル）を指したとき、sigil がその前に止まることを確かめる。あわせて、`security` に存在しないパスを直接渡したときに、検索リスト（ログインキーチェーン）に落ちないかも確かめて記録する（テスト用の項目名で調べ、利用者の項目は読まない）。
   - 結果: sigil は `security` を呼ぶ前に止まった。`security` に存在しないパスを直接渡すと、項目がないときと同じ終了コード 44 になり、検索リストに落ちたのかどうかは見分けられなかった（決着がつかなかった）。そのため、sigil が `security` の前に存在を確かめる（上の「キーチェーンのパスの確かめ方」）。
6. 項目のアクセス制御（`add-generic-password -T ""`）で毎回確認が出るかを確かめ、README の案内に反映する。
7. `security -w` が、表示できない文字を含む値をどう出すか（16 進になるか）を確かめ、README に書く。
8. 後片付け: テスト用のキーチェーンを `security delete-keychain` で消し、`security list-keychains` が 1. と同じに戻ったことを確かめる。

### README

- 専用のキーチェーンの作り方（`security create-keychain ~/Library/Keychains/<名前>.keychain-db`。パスワードは引数に書かず、入力を求めさせる）。`~/Library/Keychains/` の直下に置く必要があること。検索リストに加わるかは、`security list-keychains` で確かめるよう案内する（Apple の man ページでは `create-keychain` は検索リストに加えるとあるが、検証した 1 台の Mac では変わらなかった）
- 同じ名前を 1 つの `.env` に 2 回書かない（`op://` と `keychain://` を両方書くと、どちらも承認の対象になるが、実行時は順序によらずキーチェーンの値が使われる。`keychain://` を 2 回書くと両方読み、後のものが使われる）
- 自動ロックの設定（`security set-keychain-settings -l -u -t <秒> <パス>`）
- 項目の足し方（`security -i` の対話モードの中で `add-generic-password -s <service> -a sigil -w <値> <絶対パス>` と打つ。シェルで `-w 値` と打つと `ps` と履歴に残り、`-w` のあとにパスを続けるとパスが値として読まれて既定のキーチェーンに入るため）。同じ service の項目を複数作らない。値は印字できる文字の 1 行にする。対話モードで打った値は画面に表示され、スクロールバックやペインのログに残ること、空白や引用符を含む値は引用符で囲むこと。読むたびに確認させたいときは `-T ""` を付ける
- 全体設定の `keychain` と、`.env` の `keychain://` の書き方（行末にコメントを書かない、パスに空白を使えない）
- 「常に許可」を選ぶと、同じユーザーのほかのプロセスも `security` 経由で読めるようになること
- 値を替えたら `sigil stop` して作り直すこと

## 20. 読み取り専用のマウント（issue #2）

### 目的

完全に別のリポジトリ（ライブラリのソースなど）を、調査や参照のためにコンテナから読めるようにする。
親フォルダ（`~/src` など）で実行して両方を見ることはできない（5 章。中に別のリポジトリがある、git ではないフォルダは拒否する）。

### やらないこと

- 書き込めるマウント。必要になったら `rw` という別の指定として足す（`ro` の意味は変えない）。
- コンテナ内のパスの指定（`ro <host>:<container>`）。`/workspace/<名前>` に固定する。
- 全体設定での `ro`。全体設定に書くと、すべてのプロジェクト（信頼できないものを含む）のコードからそのフォルダが読めるため。
- マウントしたフォルダの中の `.env` などを隠すこと（「受け入れるリスク」を参照）。

### 書き方

プロジェクトの `.sigil` に、1 行に 1 つ書く。

```
ro ~/src/some-library      # /workspace/some-library に読み取り専用でマウント
ro /opt/src/other-repo     # /workspace/other-repo
```

マウントしてよい範囲は、全体設定（ホストのファイル）に `ro_root` で書く。`ro` は、どれかの `ro_root` の中（`ro_root` そのものを含む）でなければエラーになる。`ro_root` がひとつもなければ、`ro` はすべてエラー（「全体設定に ro_root を書いてください」と案内する）。

```
# ~/.config/sigil/config
ro_root ~/src
ro_root /opt/src
```

- `.sigil` はリポジトリの中にあり、信頼できない。拒否する場所の一覧（`CheckSafe`）だけでは、`~/.kube`、`~/.claude`、`~/.local`（sigil 本体と承認記録）、`~/Documents` にある別のプロジェクトの `.env` などを防ぎきれない。マウントしてよい範囲は、1Password と同じく、ホストの信頼できる側で決める。
- `ro_root` は全体設定でだけ書ける。`.sigil` に書けばエラー。
- `ro_root` も `~/` か `/` で始め、シンボリックリンクを解決し、`CheckSafe` を通す（`ro_root ~` などは拒否する）。存在しなければエラー。
- パスに空白は書けない（1 行は 2 語。6 章）。

### パスの確かめ方

コンテナを**新しく作るたび**に、承認より前に、次の順で確かめる。どれかに当てはまれば、docker を呼ばずにエラーで終了する（エラーは `<.sigil のフルパス>:<行番号>: <内容>`）。

1. `~/` か `/` で始まる（`config.ExpandHome`）。相対パス、`~`、`~user/` は拒否。
2. `..` の要素を含まない（展開したあと、`/` で区切った要素で判定する）。
3. 表示されない文字（`unicode.IsPrint` が偽のもの）を含まない。展開したパスと、解決したパスの両方で確かめる（承認の画面を偽装させないため。7 章の参照と同じ考え方）。
4. シンボリックリンクを解決する（`filepath.EvalSymlinks`）。存在しなければエラー。
5. 解決した先がフォルダである。
6. 解決したパスが、解決したどれかの `ro_root` の中にある。比較はファイルの同一性で行う（`CheckSafe` と同じ `within`）。
7. 解決したパスが、プロジェクトと同じ判定（`project.CheckSafe`。全体設定と状態のフォルダも守る）を通る。`ro_root` の中でも、秘密情報を含む場所は拒否する。
8. 解決したパスに `:` を含まない（`-v` の区切りと衝突するため）。
9. 解決したパスが、プロジェクトそのものか、その中ではない（すでにマウントされているので意味がない。コンテナが書き換えられる場所を ro の元にしないため）。
10. マウント先の `<名前>` が空・`.`・`..` ではなく、`/workspace/<名前>` が、プロジェクトのコンテナ内パスや、ほかの `ro` のマウント先と一致しない（どちらも `SafeName` の置き換えをしたあとの文字列で比べる）。

マウント先の `<名前>` は、**書かれたパス**（展開・`filepath.Clean` したもの）の最後の要素にする。シンボリックリンクの先の名前ではなく、利用者が書いた名前で見分けられるようにするため。`:` か `,` を含む名前は、プロジェクトと同じく `SafeName` にする。

docker には、書かれたパスではなく**解決したパス**を渡す。途中のシンボリックリンクを docker にたどらせないため。

**`docker run` の直前に、もう一度確かめる。** 承認から `docker run` までには、op run での起動し直しや、キーチェーンの解錠のダイアログが入り、長く空くことがある。その間に、解決したパスの途中のフォルダを差し替えられないよう（たとえば、別のプロジェクトのコンテナが書き込めるフォルダの中を `ro` に書いた場合）、直前に同じ手順で解決し直し、承認したもの（解決したパスとマウント先）と一致しなければエラーにする。

### 承認

- 対象に、`.sigil` の内容に加えて、`ro` ごとの「解決したホスト側のパスとマウント先」を含める。`.sigil` の文字列だけを対象にすると、承認のあとにシンボリックリンクの向きを変えるだけで、別のフォルダがマウントされるため。
- ハッシュ: `ro` があるときだけ、6 章の内容の後ろに `"\0ro\0"` と、`ro` ごとに `<解決したパス>\0<マウント先>\0` を足す。`ro` がなければ今までと同じバイト列なので、既存の承認記録はそのまま使える。
- 区切りの NUL が紛れないよう、`.sigil` に NUL があればエラーにする（パスと名前の NUL は、手順 3 の表示されない文字として拒否される。`.env` から作る部分は `NAME="op://..."` の行だけで NUL を含まない）。
- op run から起動し直された側（`SIGIL_INNER`）でも、確かめ直して照合する（6 章と同じ）。
- 画面: 区分「写本（読み取り専用のフォルダ）」に、`<解決したパス> → <マウント先>` を並べる。区分の下に「中の .env なども、コンテナから読めます」と添える。Plain の文面は golden テストで固定する。

### docker

```
-v <解決したパス>:/workspace/<名前>:ro
```

`.git` の `-v` のあと、Neovim の設定の前後のどちらでもよい。`.sigil` に書かれた順に付ける。

マウントしたリポジトリでは、`safe.directory` を足さなくても、コンテナ内の git（`git log` / `git status`）が動く（実機で確かめた。イメージの利用者の UID をホストに合わせているため、所有者が同じに見える）。

### 既存のコンテナ

作成時のマウントがそのまま使われる（9 章）。`ro` を変えたら `sigil stop` してから作り直す。layout の版は変えない（`ro` がないコンテナの配置は変わらないため）。

### 受け入れるリスク

- **マウントしたフォルダの中の秘密**: `.env`、`.env.local`、`apps/web/.env` などは、コンテナから読める。空のファイルを重ねる方法は、置き場所や名前のすべてを覆えず、守れている気にさせるので採らない。承認の画面で注意を出し、README にも書く。
- **直前の確かめから docker run までの差し替え**: 直前に確かめ直しても、docker が実際にマウントするまでの短い窓（ミリ秒）は残る。差し替えるには、その場所にホストで書き込める必要がある（例: 別のプロジェクトのコンテナ）。別の sigil のプロジェクトをすべて拒否すると、自分で sigil を使って開発しているライブラリを読むという主な用途ができなくなるので、拒否はしない（利用者の判断）。
- **承認のあとの中身の変化**: 承認するのはパスで、中身ではない。`ro` の先に後から `.env` や秘密のファイルが置かれても、聞き直さない。
- **`ro_root` の範囲**: `ro_root` の中は、どのプロジェクトの `.sigil` からでも（承認すれば）マウントできる。`ro_root` には、他人に見せてよいリポジトリの置き場だけを書く（README に書く）。
- **`.git`**: 読み取り専用なので、hooks や config は書き換えられない。コンテナ内の git が `ro` の先の `.git/config` の設定を実行しても、コンテナの中で動くだけ。sigil がホストの git を `ro` の先に対して呼ぶことはない。
- **マウント先の名前の正規化**: macOS は NFD と NFC を同じ名前として扱うが、マウント先はコンテナ（Linux）の中で、バイト列が違えば別のフォルダになる。衝突しても docker が別のフォルダとして作るだけで、ほかのマウントを覆わないので、正規化はしない（標準ライブラリに正規化がないこともある）。
- **画面の偽装**: パスと名前は、表示されない文字を拒否したうえで、エスケープして表示し、切り詰めずに折り返す（参照と同じ）。見た目の似た文字（ホモグリフ）は防がないが、表示するのは解決した実在のパスである。

### テスト（13 章に足す）

- 解析: `ro` の正常系、値がない・余分な値、全体設定に書くとエラー。`ro_root` の正常系、`.sigil` に書くとエラー。`.sigil` に NUL があるとエラー
- 拒否（docker を呼ばない）: `ro_root` がない、`ro_root` の外（`ro_root` の名前が前方一致するだけの別フォルダを含む）、`ro_root` の中を出ていくシンボリックリンク、相対パス、`~`、`..` を含む、表示されない文字、存在しない、フォルダではない、ホーム、`/`、`~/.ssh` の中、`~/.config` を含むフォルダ、大文字小文字だけ違うホーム、`~/.ssh` を指すシンボリックリンク、`:` を含む、プロジェクトの中、名前が `.` / `..`、マウント先の衝突（プロジェクト・ほかの `ro`）。`ro_root` そのものが危険な場所・存在しない
- 承認: 写本の区分が画面に出る（Plain の golden）、シンボリックリンクの向きを変えると聞き直す、`ro` がなければハッシュが今までと同じ、inner（`ask` でない）でも確かめ直して照合する
- 直前の確かめ: 承認のあと `docker run` の前にシンボリックリンクの向きが変わると、コンテナを作らない
- `docker run` の引数: 解決したパスに `:ro` が付く、書かれた順

### 実機での検証（14 章に足す）

一時フォルダに、テスト用プロジェクトと、マウントされる側のリポジトリ（`.env` に偽の値を置く）を作る。全体設定は `XDG_CONFIG_HOME` を一時フォルダに向け、そこに `ro_root <一時フォルダ>` を書く（利用者の全体設定は変えない）。

1. `.sigil` に `ro <マウントされる側>` を書き、`sigil check` を実行する（承認は `/dev/tty` が必要なので、ユーザーに cmux のペインで実行してもらう）。写本の区分が画面に出て、`sigil check` がすべて ✓。
2. `sigil -- ls /workspace/<名前>` で読める。`sigil -- touch /workspace/<名前>/x` が失敗する。
3. `sigil -- git -C /workspace/<名前> log -1` が動くか（`safe.directory` の要否を決める）。
4. `ro ~/.ssh` と、`~/.ssh` を指すシンボリックリンクがエラーになり、コンテナが作られない。

### README

`.sigil` の書き方に `ro` を足し、マウントしたフォルダの中の `.env` などはコンテナから読めることを書く。

## 決定事項

| 日付 | 決定 | 理由 |
|---|---|---|
| 2026-11-19 | Go 1.27.4 を使い、`.tool-versions` で固定する | 利用者の希望（最新版）。asdf で管理しているため |
| 2026-11-19 | シェル版のコンテナとの互換は持たない | 利用者は Go 版の導入後にコンテナを作り直す。layout ラベルによる検出だけ残す |
| 2026-11-19 | 仕様は superpowers の設計書（この文書）と実装計画で管理し、`docs/SPEC.md` は削除する | OpenSpec は npm パッケージで、ホストで npm のコードを動かさないというこのリポジトリの目的に反するため |
| 2026-11-19 | 承認記録は、前後の空白を無視して比べる（builder の判断） | シェル版の `$(cat …)` と同じ挙動にし、既存の承認を使えるようにするため |
| 2026-11-19 | `dev stop` で対象がなくても終了コード 0（builder の判断） | シェル版と同じ |
| 2026-11-19 | 設定エラーのファイル名はフルパスで出す（builder の判断） | プロジェクトと全体のどちらの設定か分かるようにするため |
| 2026-11-19 | 既存のコンテナに入るときは、設定・承認・op run を行わない（builder の判断） | シェル版と同じ。設定は作成時にだけ反映される（変更したら `dev stop`） |
| 2026-11-19 | ポート番号は 1〜65535 に限る（builder の判断） | 正規表現だけだと `0` や `70000` が通り、docker で分かりにくい失敗になるため |
| 2026-11-19 | イメージのツールは基本的にすべて最新版にする。ただし Node.js は最新の LTS | 利用者の方針。Node は npm パッケージとの互換性を優先 |
| 2026-11-19 | イメージのベースを Debian 13 (trixie) にし、最終ステージは公式 `php:cli-trixie`。拡張 zip / intl / bcmath は `docker-php-ext-install` で入れる | bookworm（glibc 2.36）では最新の tree-sitter CLI が動かない。apt の PHP は最新ではない（trixie は 8.4、公式イメージは 8.5） |
| 2026-11-19 | devbox-check の環境変数一覧から、公式 PHP イメージの ENV（`PHP_*` など）を名前を列挙して除外する | ホストから渡ったものだけを見せるため。パターン（`PHP_.*`）にすると、利用者が渡した `PHP_FOO` まで隠れてしまう |
| 2026-11-19 | `dev stop` の「ない」の判定は `docker rm -f` の終了コードではなく、inspect で行う（builder の判断） | `docker rm -f` は、版によっては存在しないコンテナでも成功するため |
| 2026-11-19 | `.env` に書けるのは、設定で列挙した名前で、かつ制御用ではない名前だけ。拒否する名前を `DOCKER_*` / `XDG_*` / `DEVBOX_*` / `GIT_*` / `OP_*` / `LD_*` / `DYLD_*` に広げる | 最終レビュー C1。`op run` が `.env` の行をすべて dev の環境にするので、`DOCKER_HOST` などで秘密を外部に送れた（シェル版にもあった穴） |
| 2026-11-19 | 大文字小文字やシンボリックリンクに左右されないよう、危険なパスはファイルの同一性で比べる | 最終レビュー I1。`cd /users/<name>` でホーム全体がマウントされた |
| 2026-11-19 | `~/.config` などの秘密情報を含む場所と、リポジトリを含む git ではないフォルダを拒否する | 最終レビュー I2。dotfiles の `~/.config` リポジトリで `~/.config/op` や全体設定がマウントされる。`~/src` で各リポジトリの `.git` が書き込める状態になる |
| 2026-11-19 | `.env` も承認の対象にし、確認の画面に 1Password の参照先を出す | 最終レビュー I3。無害に見える名前で、別の 1Password 項目の値を渡せた |
| 2026-11-19 | プロジェクトの `.devbox` / `.env` のシンボリックリンクと、特殊なファイル・大きなファイルを拒否する | 最終レビュー I4。リンク先の秘密がエラーに出る。`/dev/zero` で止まる |
| 2026-11-19 | コンテナの状態は `docker ps` で判定し、docker が使えないときはその旨を出す | 最終レビュー I5。Docker Desktop が止まっているときに、誤って「dev build を実行してください」「ありません」と出た |
| 2026-11-19 | inner でも承認記録を照合する | 最終レビュー M2。`DEVBOX_INNER` を外から設定されると、承認を回避できた |
| 2026-11-19 | コンテナの作成に失敗したら状態を見直し、別のペインが作っていればそれを使う | 最終レビュー M3。cmux でペインを同時に開くと、名前の衝突で片方が失敗した |
| 2026-11-19 | ホスト側のパスに `:` があればエラーにする | 最終レビュー M4。`-v` が `:` で区切るため、正しくマウントできない（macOS の Finder では `:` が `/` に見えるので、まれ） |
| 2026-11-19 | `.devbox` と `.env` の先頭の UTF-8 BOM を取り除く | 最終レビュー M6 |
| 2026-11-19 | プロジェクトの `.env` を op run にそのまま渡さず、確かめた「名前と参照の組」だけで作り直した一時ファイルを渡す。参照ではない行は扱わず、列挙されていない名前・拒否する名前の参照は注意を出して使わない。承認の対象も、生の `.env` ではなくこの組にする | コミット後のセキュリティレビュー（確認後の差し替え、dev と op run の解釈のずれ）。また、`.env` に列挙外の名前があると拒否する以前の方式は、Vite / Next.js などがアプリの設定を `.env` に置く普通の使い方で dev を起動できなくしていた（C1 の対処を置き換える） |
| 2026-11-19 | `.env` の参照に制御文字・引用符・バックスラッシュ・`$`・バッククォートがあれば拒否する | コミット後のセキュリティレビュー（端末の制御文字で確認の画面を偽装できた） |
| 2026-11-19 | 拒否する名前を、`DEVBOX_` で始まる名前すべてではなく、dev が読む名前（`DEVBOX`、`DEVBOX_INNER`、`DEVBOX_IMAGE`、`DEVBOX_CONTEXT`、`DEVBOX_PROJECT_DIR`、`DEVBOX_OP_TMP`）に絞る | 14 章の検証で使う `DEVBOX_TEST_VAR` まで拒否されていたため |
| 2026-11-21 | コマンド名を `dev` から `sigil` に改め、設定ファイル（`.devbox` → `.sigil`、`~/.config/devbox/devbox` → `~/.config/sigil/config`）、状態のフォルダ、コンテナ名・ラベル・ボリューム（`devbox-*` → `sigil-*`）、イメージ（`sigil:latest`）、環境変数（`DEVBOX_*` → `SIGIL_*`、イメージ内の目印 `SIGIL=1`）、点検のスクリプト（`sigil-check`）も、すべてそろえる。`dev` の互換は残さない | issue #4。`dev` は汎用的すぎて、役割が伝わらない。まだ使い始めたばかりで、守るべき既存の資源がほとんどないので、名前を全部そろえて分かりやすくする。全体設定のファイル名は、`~/.config/sigil/sigil` と同じ名前が続くのを避けて `config` にした |
| 2026-11-21 | `.sigil` がなく、改名前の `.devbox` だけがあるときは、名前を変えるよう知らせて止める | 黙って無視すると、渡すはずの環境変数やポートが渡らず、気づきにくいため |
| 2026-11-21 | 改名前の `.devbox` の検出は、通常のファイルのときだけにする | Jetify Devbox などは `.devbox/` というフォルダを作るため。名前だけで判定すると、そうしたプロジェクトで sigil が起動しなくなる |
| 2026-11-21 | `~/.config/sigil/` がなく、改名前の `~/.config/devbox/` があれば止めて知らせる。`DEVBOX_IMAGE` / `DEVBOX_CONTEXT` が設定されていて新しい名前がなければ注意を出す | 移し忘れると、全体設定の環境変数や指定したイメージが黙って使われなくなるため |
| 2026-11-21 | 承認のハッシュで、`.env` がないときの特別扱い（`.sigil` だけのハッシュ）をやめ、いつも同じ形式で計算する | 改名で承認記録の場所が変わり、シェル版の記録との互換を保つ意味がなくなったため |
| 2026-11-21 | `make install` は、`~/.local/bin/dev` が古い Go 版の dev だと確かめられたときだけ消す（実行はせず、埋め込まれた `devbox-check` の文字列で判定）。それ以外は消さずに知らせる | 別の `dev` というツールや、利用者のスクリプトへのリンクを消さないため |
| 2026-11-22 | 演出（16 章）を `internal/ui` に置き、標準エラーが端末のときだけ出す。`SIGIL_PLAIN` / `NO_COLOR` / `TERM=dumb` で抑える | issue #3。パイプやリダイレクト、色を嫌う環境で、今までどおりの出力を保つため |
| 2026-11-22 | `SIGIL_PLAIN` / `SIGIL_GLYPHS` は拒否する名前に入れるが、`NO_COLOR` と `TERM` は入れない | `NO_COLOR` と `TERM` は sigil 専用ではない一般的な名前で、コンテナに渡したい使い方がある（`sigil-check` も `NO_COLOR` を想定内として扱う）。`.env` から設定されても、色や演出が変わるだけで害がない |
| 2026-11-22 | 作成時の段階は実際の処理に合わせ、待ち時間を足さない。前半の段階は描き始めた時点で済んでいる | 演出のために起動を遅くしないため。見え方は実機で見てから見直す（利用者の判断） |
| 2026-11-22 | `sigil check` の整形は、ホストではなく `sigil-check` の側で行う | ホストで整形すると、信頼できないコンテナの出力をホストで解釈して描くことになるため |
| 2026-11-22 | エラーは 1 行目を世界観の言葉、2 行目を今までのメッセージ（普通の言葉での対処）にする。演出のモード（Plain など）にかかわらず同じ文言にする | issue #3。用語だけでは何をすればよいか分からないため、対処は必ず普通の言葉で残す。モードは見た目（色や動き）の切り替えで、文言は変えない |
| 2026-11-22 | `--help` の用語は〔〕で添えるだけにし、用語の対応表は載せない | 既存の説明に（）の補足があるので、用語と区別するため。エラー・注意・承認の画面で用語には必ず意味が添えてあり、対応表は冗長になるため（利用者の判断） |
| 2026-11-22 | 注意と案内では、用語に実際の意味を括弧で添えない。承認の画面だけは添える | 括弧で同じことを 2 回言うと冗長になるため（利用者の判断）。承認の画面はセキュリティ上の判断をしてもらう画面なので、読み違えがないよう意味を添え続ける |
| 2026-11-22 | 作成時の完了の明滅（3 回、約 0.5 秒）の分だけは、`docker run` のあとに待つ | コードレビューで「待ち時間を足さない」との食い違いを指摘された。明滅は issue で決めた演出なので、そのまま残す（利用者の判断） |
| 2026-11-22 | 描画中の端末の大きさの変更には追従しない | 描画中に端末を狭くすると、行が折り返して描き直しが崩れる。幅を変えなければ起きず、広げる分には崩れないので、今回は対応しない（利用者の判断） |
| 2026-11-22 | イメージに Python と uv / uvx を入れる。Python は apt ではなく uv で最新の安定版を入れ、apt の `python3-pip` は使わない | issue #10。Claude Code の MCP サーバー（多くは `uvx` で起動）が動かなかった。apt の Python（trixie は 3.13）は最新ではなく、「ツールは最新」の方針に合わない。システムの Python を root 所有の `/opt` に置き、パッケージは venv や `uvx` で入れる（利用者の判断で最新を選んだ）。`uvx` は wheel の有無では Python を選ばず、`requires-python` を満たす最初の Python（ふつうはイメージの最新版）を使う。出たばかりの版では、依存パッケージの wheel がまだなく、ソースからのビルドに失敗することがある。そのときは `uvx --python 3.14 <package>` のように版を指定する。版が上がると前の版で作った venv が動かなくなることも、最新を選ぶ代わりに受け入れる |
| 2026-11-22 | `wakatime-cli` をイメージの `/usr/local/bin` に入れる。`~/.wakatime/` はホストと共有せず、永続化もしない | issue #10。vim-wakatime は初回の起動で `wakatime-cli` を Python でダウンロードしようとして失敗した。PATH に `wakatime-cli` があれば自動更新をしないので、イメージに入れれば Neovim の設定を変えずに済む。`~/.wakatime/` を共有すると、コンテナから書き換えた実行ファイルがホストの WakaTime で動いてしまう（`.git` を読み取り専用にしているのと同じ理由）。オフラインのあいだの記録が `sigil stop` で失われることは、まれなので扱わない |
| 2026-11-22 | `~/.local` は永続化しない | `~/.local/bin` は PATH の先頭にある。全プロジェクト共通のボリュームにすると、あるプロジェクトのコードが置いたコマンドが、ほかのプロジェクトのコンテナで `git` などを乗っ取れる。また、Claude Code の公式インストーラーは本体を `~/.local/bin/claude` に置くので、永続化するとイメージを作り直しても古い Claude Code がボリューム側に残り続ける。`uvx` のキャッシュは `~/.cache` にあり永続化済みなので、MCP サーバーの起動には困らない（利用者の判断） |
| 2026-11-22 | Neovim の Python プロバイダ（pynvim）は入れない | 利用者のプラグインで Python を使うのは vim-wakatime だけで、それも `wakatime-cli` を入れれば不要になる。issue #10 の完了の条件のうち `:checkhealth provider` の項目は外す。必要になったら専用の venv に入れて `g:python3_host_prog` で指す |
| 2026-11-22 | LazyVim が使うツールのうち、lua5.1 / luarocks / libsqlite3-dev / bat / git-delta は apt で、lazygit は GitHub のリリースから入れる | issue #11。apt の lazygit（0.50.0）は古く、設定の書き方（`git.paging`）も今の版（`git.diffRenderers`）と違い、delta の明暗を端末に合わせる `--{{colorScheme}}` も使えない（利用者の判断で最新を選んだ）。delta はリリースがまれで、決めた時点では apt の版と最新が同じ（0.18.2）。luarocks を apt で root 所有の場所に入れるのは、lazy.nvim が hererocks で全プロジェクト共通のボリューム（`~/.local/share/nvim`）に Lua と luarocks をビルドしないようにするため。lazy.nvim の `rocks.hererocks` は既定の自動判定のままで、システムの luarocks が使われる。ただし、luarocks で入れたパッケージ（ネイティブの `.so` を含む）は今までどおり `~/.local/share/nvim/lazy-rocks` に置かれ、プラグイン本体と同じくプロジェクトをまたいで読み込まれる（issue #7 で扱う） |
| 2026-11-22 | delta と lazygit の設定は、`/etc/gitconfig` と `/etc/xdg/lazygit/config.yml`（root 所有）に置く | ホストの `~/.gitconfig` は渡さず、コンテナの `~/.config` は永続化しない。イメージに置けばビルドのたびに同じ内容になり、root 所有ならコンテナの中から書き換えられない。lazygit は XDG_CONFIG_DIRS（既定は `/etc/xdg`）も探すので、ENV を足さずに済む |
| 2026-11-22 | 画像の表示（snacks.image、`magick` / `chafa` / `ueberzugpp` など）、ゴミ箱、`xdg-open`、Ruby / Perl / Node のプロバイダは入れない | issue #11。コンテナの中の Neovim からは端末が kitty graphics protocol 対応だと分からず（`TERM` を固定し、端末を判定する環境変数も渡していない）、texlive や mmdc は重い。ゴミ箱は永続化せず、`trash-cli` はプロジェクトに `.Trash-<uid>` を作ることがある。ホストのブラウザは開けない。プロバイダは使うプラグインがほぼないので、設定で止める |
| 2026-11-23 | イメージに gh を入れ、GitHub のリリースから最新を取る。トークンはプロジェクトごとの fine-grained PAT を、そのプロジェクトの `.sigil` / `.env` で `GH_TOKEN` として渡す | issue #12。apt の版は古く、公式の apt リポジトリは鍵と取得先をイメージに足すことになる。lazygit と同じ取り方にそろえる。全体設定に書くと全プロジェクトに渡るので、プロジェクトの設定（承認の画面に出る）だけで渡す。GitHub MCP（リモート版）は、トークンを全プロジェクトで共有する `~/.claude`（`sigil-claude`）に書くことになるので勧めない |
| 2026-11-23 | `sigil-check` でトークンの種類（先頭の文字列）と期限（応答ヘッダー）を確かめ、権限そのものは確かめない。期限の問題は ✗ ではなく注意にする | issue #12。種類と期限は確実に分かる。権限は、fine-grained PAT ではトークンから読めず、書き込みの API を中身の誤りで呼んで 403 と 422 を見分ける方法は、GitHub が先に権限を調べる前提に頼り、崩れると本当に書き込む。点検の道具がすることとして危ないので、README の手順で一度、人の手で確かめる。期限が長いのは隔離が破れているわけではないので、失敗には数えない（利用者の判断） |
| 2026-11-23 | `~/.cache/gh` に、コンテナごとの tmpfs（`rw,noexec,nosuid,nodev`）を重ねる。`sigil.layout` の版は上げない | issue #12 のコードレビュー。gh は `gh run view --log` の Actions のログなどを `~/.cache/gh` に置き、そこは共有の `sigil-cache` の中なので、非公開のリポジトリのデータ（CI に出た秘密を含むことがある）がほかのプロジェクトのコードから読めた。issue #7 の「書き換えて、ほかで実行させる」とは逆の、「置かれたものを、ほかから読まれる」向きの問題で、#7 の方針（ふだんは共有のまま）では塞がらないので、gh を入れるこの変更で塞ぐ。gh の実行を包むスクリプトで `XDG_CACHE_HOME` を変える案より、docker の引数だけで済み、テストできる（利用者の判断）。gh が入っているのはこの変更より後に作ったイメージだけで、そのイメージで作るコンテナには必ず tmpfs が付くので、既存のコンテナを作り直させる必要はない |
| 2026-11-23 | `sigil-check` で、`~/.config/gh/hosts.yml` のトークン（コンテナの中での `gh auth login`）を ✗ にする。`gho_` / `ghu_` / `ghs_` には期限の案内を出さず、`GITHUB_TOKEN` が `GH_TOKEN` と同じ値なら数えない。期限の日付に UTC と添える | issue #12 のコードレビュー。ログインされると、すべてのリポジトリに届くトークンがあるのに「結界は完全です」になっていた。OAuth や GitHub App のトークンは期限のヘッダーがない・短いので、「期限を付けて作り直す」という案内は誤り。コンテナの時刻は UTC で、GitHub の画面と 1 日ずれて見えることがある |
| 2026-11-23 | `sigil commit` を足す。コンテナが書いた `.sigil-commit` を銘とし、ホストの git で確認のうえコミットし、push は別に確かめる | issue #13。`.git` を読み取り専用にしているので、コンテナの Claude Code はコミットできず、メッセージを手で写してコミットしていた。`.git` の一部を書けるようにする案は、サブモジュールや worktree の設定、git 自体の脆弱性などの穴が残るので採らない |
| 2026-11-23 | コミットするのは、既定で変更のあるものすべて。絞るときは、利用者がホストで `sigil commit -- <path>...` と打つ。コンテナが指定したファイルの一覧は使わない | `.github/workflows` などを紛れ込ませられないように。全部を見せて、利用者が判断する（利用者の判断） |
| 2026-11-23 | `git commit` ではなく、一時の index で tree を確定してから見せ、`commit-tree` と、元の値を条件にした `update-ref` でコミットする | 確認のあいだもコンテナは動いているので、確認のあとで `git add` すると、見せていない変更が紛れ込む。`commit-tree` はフックを実行せず、`commit.gpgSign` は見るので署名も効く。本物の index は、コミットするまで変えない |
| 2026-11-23 | フックはホストで実行せず、コンテナで実行する（pre-commit、prepare-commit-msg、commit-msg、pre-push）。フックが失敗したらコミット（push）しない | `core.hooksPath` が作業ツリーの中を指すと、コンテナが仕込んだフックがホストで動く。`.git/hooks` のフックも、中から作業ツリーのコードを呼ぶ形が多い。フックは品質の関所として必ずどこかで実行したい（利用者の判断）。コンテナで実行すれば、結果を偽られてもセキュリティ上は失うものがない |
| 2026-11-23 | コンテナでフックを実行するとき、一時の index と object のフォルダを `GIT_INDEX_FILE` / `GIT_OBJECT_DIRECTORY` / `GIT_ALTERNATE_OBJECT_DIRECTORIES` で指す | コンテナの `.git` は読み取り専用で、フックの中の `git add` が index や object を書けないため。lint-staged などが `.git` の ref に書こうとして失敗しないかは、実機で確かめる |
| 2026-11-23 | 作業ツリーの中から読み込まれる git の設定、進行中のマージなど、gitlink の追加・変更、LFS、プロジェクトの外にあるフックがあれば、`sigil commit` は拒否する | 設定を書き換えられると、`-c` では打ち消しきれない方法（`core.sshCommand`、`gpg.program` など）でホストでコードを実行できる。入れ子のリポジトリは、ホストの git が中の設定を読むきっかけになる。LFS は pre-push のフックで実体を送るので、フックを止めると届かない。ホストで `git lfs push` を呼ぶと作業ツリーの `.lfsconfig` を読むので、必要になるまで対応しない（利用者の判断） |
| 2026-11-23 | 銘は、制御文字を取り除き、双方向の書式文字を拒否してから見せ、見せた内容をそのままコミットする（`#` の行も消さない） | 端末を操作されたり、見た目とコミットされる文字の並びを変えられたりしないように。読み直すと差し替えられるので、メモリから渡す |
| 2026-11-23 | 判断の画面（`.sigil` の承認、`sigil commit` の確認）に、色・枠・記号を付ける。動きは付けず、答えを受け取ったあとの処理にだけ付ける。意味を色だけで伝えない | 使っていてテンションが上がる見た目にしたい（利用者の判断）。それまでの「承認の画面は色や動きを付けない」を改める。入力を待つ行を描き直すと打った文字とぶつかり、画面を少しずつ出すと待ち時間が増えるので、動きは付けない |
| 2026-11-23 | コンテナが書いた内容（差分、ファイル名、銘、フックの出力）は、制御文字を見える形に置き換えてから sigil が色を付ける。`git --color` の出力は使わない | コンテナの ESC が混ざると、偽の表示を作れるため |
| 2026-11-23 | 刻印（`commit-tree` / `update-ref`）と push は、演出を描かずに行う。演出は、コンテナでフックを実行するあいだと、済んだあとの明滅・結果の枠にだけ付ける | 署名の pinentry や 1Password、SSH のパスフレーズの確認が端末に出ることがあり、描画とぶつかるため。待ち時間を足さない方針（16 章）にも合う |
| 2026-11-23 | 作業ツリーの中を指す `core.hooksPath` / `include.path` を、手で `git commit` するときのために `sigil` が知らせる機能は、別の issue にする | 今回の範囲を絞るため（利用者の判断） |
| 2026-11-23 | 最初のコミット（HEAD なし）では、本物の index を写さず、空の index から tree を作る | 利用者が add 済みの別のファイルが、pathspec の外なのに紛れ込むため |
| 2026-11-23 | HEAD にそろえるには `git reset -q HEAD -- :/` を使う（パスなしの `git reset -q` は使わない） | パスなしだと本物の `ORIG_HEAD` と HEAD の reflog に書いてしまうため |
| 2026-11-23 | LFS は、`.gitattributes` を読むのではなく、コミットに入るパスの `filter` 属性（`check-attr`）で調べる。`.lfsconfig` の有無は従来どおり | ファイルを自分で読むと、確かめてから読むまでにリンクへ差し替えられたり、`.gitignore` 済みの `.gitattributes`（git は使う）を取りこぼしたりするため。実際に適用される属性を git に聞くのが確実 |
| 2026-11-23 | `include.path` / `includeIf.*.path` は、値を解決し、作業ツリーの中を指せば（ファイルがなくても）拒否する | 読み込み元の一覧には、まだないファイルが出ないため。のちにコンテナが作ると読み込まれる |
| 2026-11-23 | `.git` が `<Root>/.git` の本物のフォルダでなければ拒否する（リンクされた worktree、`--separate-git-dir`、gitfile は対応しない） | gitfile が作業ツリーの中を指すと、「`.git` の中の設定は安全」という例外が、コンテナの書ける場所に効いてしまうため |
| 2026-11-23 | 設定の読み込み元は、見かけのパスとリンクを解いたパスのどちらかが作業ツリーの中なら拒否する | リンクを使った回避を防ぐため |
| 2026-11-23 | フックの場所は、設定ファイルの `core.hooksPath` から自分で求める（既定は `.git/hooks`、空は作業ツリーの最上位） | `rev-parse --git-path hooks` は、sigil が常に付ける `-c core.hooksPath=/dev/null` に上書きされるため |
| 2026-11-23 | 判断の画面（確認の画面、push 先、承認の画面）は、内容を `…` で切り詰めず、端末の幅で折り返す | 右端で切ると、銘の行末や、パスの `.env` のような末尾、改名の新しい側、リンク先、push 先のホスト、1Password の参照の項目名など、肝心な部分が見えなくなりうる。レビューで指摘された |
| 2026-11-23 | `sigil commit -- <path>` のパスは、今いるフォルダからのパスとして読み、プロジェクトの外を指せばエラーにする | git と同じ読み方でないと、サブフォルダで打ったパスが最上位から解釈され、意図しないファイルが対象になる（確認の画面には出るが、紛らわしい） |
| 2026-11-23 | `sigil commit -- <path>` で、`:` で始まる指定（pathspec の magic）を拒否する | `:(top)` や `:!` で、今いるフォルダからという決まりや、銘のファイルを外す指定をすり抜けられるため |
| 2026-11-23 | push の URL は、画面に出すときと pre-push のフックに渡すときに、URL の形なら userinfo を取り除く（scp の形の user は残す） | `remote get-url --push` は全体設定の `insteadOf` を反映するので、`https://user:TOKEN@host/...` のような認証情報が入りうる。そのままだとコンテナに渡り、docker exec の引数として `ps` にも見え、画面にも出る。セキュリティレビューで指摘された |
| 2026-11-23 | HEAD にサブモジュール（mode 160000）が 1 つでもあれば、変更がなくても `sigil commit` は拒否する（`add` より前に `ls-tree` で調べる）。「変更のない既存のサブモジュールはかまわない」を取り消す | `git add -A` はサブモジュールの中で `git status` を動かし、その子の git はコンテナが書ける入れ子の設定（`sub/.git/config`、差し替えた gitfile の先）と `.gitattributes` を読む。`filter.<x>.clean` がホストで実行されることを git 2.56.1 で再現した。キーの名前をコンテナが決められるので `-c` では防げない。最終レビューで Critical として指摘された |
| 2026-11-23 | push は `HEAD` ではなく刻印したコミットの id を送り（`<id>:<merge の ref>`）、件数と pre-push の入力もその id で求める。`--recurse-submodules=no` を付ける | 確認や pre-push のあいだに HEAD が動くと、利用者が見ていないコミットまで送ってしまうため。全体設定の `push.recurseSubmodules` で、サブモジュールの中の git を動かさないため |
| 2026-11-23 | `sigil commit -- <path>` のパスは `:(top,literal)<path>` として渡し、glob として読まない。既定は `:(top)` | `a[b].txt` が `ab.txt` まで拾うと、確認の画面の範囲と違うファイルが入る。`top` は、コンテナの pre-commit の cwd に左右されないため。`:(top).` は何にも一致しないので使わない |
| 2026-11-23 | LFS の `check-attr` は、確定した tree（`--source`）と作業ツリーの両方で調べ、どちらかが `lfs` なら拒否する | tree だけだと、`.gitignore` 済みで tree に入らないが `add` のときには効いていた `.gitattributes` を取りこぼす。作業ツリーだけだと、`add` のあとにコンテナが `.gitattributes` を書き換えると、コミットする tree と違う属性で判断してしまう |
| 2026-11-23 | 銘のファイルは `git add` の除く指定（`:(exclude,top).sigil-commit`）で外さず、`add -A -- <add の pathspec>` のあとで一時 index から戻す（HEAD があれば `reset -q HEAD -- ':(top,literal).sigil-commit'`、なければ `rm --cached -q --ignore-unmatch`）。ホストとコンテナの pre-commit で同じ手順にする。刻印後の本物の index の `reset` は除く指定を残す。`-- .sigil-commit` の指定はエラーにする | `.sigil-commit` を `.gitignore` に入れると（README の勧めどおり）、除く指定で名指ししただけで `git add` が「無視されたパス」として失敗する（git 2.47 / 2.54 / 2.56 で確認）。実機の確認で、コンテナの pre-commit がこれで止まった。テストは `.gitignore` に入れていなかったので見落とした。`reset` は `.gitignore` の影響を受けず、除く指定のままなら、利用者が add してあった銘のファイルの変更を消さずに済む |
| 2026-11-23 | 状態の印を ASCII の記号（`+` `~` `-` `→`）にする | 装飾記号（Dingbats）は端末やフォントで幅や大きさがそろわず、見栄えが悪かった（実機確認での利用者の判断） |
| 2026-11-23 | `sigil commit` の `d` は、ホストに delta があれば delta で差分を整形する。事前に制御文字を置き換え、`--no-gitconfig --paging=never --line-numbers` で呼び、なければ・失敗したら組み込みの表示に戻す。Plain / Mono では使わない | 読みやすさのための利用者の判断（issue #13）。差分はコンテナの内容なので、delta に生の制御文字を渡さず（出力は delta 自身の SGR と `ESC[K` だけになり `less -R` に渡せる）、`[delta]` の設定を読ませず、環境変数 `DELTA_FEATURES` は子プロセスにだけ空で渡して上書きする（`DELTA_FEATURES=hyperlinks` は `--no-gitconfig` でも OSC 8 を戻す。実機で確認）（hyperlinks で、コンテナが決めたファイル名が OSC 8 のクリックできるリンクになり、`.command` の実行につながるため）。受け入れる危険: 利用者が入れた delta（git や less と同じ立場）が信頼できない内容を解釈するので、delta 自身の不具合は残る。軽減は、事前の置き換え・`--no-gitconfig`・フォールバック |
| 2026-11-23 | CI を ci.yml（テスト・静的検査・ビルド・Codecov）と vulncheck.yml（govulncheck。定期実行も）に分ける | issue #5。定期実行は govulncheck だけでよく、ファイルを分ければ `if:` でほかのジョブを止める必要がない |
| 2026-11-23 | Actions は gh-actions-lock で固定する。拡張はホストに版を固定して入れ、ロックファイルは手元で作る。CI では拡張を入れず `--verify` も回さない。止めるのはランナー側の確認（ロックされたコミットで動くことと、ポリシー「Require lockfile」）に任せ、どちらも確かめられなければ YAML も SHA で固定する | issue #5。公式で、間接的に使う Actions も固定でき、ランナー側の確認と連動する（ほかの候補は pinact、unfunco/gh-pin、amenocal/gh-pin-actions）。拡張はほかのプロジェクトでも使うので、コンテナではなくホストに入れる（GitHub 公式で、gh 自体をすでにホストで信頼しているため。利用者の判断）。CI の `--verify` は、ロックファイルの誤りをランナーより先に知らせるだけの補助で、一人で開発するこのリポジトリでは、CI で拡張を入れて版を管理する手間に見合わない（利用者の判断） |
| 2026-11-23 | Dependabot に `cooldown`（7 日）を付ける | issue #5 のコードレビュー。新しい版が出た当日に PR が来ると、乗っ取られたリリースが、それが知られる前に取り込まれうる。Actions の更新は急がないので、7 日待っても困らない |
| 2026-11-24 | 1Password がない端末向けに、macOS のキーチェーンからも注入できるようにする。提供元は参照のスキーム（`op://` / `keychain://`）で分け、全体設定で提供元を切り替える仕組み（`secret://`）は作らない | issue #25（利用者の判断）。`.env` と `secrets.env` は端末ごとのファイルなので、スキームで分けるだけで端末ごとに使い分けられる。切り替えの仕組みは、1Password の参照とキーチェーンの項目名の対応づけが要り、大きくなる |
| 2026-11-24 | キーチェーンは、sigil が `/usr/bin/security find-generic-password` を実行して値を受け取る（「値を取りに行くのは op run で、sigil は受け取るだけ」の例外） | キーチェーンには `op run` に当たる仕組みがなく、cgo を使わないので Security.framework も呼べない。値がメモリに載るのは op run でも同じで、変わるのは sigil が外部コマンドを実行する点。絶対パスでの実行、service 名の文字の制限、出力の上限で守る |
| 2026-11-24 | 読むのは、全体設定の `keychain` で指定した専用のキーチェーンだけ。指定はプロジェクトの `.sigil` では書けない | 利用者は専用のキーチェーンを作って使っている。リポジトリが書ける `.env` で、ログインキーチェーンの項目（gh のトークンなど）を名指しされないように。今の 1Password（参照先はリポジトリが自由に書け、守るのは承認の画面だけ）より読める範囲が狭くなる |
| 2026-11-24 | 専用キーチェーンは、リンクを解いたパスが `~/Library/Keychains/` の直下にあり、`login.keychain(-db)` ではなく、本人が持ち主の通常のファイルであるときだけ使う。`security` にはリンクを解いたパスを渡す | 設計段階のセキュリティレビュー（H3・M3）。全体設定はリンクを許すので、ログインキーチェーンへのリンクでも通ってしまう。プロジェクトや dotfiles の下に置かれると、コンテナがファイルを持ち出して総当たりしたり、差し替えて解錠のパスワードを入力させたりできる。マウントされる場所だけを拒否する案より判定が単純で確実（利用者の判断） |
| 2026-11-24 | キーチェーンの参照は `keychain://<service>` だけにし、account では絞らない | 専用のキーチェーンなので service だけで項目が決まり、今ある項目をそのまま使える。同じ service の項目を複数作らないよう README で案内する（利用者の判断。レビュー L3 で `-a sigil` に固定する案も出た） |
| 2026-11-24 | キーチェーンから読むはずの名前は、読めなければ渡さない。ホストのシェルの同じ名前の値や、op run が設定した `keychain://...` の文字列には戻らない | 設計段階のセキュリティレビュー（H2）。キーチェーンに置いた権限の狭いトークンの代わりに、シェルの権限の広いトークンが渡りうるため |
| 2026-11-24 | 承認の対象があるかの判定にキーチェーンの組を含める。承認のハッシュは、優先順位を決める前のプロジェクトの組から作り、op run に渡す env ファイル（勝ったものだけ）とは分ける | 設計段階のセキュリティレビュー（H1・M4）。`.sigil` がなくキーチェーンの参照だけの `.env` で、承認を飛ばせた。勝ったものだけでハッシュを作ると、既存の承認が無効になり、全体の `secrets.env` の編集でプロジェクトの承認が変わる |
| 2026-11-24 | 全体の `secrets.env` の行は、`op://`・`keychain://`・生の値のどれでも、プロジェクトの `.env` の参照に勝つ | 7 章の op run の順序（全体が後で勝つ）と同じにし、プロジェクトで全体のシークレットを上書きされないため。生の値の扱いはレビュー（M6）で未定と指摘された |
| 2026-11-24 | `security` は最小の環境・作業フォルダ `/`・標準入力 `/dev/null`・制御端末なし（`Setsid`）・120 秒のタイムアウトで実行する | 設計段階のセキュリティレビュー（M1）。inner の環境には 1Password のシークレットが入っている。端末に直接パスワードを求められたり、ダイアログに誰も応えずに止まったままになったりしないように |
| 2026-11-24 | sigil は読み終えたあとにキーチェーンをロックし直さず、利用者が設定した自動ロックに任せる | 解錠中はホストのほかのプロセスも読めるが、コンテナからは届かず sigil の守る範囲の外。コンテナを作るたびにパスワードを聞かれるのを避ける（利用者の判断） |
| 2026-11-24 | 専用キーチェーンの項目は、承認さえ通ればどのプロジェクトからも読めることを受け入れる | 1Password の vault と同じ性質で、承認の画面に service 名を必ず出す。プロジェクトごとの許可の仕組みは、必要になったら別の issue にする（レビュー M5） |
| 2026-11-24 | 実機の検証で、テスト用のキーチェーンの作成・項目の追加・ロック・削除を Claude が `security` で行ってよい。テスト用のキーチェーンは `~/Library/Keychains/sigil-dev-test-*` に作り、終わったら消して検索リストが元に戻ったことを確かめる | 置き場所の制限があるので一時フォルダには作れない。利用者のキーチェーンは開かない（利用者の判断） |
| 2026-11-24 | `security` の子プロセスの環境は空にする。ロックされているときは macOS の解錠ダイアログに任せ、失敗したときの案内（`security unlock-keychain`）はエラーに添える | 実機（テスト用キーチェーン）で、空の環境でも読め、ロック時はダイアログが出て解錠後に値が返ることを確かめた。`-T ""` で作った項目は読むたびに確認が出た。表示できない文字を含む値は 16 進で出力された（README で、値は印字できる文字の 1 行にするよう案内する） |
| 2026-11-24 | 項目の足し方は、シェルではなく `security -i`（対話モード）の中で `-w <値>` を打つよう案内する | シェルで `-w 値` と打つと `ps` と履歴に残る。`-w` のあとにキーチェーンのパスを続けると、パスが値として読まれ、既定のキーチェーン（ログインキーチェーン）に入ってしまう |
| 2026-11-24 | ログインキーチェーンの拒否は、名前を小文字にして `login` で始まるもの（`login_renamed_*` を含む）と、`login.keychain(-db)` と同じファイル（`os.SameFile`）にする | タスクのレビューで、APFS は大文字小文字を区別しないので `Login.keychain-db` が判定をすり抜けると分かった（実機で再現）。パスワードを変えたあとに残る古いログインキーチェーンも拒否するため |
| 2026-11-24 | `security` の時間切れでは、プロセスグループごと止める（`Setsid` で作ったグループに `kill(-pid)`） | 子だけを止めると、孫がパイプを持ったままになり、待ちが長引いた（実装中に確かめた） |
| 2026-11-24 | プロジェクトの参照が全体の `secrets.env` の生の値・`op://` に負けた名前は、全体の値を読み込めたとき（op run から起動し直し、`secrets.env` を op run に渡したとき）だけ渡す。読み込めなければ注意を出して渡さず、`LookupEnv` には戻らない | ブランチ全体の最終レビュー（IMPORTANT 1）。全体の値が環境に入るのは op run 経由だけで、op run に `secrets.env` を渡すのは `op://` を含むときだけ。`op` のない Mac では、プロジェクトのキーチェーンの参照が捨てられ、ホストのシェルの同じ名前の値が代わりに渡っていた |
| 2026-11-24 | `security` に存在しないパスを渡したときに検索リストへ落ちるかは決着がつかなかったので、sigil は `security` を呼ぶ前にキーチェーンのファイルの存在を確かめる | 実機の検証（手順 5）で、存在しないパスも項目がないときと同じ終了コード 44 になり、見分けられなかった（ブランチ全体の最終レビュー） |
| 2026-11-24 | Bitwarden は今回の範囲に入れない | `bw` には `op run` に当たる仕組みがなく、セッショントークンの扱いが要る。`bws run` は別の製品（Secrets Manager）。必要になったら別の issue にする（利用者の判断） |
| 2026-11-24 | 読み取り専用のマウントの書式は `ro <パス>`。マウント先は `/workspace/<書かれたパスのフォルダ名>` に固定し、コンテナ内のパスは指定させない（issue #2） | 書き込めるマウントを将来足すなら `rw` を別の指定にすれば `ro` の意味が変わらない。`<host>:<container>` は `-v` の区切りと紛らわしく、フォルダ名で WakaTime や Claude Code が見分けられれば足りる（利用者の判断） |
| 2026-11-24 | `ro` はプロジェクトの `.sigil` でだけ書ける | 全体設定に書くと、すべてのプロジェクト（信頼できないものを含む）のコードからそのフォルダが読めるため。必要になってから足す（利用者の判断） |
| 2026-11-24 | マウントしたフォルダの中の `.env` などは隠さず、承認の画面と README で注意する | 空のファイルを重ねる方法は、`.env.local` やサブフォルダの `.env` などを覆いきれず、守れている気にさせるため（利用者の判断） |
| 2026-11-24 | `ro` の承認には、`.sigil` の文字列に加えて、解決したホスト側のパスとマウント先を含める。`ro` がなければハッシュは今までと同じ | 承認のあとにシンボリックリンクの向きを変えるだけで、別のフォルダがマウントされるのを防ぐため。既存の承認記録を使えるようにするため |
| 2026-11-24 | `ro` でマウントしてよい範囲を、全体設定の `ro_root` で決める（許可リスト）。`ro_root` がなければ `ro` はエラー | 設計段階のセキュリティレビュー（H1）。拒否リスト（`CheckSafe`）だけでは、`~/.kube`、`~/.claude`、`~/.local`（sigil 本体と承認記録）、別のプロジェクトの `.env` などを、信頼できない `.sigil` から読ませられた。範囲はホストの信頼できる側で決める（利用者の判断） |
| 2026-11-24 | `ro` は承認の前に加えて、`docker run` の直前にも解決し直し、承認したものと一致しなければ作らない。別の sigil のプロジェクトの中は拒否しない | 設計段階のセキュリティレビュー（H2）。承認から作成までに op run やキーチェーンのダイアログが入り、別のプロジェクトのコンテナにパスを差し替えられる窓が長くなる。別のプロジェクトを拒否すると、自分で開発しているライブラリを読むという主な用途ができなくなるので、残る短い窓は受け入れる（利用者の判断） |
| 2026-11-24 | `.sigil` に NUL があればエラーにする | 設計段階のセキュリティレビュー（M4）。承認のハッシュは NUL で区切るので、`.sigil` に NUL を入れて区切りをずらせないようにする |
| 2026-11-24 | マウント先の名前は、NFC / NFD の正規化をせずにバイト列で衝突を判定する | 設計段階のセキュリティレビュー（M1）への対応。コンテナ（Linux）ではバイト列が違えば別のフォルダになり、ほかのマウントを覆わない。標準ライブラリに正規化がない |
| 2026-11-24 | `ro` のマウント先には `safe.directory` を足さない | 実機の検証で、足さなくてもコンテナ内の `git log` / `git status` が動いた |
