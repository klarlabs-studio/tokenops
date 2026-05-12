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

.PHONY: all build test fmt vet lint verify clean tools tidy ci run-daemon bench bench-gate sec sec-gate sec-remediate policy-guard install-hooks eval eval-gate cover-debt cover-debt-gate

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

# eval runs the full optimizer quality eval suite against offline
# fixtures. Reports per-optimizer quality scores, compression ratios,
# and success rates. Use this locally to iterate on optimizer changes.
eval:
	$(GO) test -count=1 -run TestEvalSuitesPass -v ./internal/eval/

# eval-gate is the CI regression gate for optimizer quality. It asserts
# that the aggregate success rate across all eval fixtures stays above
# the minimum threshold defined in the test. Add new eval fixtures to
# internal/eval/testdata/ to expand coverage.
eval-gate:
	$(GO) test -count=1 -run TestEvalSuitesPass ./internal/eval/

fmt:
	$(GO) fmt $(PKG)

vet:
	$(GO) vet $(PKG)

lint:
	$(GOLANGCI_LINT) run

tidy:
	$(GO) mod tidy

# cover-debt prints the per-domain coverage report using coverctl.
# Non-zero exit when a domain drops below its threshold.
# Install coverctl: go install github.com/coverctl/coverctl@latest
COVERCTL ?= coverctl
COVERCTL_CONFIG ?= .coverctl.yaml

cover-debt:
	-$(COVERCTL) check -config $(COVERCTL_CONFIG)

verify: fmt vet lint test bench-gate eval-gate sec-gate proto-verify

# proto-verify runs proto-check only when protoc is on PATH; CI without
# protoc skips the gate (the targets exist for local devs who modify
# .proto files).
proto-verify:
	@if command -v protoc >/dev/null; then \
		$(MAKE) proto-check; \
	else \
		echo "proto-verify: protoc not installed; skipping"; \
	fi

# policy-guard enforces local contribution policy checks.
policy-guard:
	@bash scripts/require-pr-branch.sh

# sec runs the full nox scan and writes findings.json to the working
# tree. Use this for triaging; the result file is gitignored.
sec:
	nox scan .

# sec-gate runs the same critical-only gate the security workflow runs
# in CI. Fails on any unwaived critical finding. nox scan exits non-zero
# whenever findings exist; we ignore that and use scripts/sec-gate.py to
# enforce the critical+VEX policy explicitly.
sec-gate:
	-nox scan . > /dev/null
	@python3 scripts/sec-gate.py

# sec-remediate applies nox's OSV-driven dep upgrades, then refreshes
# the language manifests so the change is consistent end-to-end. Set
# INCLUDE_MAJOR=1 to allow major-version bumps.
sec-remediate:
	nox scan .
	@if [ "$(INCLUDE_MAJOR)" = "1" ]; then \
		nox fix -include-major -input findings.json; \
	else \
		nox fix -input findings.json; \
	fi
	$(GO) mod tidy
	@if [ -f web/dashboard/package.json ]; then \
		(cd web/dashboard && npm install --package-lock-only --silent); \
	fi
	@if [ -f web/docs/package.json ]; then \
		(cd web/docs && npm install --package-lock-only --silent); \
	fi

ci: verify build

clean:
	rm -rf $(BIN_DIR) coverage.out

tools:
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# proto regenerates Go bindings from pkg/eventschema/proto/v1/*.proto.
# Requires protoc + protoc-gen-go (run `make tools`).
proto:
	@command -v protoc >/dev/null || { echo "protoc not installed; install from https://grpc.io/docs/protoc-installation/"; exit 1; }
	protoc \
		--go_out=. \
		--go_opt=module=github.com/felixgeelhaar/tokenops \
		pkg/eventschema/proto/v1/*.proto

# proto-check fails CI when generated bindings drift from the .proto
# source. Add to make verify when bindings land in the repo.
proto-check: proto
	@if ! git diff --quiet -- pkg/eventschema/proto/; then \
		echo "::error::proto bindings out of sync; run 'make proto'"; \
		git --no-pager diff -- pkg/eventschema/proto/; \
		exit 1; \
	fi

run-daemon: $(BIN_DIR)/tokenopsd
	./$(BIN_DIR)/tokenopsd start

# install-hooks installs repository-managed git hooks for local policy
# checks (pre-push currently).
install-hooks:
	@mkdir -p .git/hooks
	@cp scripts/git-hooks/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Installed .git/hooks/pre-push"
