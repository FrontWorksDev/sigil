BIN := bin/sigil
INSTALL_PATH := $(HOME)/.local/bin/sigil
# 改名前のコマンド名。互換は残さないので、install のときに消す。
# ただし、別の dev というツールや、利用者のスクリプトへのリンクを消さないように、
# 古い Go 版の dev だと確かめられたときだけ消す（実行はせず、埋め込まれた devbox-check の文字列で判定）。
OLD_PATH := $(HOME)/.local/bin/dev

# sigil --version で表示する版（issue #6）。タグがなければコミットの短い名前になる。
# git がない場所では空になり、sigil は dev と表示する。
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null)

.PHONY: build install test vet fmt check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-X sigil/internal/app.version=$(VERSION)" -o $(BIN) .

# 置き場所にシンボリックリンクがあると、install はリンク先を上書きしてしまうので、
# 先にリンクだけを消してから実体を置く。
install: build
	mkdir -p $(dir $(INSTALL_PATH))
	rm -f $(INSTALL_PATH)
	install -m 0755 $(BIN) $(INSTALL_PATH)
	@if [ -f "$(OLD_PATH)" ] && [ ! -L "$(OLD_PATH)" ] && grep -q 'devbox-check' "$(OLD_PATH)"; then \
		rm -f "$(OLD_PATH)"; \
		echo "古い dev（$(OLD_PATH)）を消しました"; \
	elif [ -e "$(OLD_PATH)" ] || [ -L "$(OLD_PATH)" ]; then \
		echo "注意: $(OLD_PATH) は古い Go 版の dev ではないので、消していません"; \
	fi

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt が必要: $$out"; exit 1; fi

check: test vet fmt
