# ADR 0005 — Background coaching is a local, heuristic digest

- **Status:** Proposed
- **Date:** 2026-09-27
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0001 (opt-in usage-coaching hooks, Phase 4 digest), ADR 0004
  (loop and authority ladder), `internal/contexts/coaching/coaching` (async
  pipeline), `internal/contexts/coaching/waste` (detector), PR #438

## Context

Background coaching has been deferred "until opt-in, replay scope, and cost
policy are explicit". This ADR makes them explicit. Measured on the
maintainer's machine on 2026-09-27:

- **The async pipeline has never run.** `coaching.New`, `NewLLMEnricher`, and
  `RouteBackend` have no production caller, and the event store holds no
  `coaching` envelope among 274,256 events. Waste detection runs only on
  demand: `tokenops replay`, the MCP workflow tools, and the local API.
- **Detection is cheap.** Replay plus detection took 0.1–0.8 s of local CPU
  per workflow; the 15 busiest workflows of a week took 3 s together. The
  only money cost in the pipeline is the optional LLM enricher, which calls
  the operator's model to rewrite each finding.
- **Precision was the real blocker.** Before #438 every one of 80 real
  workflows produced a finding, all of them measuring session length rather
  than behaviour. After #438, 24 of 80 carry a genuine "oversized context"
  finding and nothing else. That is real, but it recurs: a proactive channel
  that reported it per session would nag on almost a third of sessions.
- **The live channel exists.** The Stop-hook coach (ADR 0001) already nudges
  during a session on budget tiers, on Claude Code, Codex, Cursor, and
  opencode.

## Decision

Background coaching is an **opt-in, local, heuristic digest of completed
sessions**. It never calls a model, never interrupts, and never re-analyses
history on its own.

1. **Opt-in.** `coaching.background.enabled` defaults to `false`. Nothing runs
   until the operator turns it on.
2. **Scope: completed sessions, once each.** A session is complete when it
   has had no new turn for `coaching.background.idle` (default 30m). Each
   completed session is analysed once; the finding envelope's ID is
   deterministic in (workflow, kind), so the store's ID dedup is the
   watermark and a restart does not re-report. Sessions that completed
   before background coaching was enabled are not analysed unless the
   operator asks with `coaching.background.backfill` (a duration, default
   `0`).
3. **Cost: zero dollars by construction.** The background pass is heuristic
   only. The LLM enricher is not wired to it; rewriting a finding with a
   model stays an explicit, on-demand choice. This also keeps finding text,
   which is derived from the operator's sessions, from leaving the machine
   on a schedule.
4. **Delivery follows `coaching.delivery`, and is pull-based.**
   - `observe`: findings are stored and nothing is surfaced.
   - `advise`: surfaces that already summarise attention (`tokenops status`,
     `tokenops_status`, `tokenops_resource_glance`) show a digest: finding
     kinds with their session counts and trend since the last digest, not a
     per-session list.
   - `intervene`: same as `advise`. Interrupting belongs to the live hooks;
     a finding about a session that has ended has nothing left to interrupt.
5. **Only reviewed kinds are delivered.** A finding kind reaches the digest
   only once its precision on real sessions has been reviewed (as #438 did).
   Unreviewed kinds are stored under `observe` semantics. Initially only
   the oversized-context `trim_context` finding qualifies: it is the one
   kind that fired on the 80-workflow sample and was checked against real
   peaks. `reuse_cache` (identical prompt repeated) never fired there, so
   its precision is unknown until it does.
6. **The unused async pipeline is replaced, not wired.** Its job queue,
   worker pool, per-run dollar budget, and model router exist for work this
   decision rules out. The implementation replaces it with a small scheduled
   pass in the daemon's lifecycle modules and deletes the unused code.
7. **Verification.** A digest that changes nothing is noise with a schedule.
   The digest records which kinds it surfaced; the next digests measure
   whether their session rate falls, which is the outcome evidence ADR 0004
   asks every intervention to earn.

## Alternatives considered

- **Keep on-demand only.** Honest and free, and the default until this is
  accepted. It leaves recurring waste invisible unless someone asks.
- **Wire the existing pipeline as is.** Rejected: it defaults to replaying
  whatever is submitted, carries a dollar budget for LLM calls this decision
  excludes, and has no completion or dedup notion, so a restart would repeat
  its findings.
- **LLM-enriched background findings.** Rejected for the background pass:
  recurring model spend nobody approved per call, and derived session text
  leaving the machine on a timer. Remains available on demand.
- **Per-session proactive nudges.** Rejected: at the measured rate they would
  fire on about a third of sessions for the same known condition. The live
  Stop-hook already covers the in-session moment.
- **Full-history analysis on first enable.** Rejected as a default: a
  surprise CPU burst and a flood of stale findings. Available through
  `backfill`.

## Consequences

- A new config block, `coaching.background` (`enabled`, `idle`, `backfill`),
  validated at load and covered by the config matrix.
- `coaching` envelopes start to exist; retention's existing `coaching` key
  governs them.
- The `advise` digest adds one line to existing attention surfaces rather
  than a new surface.
- `internal/contexts/coaching/coaching` shrinks to what the scheduled pass
  needs; the LLM enricher and router move to the on-demand path or are
  removed if nothing on-demand uses them.
- Promotion of a finding kind into the digest is a reviewed change with
  real-session evidence, like #438.

## Open questions for acceptance

- Is 30 minutes of inactivity the right completion signal for agent sessions
  that pause for long builds, or should completion also accept a session-end
  hook where the client has one?
- Should the digest's trend compare against the previous digest or a fixed
  trailing window (for example, the prior seven days)?
