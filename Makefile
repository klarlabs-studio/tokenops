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

.PHONY: all build test fmt vet lint verify clean tools tidy ci run-daemon bench bench-gate

all: build

build: $(addprefix $(BIN_DIR)/,$(BINARIES))

$(BIN_DIR)/%:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $@ ./cmd/$*

test:
	$(GO) test -race -count=1 -coverprofile=coverage.out $(PKG)

# bench runs the proxy overhead microbenchmarks. Useful locally; numbers
# vary with hardware so this is informational, not a CI gate.
bench:
	$(GO) test -run=^$$ -bench='BenchmarkProxy' -benchtime=2s -benchmem ./internal/proxy/

# bench-gate runs the proxy p99-overhead gate (TestProxyP99OverheadGate).
# This is the CI regression gate for proxy-bench: it asserts proxy p99
# overhead stays below 50ms (override via TOKENOPS_BENCH_P99_MS).
bench-gate:
	$(GO) test -run TestProxyP99OverheadGate -count=1 ./internal/proxy/

fmt:
	$(GO) fmt $(PKG)

vet:
	$(GO) vet $(PKG)

lint:
	$(GOLANGCI_LINT) run

tidy:
	$(GO) mod tidy

verify: fmt vet lint test bench-gate

ci: verify build

clean:
	rm -rf $(BIN_DIR) coverage.out

tools:
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

run-daemon: $(BIN_DIR)/tokenopsd
	./$(BIN_DIR)/tokenopsd start
