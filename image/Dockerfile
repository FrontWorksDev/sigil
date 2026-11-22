# syntax=docker/dockerfile:1
#
# sigil: 隔離された開発用コンテナ
#   Neovim (LazyVim) / Claude Code / Node.js / Go / Rust / PHP
#
# ビルド:  sigil build   （bin/sigil が UID/GID を合わせて docker build します）

# 方針: 言語ランタイムとツールは、すべてその時点の最新版を使う（sigil build のたびに更新される）。
# ただし Node.js は、npm パッケージの互換性を優先して最新の LTS にする。
# ベースは Debian 13 (trixie)。bookworm の glibc 2.36 では、最新の tree-sitter CLI が動かない。
ARG NODE_IMAGE=node:lts-trixie-slim
ARG GO_IMAGE=golang:trixie
ARG RUST_IMAGE=rust:slim-trixie
ARG COMPOSER_IMAGE=composer:2
# Debian の apt の PHP は最新ではないので、公式 PHP イメージ（中身は debian:trixie-slim）を土台にする
ARG PHP_IMAGE=php:cli-trixie

FROM ${NODE_IMAGE} AS node
FROM ${GO_IMAGE} AS go
FROM ${RUST_IMAGE} AS rust
FROM ${COMPOSER_IMAGE} AS composer

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
