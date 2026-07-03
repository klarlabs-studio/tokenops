---
updated: 2026-07-03
tags: [architecture]
---
# tokenops architecture

DDD with bounded contexts under `internal/contexts/*`, enforced by `internal/archlint` (domain packages must not import adapters/infra; new domain packages must be added to `domainPackages`). Full doc: `docs/architecture-ddd.md`.

## Contexts
- **Prompts** — tokenizer, providers, llm.
- **Optimization** — `optimizer/*` (pipeline: prompt_compress, command_fmt, dedupe, retrieval_prune, context_trim, router), `eval`, `replay`, `formatter` (fmt engine), `fmtlearn` (learn analysis).
- **Coaching** — coaching, efficiency, waste.
- **Spend / Observability / Governance / Security / Workflows / Rules** — see architecture-ddd.md.

## fmt subsystem (see memory note fmt-subsystem for detail)
- `optimization/formatter` — 46 formatters + engine. `enforceCritical` guarantees critical-line survival for built-in AND config formatters. `DefaultFormatters()` = single source of truth.
- `optimization/fmtlearn` — pure `Analyze()` → advisory report.
- `optimization/optimizer/toolfmt` — proxy-plane tool-output compressor.
- `infra/fmtindex` — shared jsonl learn-index reader/writer (CLI + MCP). Infra, NOT in archlint domain list.

## Adapters
- `internal/proxy` — single HTTP adapter (server, routes, analytics/rules/events handlers).
- `internal/cli` — cobra CLI; `internal/mcp` — MCP tool surface (26 tools).
- `internal/storage/sqlite` — event store (Envelope-based Append/Query).

## Release
Tag `v*` → goreleaser (binaries + homebrew tap). See memory note tokenops-release-flow.
