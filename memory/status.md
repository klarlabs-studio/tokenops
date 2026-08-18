---
updated: 2026-08-18
---
## Current State
tokenops is a local-first MCP server + CLI for flat-rate AI subscriptions. Repo `github.com/klarlabs-studio/tokenops`, module `go.klarlabs.de/tokenops`; brew cask `klarlabs-studio/tap/tokenops` (`brew trust` first). Latest **release in flight: v0.44.0** (changelog PR). `main` has #167: `tokenops daemon install`, `daemon.url` 0600, `tokenops up` copy killed, archlint complete, Gemini 2.5 vendor-verified+pinned, opt-in retention.

Pricing is researched + effective-dated (ADR 0002) with verified-row pinning. **Opus 4.x = $5/$25/$0.50**. **Gemini 2.5 Pro/Flash/Flash-Lite cache-read = 10% of input**.

v0.43.0 made a dead ingestion pipeline **visible**. v0.44.0 makes it **supervisable**. After the tag: brew-upgrade, then `tokenops daemon install` on the operator Mac.

## Last Session Summary
2026-08-18: eval of v0.43.0 → #167 merged → cutting v0.44.0. Prior: 2026-08-03 v0.43.0 (#162/#165/#166) after a 27-day silent ingestion outage.

## Next Session Should
Tag v0.44.0 once the changelog PR merges (goreleaser + brew cask). Then on the Mac: `brew upgrade --cask klarlabs-studio/tap/tokenops` and `tokenops daemon install`. Confirm `tokenops daemon status` and `tokenops vendor-usage status`.

## Blocked / Waiting
- BLOCKED: fmt learn threshold tuning — needs more real usage telemetry.
- WAITING: user to live-verify an OpenAI-compat provider (would flip 9 providers unit→live).
