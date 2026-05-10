# Coverage Debt Dashboard

Risk-ranked quality debt tracker for low-coverage, high-impact packages.
Goals are per-package, not blanket percentages.

## Scoring

Each package is ranked by **Risk Score** = `impact × (1 - coverage)`, where:

- **Critical (10)**: Boot sequence, entrypoints, data persistence — a failure stops the daemon or corrupts data.
- **High (7)**: Integration surfaces, handlers, serialization — a failure breaks a feature but does not crash the daemon.
- **Medium (4)**: Utility, config, formatting — a failure degrades output or requires a restart.
- **Low (1)**: Thin entry wrappers that delegate immediately.

Coverage goals target risk reduction: Critical and High packages must meet their threshold before Medium/Low can be addressed.

## Current State

As of `1d03a0d`:

| Package | Files | Coverage | Risk | Risk Score | Goal | Gap |
|---|---|---|---|---|---|---|
| `internal/daemon` | 1 | 0% | Critical (10) | 10.0 | ≥50% | 50 pts |
| `cmd/tokenopsd` | 1 | 0% | Critical (10) | 10.0 | ≥50% | 50 pts |
| `internal/mcp` | 3 | ~35% | High (7) | 4.6 | ≥60% | 25 pts |
| `internal/storage/sqlite` | 6 | ~55% | High (7) | 3.2 | ≥70% | 15 pts |
| `internal/otlp` | 2 | ~65% | Medium (4) | 1.4 | ≥75% | 10 pts |
| `internal/config` | 2 | ~90% | Medium (4) | 0.4 | ≥90% | 0 pts |
| `cmd/tokenops` | 1 | 0% | Low (1) | 1.0 | ≥30% | 30 pts |

**Overall Health**: 42 / 100 (higher is better — sum of `coverage × risk` / sum of `risk`).

## Priority Queue

1. `internal/daemon` — Critical. Boot sequence composes every subsystem. A regression is silent until deploy.
2. `cmd/tokenopsd` — Critical. Entrypoint parses CLI flags and loads config. Shared with `tokenops start`.
3. `internal/mcp/tools.go` — High. Zero coverage on five tool handlers plus `parseTimeOrDuration`.
4. `internal/storage/sqlite/serialize.go` — High. Envelope ↔ row marshalling is data-fidelity critical.
5. `internal/storage/sqlite/helpers.go` — High. Null-value helpers used by every serialization path.
6. `internal/otlp` — Medium. Attribute mapping tested for PromptEvent only.
7. `internal/config` — Medium. Boolean env-override paths have a small gap.
8. `cmd/tokenops` — Low. Thin wrapper; covered by CLI integration tests.

## Scoring Rubric

| Risk Level | Criteria | Examples |
|---|---|---|
| Critical (10) | Daemon will not start or will silently corrupt data | Boot sequence, entrypoints, persistence layer |
| High (7) | Feature broken but daemon still serves other features | Tool handlers, serialization, attribute mapping |
| Medium (4) | Output degraded or operator experience harmed | Config parsing, formatting, utility functions |
| Low (1) | Thin delegation; defect caught by downstream tests | Entry wrappers, main.go |

## Baseline Workflow

```bash
# Capture baseline
make test
go tool cover -func coverage.out | grep -E '^(github.com.*/daemon|github.com.*/tokenopsd|github.com.*/mcp|github.com.*/sqlite)'

# Re-check debt dashboard after changes
# Update table above and commit alongside test changes
```

## Goal Progression

1. **Current milestone**: cover pure functions in Critical and High packages (resolveCertDir, resolveStoragePath, parseTimeOrDuration, jsonString, envelopeToRow, helpers).
2. **Next milestone**: cover error paths and CLI flag parsing in Critical packages (runStart config error, unknown command).
3. **Future milestone**: cover integration paths in High packages (tool handlers with DB, OTLP multi-type attribute mapping).
