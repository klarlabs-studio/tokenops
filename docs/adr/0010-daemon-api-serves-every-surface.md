# ADR 0010 — The daemon API serves every surface

- **Status:** Accepted 2026-10-03. Slices 1–4 implemented.
- **Date:** 2026-10-03
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0004 (phase 4 capability layer, phase 9 surface-native insight), ADR 0009 (endpoints, routers, billers), `docs/competitive-landscape.md` (G7, G8)

## Context

TokenOps answers the same questions on three surfaces: the CLI, the MCP
server and the status line. A fourth is coming: a menu-bar app built on
Vitra, the Klarlabs Go and web desktop runtime, once Vitra's macOS tray
lands. After that come an OpenTelemetry export and a team view.

The daemon's local HTTP API cannot answer most of those questions today.
It serves spend, forecast, workflows, rules, audit, domain events and
source freshness. It does not serve what a menu bar shows first: plan
windows and headroom, the session budget, spend by paying account, the
coach's open tip, the active mode. Those live in the MCP package, which
computes them in its own process from the SQLite store, and in the CLI.

A desktop app could open the store itself, as `tokenops serve` does. That
is how the CLI and MCP server came to disagree about plan headroom, agent
DX and status (see `internal/archlint/capability_test.go`): every reader
grew its own copy. A web frontend in a desktop shell also should not hold
a SQLite handle or the business rules.

## Decision

1. **The daemon API is the read model for every surface that is not an
   agent.** A menu bar, a dashboard, a script or a team collector reads
   the daemon over HTTP and never opens the store. MCP stays the agent's
   interface; the CLI may keep reading the store directly for commands
   that must work without a running daemon.

2. **Every endpoint calls a capability package.** An endpoint is added
   together with the `internal/capability` use case it serves, and the CLI
   and MCP tool for the same question move onto that use case in the same
   change. The ratchet in `internal/archlint/capability_test.go` already
   forbids new adapter → domain imports, so this also shrinks the
   migration backlog.

3. **Coverage is tested.** A parity test fails when an MCP read tool has
   no API route and is not on a short, reasoned exemption list (agent-only
   tools such as `tokenops_prepare_work`). Name parity is not capability
   parity, so the test also checks that both answers come from the same
   capability function.

4. **One contract.** JSON with the same field names the MCP tools'
   structured output uses, under `/api/`, behind the existing `DashAuth`
   token on loopback. An OpenAPI document is generated from the handlers'
   types and checked in CI. Errors use the MCP `{error, hint}` shape, so
   "no plan bound" reads the same everywhere.

5. **Privacy line unchanged.** The API serves derived figures: windows,
   spend, grades, tips, counts. It never serves prompt text, file contents
   or transcripts, so the same contract can later feed an OpenTelemetry
   exporter or a team rollup without a second review (positioning decision
   of 2026-09-15).

6. **Actions come last and through authority.** Writes (switch mode, bind
   a plan, set a budget, accept or dismiss a coach tip, approve a routing
   proposal) go through `internal/capability/authority` and the audit log,
   exactly as the MCP tools do. They are a separate slice.

## Slices

| Slice | Endpoints | Capability work |
|---|---|---|
| 1 — menu bar core | `GET /api/glance`, `GET /api/plans/headroom`, `GET /api/plans/session-budget` | Move the session budget and the glance insight out of `internal/mcp` into `internal/capability/headroom` |
| 2 — coach and state | `GET /api/status`, `GET /api/coach` (mode, preset, open tip), `GET /api/vendor-usage` | Status and coach read models into capability packages |
| 3 — work quality | `GET /api/dx`, `GET /api/scorecard`, `GET /api/story`, `GET /api/spend/top`, `GET /api/spend/burn-rate` | `capability/dx`, scorecard, story; removes the matching adapter → domain imports |
| 4 — actions | `POST /api/mode`, `POST /api/plans`, `POST /api/budgets`, `POST /api/coach/tips/{id}` | Through `capability/authority`, audited |
| 5 — contract | OpenAPI document, parity test, docs page | — |

The parity test (§3) lands with slice 1, with every route not yet built on
the exemption list. Each later slice must shorten that list.

## Consequences

- The menu-bar app is a thin web frontend; its timing depends only on
  Vitra's macOS tray.
- The daemon becomes required for those surfaces. The CLI keeps working
  without it.
- Each slice shrinks the adapter → domain ratchet, so the CLI and MCP
  divergences this ADR's context describes cannot recur through the API.
- The API surface grows and needs versioning discipline. Field names
  follow the MCP structured output, which agents already depend on.
