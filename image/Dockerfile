# syntax=docker/dockerfile:1
#
# sigil: 隔離された開発用コンテナ
#   Neovim (LazyVim) / Claude Code / Node.js / Go / Rust / PHP / Python (uv)
#
# ビルド:  sigil build   （bin/sigil が UID/GID を合わせて docker build します）

# 方針: 言語ランタイムとツールは、すべてその時点の最新版を使う（sigil build のたびに更新される）。
# ただし Node.js は、npm パッケージの互換性を優先して最新の LTS にする。
# ベースは Debian 13 (trixie)。bookworm の glibc 2.36 では、最新の tree-sitter CLI が動かない。
ARG NODE_IMAGE=node:lts-trixie-slim
ARG GO_IMAGE=golang:trixie
ARG RUST_IMAGE=rust:slim-trixie
ARG COMPOSER_IMAGE=composer:2
ARG UV_IMAGE=ghcr.io/astral-sh/uv:latest
# Debian の apt の PHP は最新ではないので、公式 PHP イメージ（中身は debian:trixie-slim）を土台にする
ARG PHP_IMAGE=php:cli-trixie

FROM ${NODE_IMAGE} AS node
FROM ${GO_IMAGE} AS go
FROM ${RUST_IMAGE} AS rust
FROM ${COMPOSER_IMAGE} AS composer
FROM ${UV_IMAGE} AS uv

FROM ${PHP_IMAGE}

ARG TARGETARCH
ARG NVIM_VERSION=stable
ARG USERNAME=dev
ARG USER_UID=1000
ARG USER_GID=1000

ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    DEBIAN_FRONTEND=noninteractive

# --- 基本ツール ---------------------------------------------------------
# openssh-client はあえて入れない（コンテナから SSH で push させないため）
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git less procps file jq \
      unzip zip xz-utils \
      build-essential pkg-config libssl-dev \
      ripgrep fd-find fzf bash-completion \
      libzip-dev libicu-dev \
 && ln -s /usr/bin/fdfind /usr/local/bin/fd \
 && rm -rf /var/lib/apt/lists/*

# --- LazyVim が使うツール ------------------------------------------------
# lua5.1 / luarocks: lazy.nvim が luarocks を使うプラグインを入れるためのもの。
#   なければ lazy.nvim は hererocks で Lua と luarocks を ~/.local/share/nvim（全プロジェクト
#   共通のボリューム）に自分でビルドする。apt で root 所有の場所に入れておけば、
#   コンテナの中から書き換えられず、ビルドも走らない。
# libsqlite3-dev: snacks.picker の履歴（frecency）を SQLite に保存するためのもの。
#   LuaJIT の FFI で libsqlite3.so を読むので、-dev にしかない .so の名前のリンクが要る。
# bat: fzf-lua のプレビューと lazygit で使う。Debian では batcat という名前なので、fd と同じくリンクを張る。
# git-delta: git と lazygit の差分の表示（設定は下の /etc/gitconfig と /etc/xdg/lazygit）。
# どれも apt の版で足りる（delta はリリースがまれで、apt の版が最新に近い）。
RUN apt-get update && apt-get install -y --no-install-recommends \
      lua5.1 liblua5.1-0-dev luarocks \
      libsqlite3-dev \
      bat git-delta \
 && ln -s /usr/bin/batcat /usr/local/bin/bat \
 && rm -rf /var/lib/apt/lists/*

# --- PHP 拡張 -----------------------------------------------------------
# mbstring / xml / curl / sqlite3 は公式イメージに最初から入っている。足りないものだけビルドする
RUN docker-php-ext-install -j"$(nproc)" zip intl bcmath

# --- Neovim（LazyVim は新しい Neovim が必要なので公式リリースから） ----
RUN case "${TARGETARCH}" in \
      amd64) arch=x86_64 ;; \
      arm64) arch=arm64 ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && curl -fsSL "https://github.com/neovim/neovim/releases/download/${NVIM_VERSION}/nvim-linux-${arch}.tar.gz" \
    | tar -xz -C /opt \
 && ln -s "/opt/nvim-linux-${arch}/bin/nvim" /usr/local/bin/nvim

# --- 言語ランタイム（公式イメージからコピー） ---------------------------
COPY --from=node /usr/local/bin/ /usr/local/bin/
COPY --from=node /usr/local/lib/node_modules/ /usr/local/lib/node_modules/
COPY --from=node /usr/local/include/node/ /usr/local/include/node/
COPY --from=go /usr/local/go /usr/local/go
COPY --from=rust /usr/local/rustup /usr/local/rustup
COPY --from=rust /usr/local/cargo /usr/local/cargo
COPY --from=composer /usr/bin/composer /usr/local/bin/composer

# pnpm / yarn（corepack）と、LazyVim の treesitter が使う tree-sitter CLI
RUN (corepack enable || true) \
 && npm install -g tree-sitter-cli \
 && npm cache clean --force

# --- Python（uv で最新の安定版） ----------------------------------------
# Debian の apt の Python（trixie は 3.13）は最新ではないので、uv で入れる。
# MCP サーバーの多くは `uvx <package>` で起動するので、uv / uvx も入れる。
# 置き場所は root 所有の /opt にし、dev ユーザーからは書き換えられないようにする
# （UV_PYTHON_INSTALL_DIR はこの RUN の中だけで使う。ENV に残すと、dev ユーザーの
#   `uv python install` がこの場所に書こうとして失敗するため）。
# uv が入れる Python には pip が同梱されているが、EXTERNALLY-MANAGED の印があるので、
# venv の外での `pip install`（--user を含む）は拒否される。パッケージは venv / uvx で入れる。
# --no-bin: root の ~/.local/bin に python3.x のリンクを作らせない（入口は下の /usr/local/bin だけにする）。
COPY --from=uv /uv /uvx /usr/local/bin/
RUN export UV_PYTHON_INSTALL_DIR=/opt/uv-python UV_NO_CACHE=1 \
 && uv python install --no-bin \
 && py="$(uv python find --managed-python)" \
 && ln -s "$py" /usr/local/bin/python3 \
 && ln -s python3 /usr/local/bin/python \
 && python3 --version

# --- wakatime-cli -------------------------------------------------------
# vim-wakatime は、PATH に wakatime-cli があればそれを使い、自動更新（Python による
# ~/.wakatime/ へのダウンロード）をしない。~/.wakatime/ は永続化していないので、
# イメージに入れておかないと、コンテナを作り直すたびにダウンロードが走る。
# root 所有の /usr/local/bin に置くので、コンテナの中からは差し替えられない。
# チェックサムは同じリリースのものなので、壊れたダウンロードを検出するためのもの。
RUN case "${TARGETARCH}" in \
      amd64|arm64) arch="${TARGETARCH}" ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && tmp="$(mktemp -d)" && cd "$tmp" \
 && base=https://github.com/wakatime/wakatime-cli/releases/latest/download \
 && curl -fsSLO "${base}/wakatime-cli-linux-${arch}.zip" \
 && curl -fsSLO "${base}/checksums_sha256.txt" \
 && grep " wakatime-cli-linux-${arch}.zip\$" checksums_sha256.txt | sha256sum -c - \
 && unzip -q "wakatime-cli-linux-${arch}.zip" \
 && install -m 755 "wakatime-cli-linux-${arch}" /usr/local/bin/wakatime-cli \
 && cd / && rm -rf "$tmp" \
 && wakatime-cli --version

# --- lazygit ------------------------------------------------------------
# apt の版（trixie は 0.50.0）は古く、設定の書き方（git.paging）も今と違うので、
# GitHub のリリースから最新を取る。ファイル名に版が入るので、releases/latest の
# リダイレクト先から版を読む。チェックサムは wakatime-cli と同じく、壊れたダウンロードの検出用。
RUN case "${TARGETARCH}" in \
      amd64) arch=x86_64 ;; \
      arm64) arch=arm64 ;; \
      *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && tag="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/jesseduffield/lazygit/releases/latest)" \
 && ver="${tag##*/v}" \
 && case "$ver" in [0-9]*.[0-9]*) ;; *) echo "unexpected lazygit tag: $tag" >&2; exit 1 ;; esac \
 && tmp="$(mktemp -d)" && cd "$tmp" \
 && base="https://github.com/jesseduffield/lazygit/releases/download/v${ver}" \
 && file="lazygit_${ver}_linux_${arch}.tar.gz" \
 && curl -fsSLO "${base}/${file}" \
 && curl -fsSLO "${base}/checksums.txt" \
 && grep " ${file}\$" checksums.txt | sha256sum -c - \
 && tar -xzf "$file" lazygit \
 && install -m 755 lazygit /usr/local/bin/lazygit \
 && cd / && rm -rf "$tmp" \
 && lazygit --version

# --- 差分の表示（delta） --------------------------------------------------
# ホストの ~/.gitconfig はコンテナに渡さないので、システム全体の設定に書く。
# lazygit の設定は、XDG_CONFIG_DIRS（/etc/xdg）から読まれる場所に置く。どちらも root 所有なので、
# コンテナの中からは書き換えられず、イメージを作るたびに同じ内容になる。
# --{{colorScheme}} は、lazygit が端末の明暗に合わせて --dark / --light に置き換える。
RUN git config --system core.pager delta \
 && git config --system interactive.diffFilter 'delta --color-only' \
 && git config --system delta.navigate true \
 && mkdir -p /etc/xdg/lazygit \
 && printf '%s\n' \
      'git:' \
      '  diffRenderers:' \
      '    - command: delta --{{colorScheme}} --paging=never' \
    > /etc/xdg/lazygit/config.yml

# --- 一般ユーザー -------------------------------------------------------
# Mac の GID 20 (staff) は Debian 側に既にあるので、存在すれば流用する
RUN if ! getent group "${USER_GID}" >/dev/null; then \
      groupadd --gid "${USER_GID}" "${USERNAME}"; \
    fi \
 && useradd --uid "${USER_UID}" --gid "${USER_GID}" -m -s /bin/bash "${USERNAME}" \
 && chown -R "${USER_UID}:${USER_GID}" /usr/local/rustup \
 && git config --system safe.directory /workspace

# 名前付きボリュームのマウント先を先に作っておく
# （空のボリュームは初回にこのディレクトリの所有者を引き継ぐ）
RUN mkdir -p \
      /home/${USERNAME}/.config \
      /home/${USERNAME}/.local/bin \
      /home/${USERNAME}/.local/share/nvim \
      /home/${USERNAME}/.local/state/nvim \
      /home/${USERNAME}/.claude \
      /home/${USERNAME}/.cache \
      /home/${USERNAME}/.cargo/bin \
      /home/${USERNAME}/.npm-global \
      /home/${USERNAME}/go \
      /workspace \
 && chown -R "${USER_UID}:${USER_GID}" /home/${USERNAME} /workspace

COPY sigil-check /usr/local/bin/sigil-check
RUN chmod 755 /usr/local/bin/sigil-check

USER ${USERNAME}

ENV PATH=/home/${USERNAME}/.local/bin:/home/${USERNAME}/.npm-global/bin:/home/${USERNAME}/.cargo/bin:/home/${USERNAME}/go/bin:/usr/local/go/bin:/usr/local/cargo/bin:${PATH} \
    GOPATH=/home/${USERNAME}/go \
    CARGO_HOME=/home/${USERNAME}/.cargo \
    RUSTUP_HOME=/usr/local/rustup \
    NPM_CONFIG_PREFIX=/home/${USERNAME}/.npm-global \
    npm_config_cache=/home/${USERNAME}/.cache/npm \
    CLAUDE_CONFIG_DIR=/home/${USERNAME}/.claude \
    DISABLE_AUTOUPDATER=1 \
    EDITOR=nvim \
    SIGIL=1

# --- Claude Code（公式のネイティブインストーラー） -----------------------
# 更新はイメージの作り直しで行う（DISABLE_AUTOUPDATER=1）
RUN curl -fsSL https://claude.ai/install.sh | bash

# コンテナ内だと一目で分かるプロンプト
RUN printf '%s\n' \
      'PS1="\[\e[1;35m\][sigil]\[\e[0m\] \w \$ "' \
      'alias vim=nvim' \
    >> /home/${USERNAME}/.bashrc

WORKDIR /workspace
CMD ["sleep", "infinity"]
