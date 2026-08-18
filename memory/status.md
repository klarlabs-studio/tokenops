---
updated: 2026-08-18
---
## Current State
tokenops is a local-first MCP server + CLI for flat-rate AI subscriptions (rate-limit prediction, spend analytics, `tokenops fmt`). Repo `github.com/klarlabs-studio/tokenops`, module `go.klarlabs.de/tokenops`; brew cask `klarlabs-studio/tap/tokenops` (`brew trust` first). Latest **release v0.43.0** (2026-08-03). **Unreleased on this branch:** `tokenops daemon install` (launchd/systemd — the 27-day outage class), `daemon.url` 0600, `tokenops up` copy killed, archlint complete, Gemini 2.5 vendor-verified+pinned, opt-in retention wired.

Pricing is researched + effective-dated (ADR 0002) with verified-row pinning. **Opus 4.x = $5/$25/$0.50**. **Gemini 2.5 Pro/Flash/Flash-Lite cache-read = 10% of input** (Google pricing page 2026-08-18; old catalog $0.31/$0.075 was the retired explicit-cache figure). Gemini 1.5 no longer on the vendor page — unpinned, historical only.

v0.43.0 made a dead ingestion pipeline **visible** (SilentFor + measurement.trusted=false + missing-daemon warning). This branch makes it **supervisable**. `tokenops serve` still does not ingest.

## Last Session Summary
2026-08-18: evaluation of v0.43.0/main, then fixed the findings (daemon install, 0600 hint, stale `tokenops up`, archlint, Gemini pin, retention opt-in, docs/Pages/SECURITY). Prior: 2026-08-03 shipped v0.43.0 (#162/#165/#166) after a 27-day silent ingestion outage. 2026-07-08: pricing arc v0.41.0 + CI hardening (#154/#155). Detail in `memory/sessions/`.

## Next Session Should
Ship this branch (tests + PR). After merge: brew-upgrade, `tokenops daemon install` on the operator Mac so the next reboot does not freeze the store. Optional: extra pricing sources (ADR 0002 Phase 3), coach-hook Phase 2, fmt learn once more telemetry exists.

## Blocked / Waiting
- BLOCKED: fmt learn threshold tuning — needs more real usage telemetry.
- WAITING: user to live-verify an OpenAI-compat provider (would flip 9 providers unit→live).
