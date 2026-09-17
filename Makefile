GO      ?= go
BIN     ?= zlib
PREFIX  ?= /usr/local
# The version reported by `zlib version` is injected at link time; a locally
# built binary without -ldflags -X falls back to "dev" in cmd/root.go.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X zlib/cmd.version=$(VERSION)

.PHONY: all build test test-verbose vet fmt fmt-check lint cover install uninstall clean smoke check

all: check build

## build: compile the single static binary
build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) .

## test: run the full test suite
test:
	$(GO) test ./...

## test-verbose: run tests with per-case output
test-verbose:
	$(GO) test -v ./...

## vet: run the standard analyzer
vet:
	$(GO) vet ./...

## fmt: rewrite sources with gofmt
fmt:
	$(GO) fmt ./...

## fmt-check: fail if any file is not gofmt-clean
fmt-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "these files are not gofmt-clean:"; echo "$$files"; exit 1; \
	fi

## lint: vet plus a formatting check
lint: vet fmt-check

## cover: report coverage per package
cover:
	$(GO) test -cover ./...

## install: place the binary on PATH
install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)

## uninstall: remove the installed binary
uninstall:
	rm -f $(PREFIX)/bin/$(BIN)

## smoke: exercise the CLI end to end without touching the network or the
## caller's real config (ZLIB_CONFIG_DIR is isolated to a temp directory)
smoke: build
	@tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	ZLIB_CONFIG_DIR="$$tmp" ./$(BIN) config set --zlib-email smoke@example.com --zlib-password pw >/dev/null; \
	ZLIB_CONFIG_DIR="$$tmp" ./$(BIN) config show >/dev/null; \
	ZLIB_CONFIG_DIR="$$tmp" ./$(BIN) --json config show >/dev/null; \
	ZLIB_CONFIG_DIR="$$tmp" ./$(BIN) doctor >/dev/null 2>&1 || true; \
	./$(BIN) help >/dev/null; \
	./$(BIN) version >/dev/null; \
	./$(BIN) search >/dev/null 2>&1 && { echo "expected a usage error from a bare search"; exit 1; } || true; \
	echo "smoke: ok"

## check: everything CI should run
check: lint test

## clean: remove build artifacts
clean:
	rm -f $(BIN)
	$(GO) clean -testcache

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
