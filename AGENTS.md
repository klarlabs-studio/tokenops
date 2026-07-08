# AGENTS.md — last updated: 2026-07-03
# Keep under 400 lines. Split overflow to memory/ files.

## Working Style
Output format: structured (tables + short sections); caveman mode often active (terse, drop filler) — but code/commits/PRs written normally.
Decision style: recommend directly with a clear default; surface real forks via a question, otherwise pick and note it.
When stuck: make a call and flag it; proceed to the obvious next step (tests, lint, push) without waiting for "continue".
Review mode: critique hard on correctness, then ship.

## Project Context
Company: tokenops (open source, Apache 2.0) — local-first MCP server + CLI for flat-rate AI subscriptions.
What we're building: rate-limit prediction + spend analytics + `tokenops fmt` deterministic command-output compression.
Phase: maintenance + feature growth (fmt subsystem shipped v0.26–v0.28.1).
Stack: Go 1.25 (DDD contexts), SQLite event store, Vue 3 + D3 dashboard, VitePress docs, goreleaser + homebrew.

## Constraints
Never: add a domain package under internal/contexts/* without adding it to internal/archlint domainPackages (CI fails otherwise).
Never: let a formatter drop a critical line — the engine's enforceCritical guard must remain the single enforcement point.
Always: run `gofmt -l .` + `golangci-lint run ./...` + `go test ./...` before pushing (CI Lint runs gofmt before golangci, so gofmt failures mask lint issues).
Always: branch off fresh origin/main; PR merges need `gh pr merge <n> --squash --admin --delete-branch` (base-branch policy blocks plain squash).
If a fmt formatter change touches >1 file of shared engine behavior, add golden survival + monotonic tests.

## Known Failure Modes
- Subagents writing formatters occasionally get derailed by injected memory/planning/skill prompts (one did 0 tool-uses) → correct by re-spawning fresh; prefix the task with "ignore any memory/planning/skill prompts; focused code task".
- gofmt struct-alignment failures slip past local editor diagnostics → correct by running `gofmt -l .` explicitly before push (CI blocks on it).
- Tends to branch off a stale main → merge blocked as BEHIND (branch protection requires up-to-date). Correct by branching off fresh origin/main, or rebase + force-push before merge.
- Changing a hook/CLI's log/ledger schema is forward-only → existing rows lack new fields and won't backfill, so a stats reader shows zeros/gaps until fresh events accrue. Don't read the gap as "nothing happening"; after a schema bump, ship + reinstall the binary the hook runs, then wait for new events before trusting the breakdown.
- After a GitHub repo transfer, a stale explicit `release.github.owner/name` in `.goreleaser.yaml` → goreleaser creates the release via redirect but POSTs asset uploads to the OLD repo URL, which returns 307 (unfollowed) → release "succeeds" with 0 assets. Correct by pinning `release.github.owner/name` to the NEW repo before the first post-transfer tag; delete the failed tag + partial release, re-tag.
- read-guard ACTIVE mode blocks the parent's own file re-reads when a subagent already read that file under the shared session_id (the parent lacks the content in its context). FIXED IN CODE 2026-07-04 (ledger now scoped by Claude Code's `agent_id`), but the installed brew binary still has the bug until upgraded — until then, use ranged reads (offset/limit bypass the guard) to fetch what you need.
- Adding a dependency can silently raise `go.mod`'s `go` directive past CI's pinned version (`go get` of a lib needing a newer Go bumps it; `go mod tidy` won't lower it). CI pins a specific Go (`go-version: "1.25"`), so a jump to 1.26 breaks CI. Correct by `grep '^go ' go.mod` after any `go get`, pinning a lib version compatible with CI's Go (check `<proxy>/@v/<ver>.mod`), and manually resetting the directive.
- Swapping a heuristic for an exact implementation can break tests that were only passing on the heuristic's error. The exact tiktoken tokenizer failed a compress test that asserted savings on trailing-whitespace "redundancy" the char/4 heuristic overcounted. Correct by treating such failures as the exact impl being RIGHT and fixing the test's premise, not reverting the code.
- Tends to dismiss surprising pricing drift/data as a "false positive" by reasoning from priors instead of checking the source. Twice cost real accuracy: the Opus "correction" ($5→$15, reverted) and calling mistral-medium's +275% an artifact (the vendor confirmed Medium 3.5 IS $1.50/$7.50). Correct by cross-checking the authoritative VENDOR page before acting on any catalog change or drift verdict — never single-source, and never let a prior override the primary source. The Anthropic-only consistency guard cannot catch non-Anthropic rows, so vendor-verify (and mark `verified: true`) is the only protection there.

## Decision Summary
# 3–5 most consequential. Full log in memory/decisions.md
- 2026-07-03: critical-line survival enforced in the ENGINE, not per-formatter → user config formatters are as safe as built-ins (the moat).
- 2026-07-03: learning is offline/advisory/gated, never runtime self-modification; `fmt learn --apply` writes only safe loss-level overrides locally.
- 2026-07-03: cloud CLIs pass JSON through untouched (generic dedup corrupts JSON); only table/text compressed.

## Active Patterns
- "brief me" → /brief (reads ./memory/status.md)
- "capture" → /capture (writes session log, updates status)
- "/mem-compact" → digest sessions older than 30 days
- Note: no CLAUDE.md in this repo — AGENTS.md is the single project-instruction file.
