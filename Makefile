SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

GO ?= go
GOLANGCI_LINT ?= golangci-lint
BIN_DIR := bin
BINARIES := tokenops tokenopsd
PKG := ./...

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X github.com/felixgeelhaar/tokenops/internal/version.Version=$(VERSION) \
  -X github.com/felixgeelhaar/tokenops/internal/version.Commit=$(COMMIT) \
  -X github.com/felixgeelhaar/tokenops/internal/version.Date=$(DATE)

.PHONY: all build test fmt vet lint verify clean tools tidy ci run-daemon

all: build

build: $(addprefix $(BIN_DIR)/,$(BINARIES))

$(BIN_DIR)/%:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $@ ./cmd/$*

test:
	$(GO) test -race -count=1 -coverprofile=coverage.out $(PKG)

fmt:
	$(GO) fmt $(PKG)

vet:
	$(GO) vet $(PKG)

lint:
	$(GOLANGCI_LINT) run

tidy:
	$(GO) mod tidy

verify: fmt vet lint test

ci: verify build

clean:
	rm -rf $(BIN_DIR) coverage.out

tools:
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

run-daemon: $(BIN_DIR)/tokenopsd
	./$(BIN_DIR)/tokenopsd start
