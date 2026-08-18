---
updated: 2026-08-18
---
## [OPEN]
- Actions-minute budget (over 3k): build matrix now push-only (#155). Further cuts if still needed: 1-job linux/amd64 canary on PRs; trim security.yml/nox; consider reducing Test -race scope.
- ADR 0002 Phase 3: additional SOURCES (OpenRouter /models, vendor-page scrape, curated) + optional auto-refresh poller. All-provider LiteLLM source already shipped.
- coach-hook Phase 2+ (ADR 0001): SessionStart spend brief, UserPromptSubmit budget guardrail, PreCompact/SessionEnd wrap-up, weekly scorecard digest, Codex/Cursor Stop parity.
- fmt learn threshold tuning — needs more real command-run telemetry.
- read-guard: ACTIVE mode in ~/.claude/settings.json. Watch `tokenops read-guard stats`; revert to observe if the agent fights a needed block.

## [WAITING]
- 2026-07-04: User to live-verify an OpenAI-compat provider (OpenRouter). Would flip 9 providers from unit-verified to live-verified.

## Resolved
- 2026-08-18: Gemini 2.5 cache-read vendor-verified + pinned (Pro $0.125, Flash $0.03, Flash-Lite $0.01). Gemini 1.5 no longer on Google's pricing page — left unpinned for back-pricing. `tokenops daemon install` + daemon.url 0600 + `tokenops up` copy + archlint completeness + opt-in retention.
- 2026-08-03: 27-day silent ingestion outage made visible (#162/#165) and released as v0.43.0 (#166).
- 2026-07-08: CI path-filter gap — RESOLVED (#154). PRs always run required checks; Test job reports "Test (ubuntu-latest)".
- 2026-07-08: pricing show/diff pin-awareness — DONE (#152).
- 2026-07-08: Snapshot-vs-baseline precedence — RESOLVED via verified-row pinning (#151).
- 2026-07-08: Catalog drift from the all-provider refresh — RESOLVED (#149/#150). Residual diff is LiteLLM staleness, not catalog error.
- 2026-07-07: `pricing refresh` caught the wrong Opus 'correction' → reverted to $5/$25/$0.50 (v0.39.0).
- 2026-07-07: fable-5 rate CONFIRMED — $10/$50/$1.00.
- 2026-07-07: Stale-ingestion health warning — DONE (#131, v0.37.0).
- 2026-07-04: read-guard cross-agent block FIXED; vendor-meter prediction FIXED; tiktoken OpenAI tokenizer; v0.34.0 / v0.35.0 released.
- 2026-07-03: Full provider coverage 4→17; repo transferred to klarlabs-studio.
