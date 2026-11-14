# ADR 0019 — Derived figures follow changes, not the clock

- **Status:** Accepted 2026-11-13
- **Date:** 2026-11-13
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0010 (the daemon API serves every surface), ADR 0012
  (team plane), PR #967 (headroom reads share one suffix), PR #968 (latest
  event per source by index)

## Context

The daemon is meant to sit idle in the background, yet on a real machine
it keeps about a tenth of a core busy around the clock. A CPU profile of
a running daemon (eight minutes, local `TOKENOPS_PPROF_ADDR`) and its log
show where it goes:

| Work | Share of profiled CPU | Cadence |
| --- | --- | --- |
| Team upload (`teamshare.Build`) | ~40% direct, most of the GC | hourly |
| Garbage collection | ~35% | follows allocation |
| Hourly anomaly read | ~8% | hourly |
| Source freshness snapshot | ~4% | per refresh |
| Boundary figures (headroom) | ~3.5% | every minute |
| Transcript and usage pollers | ~3% | 30 s to 15 min |

The team upload alone made 55% of all allocations since start, 75% with
the session units it reads. Its log shows the pattern clearly: between
02:00 and 07:00, with nobody working, each hourly upload rebuilt the same
two days of rows from raw events and transcripts, and each took six to
nine minutes at background priority. Nothing it sent had changed.

That is the shape of every expensive job here. The event store
(`events.db`) is already an append-only log, and `events_changes`
(migration 7) already records every update and delete; `EventCache` uses
both to read only what changed. But every *derived* figure — team rows,
anomalies, the glance, headroom — is recomputed from raw events on a
timer, whether or not any event arrived, and each recompute decodes and
walks weeks of events.

The pollers, which we suspected first, are a small share. Moving them to
file-change notifications is not where the power goes.

## Decision

1. **Derived figures are projections of the event log.** A figure that
   is computed from events is kept as a stored result, together with the
   point in the log it reflects: the newest rowid and the last
   `events_changes` sequence, plus a key for any other input (the config,
   the transcript files' sizes and modification times, the projection's
   own version).

2. **A job runs when its inputs changed, not when a timer fires.** Each
   periodic job first compares its inputs' position with the one its last
   result reflects. Unchanged inputs mean no work: no read, no decode, no
   upload. The timer stays as the latest moment a check happens, not as
   the trigger for a full recompute.

3. **Figures that depend on the clock alone are cheap to refresh.** Some
   figures move with time even when no event arrives: a window resets, a
   pace changes. These are recomputed from small rollups (per day, or per
   time bucket inside a plan window), never from raw events. A rolling
   window becomes a sum over a few hundred buckets.

4. **Projections are rebuilt, never patched by hand.** Each projection
   carries a version. A change to how a figure is computed (a pricing
   change, a correction such as counting Codex reasoning tokens once)
   bumps the version, and the projection is rebuilt from the log. Late
   events and corrections arrive through `events_changes`, so a bucket
   accepts events out of order and recomputes the days they touch.

5. **Every projection is checked against the full recompute.** Tests
   feed the same events to the projection and to the existing
   from-scratch computation and require the same answer, as the verify
   comparison does today. The from-scratch path stays as the reference.

6. **No framework.** This is a stored result and an input position per
   job, and a few rollup tables in the existing SQLite store, all inside
   the daemon. No message bus, no separate process, no general CQRS or
   event-sourcing library.

## Order

Measured cost decides the order:

1. **Team upload:** skip an upload whose inputs have not moved since the
   last one the server accepted; then keep per-day rows and rebuild only
   the days a change touched.
2. **Anomalies:** the same input check before the hourly read.
3. **Glance and headroom:** bucketed window rollups, and a glance snapshot
   that is marked stale by a change in the log or a window boundary rather
   than by its age alone.
4. **Pollers:** file-change notifications only if a later profile shows
   them mattering.

## Consequences

- An idle machine does close to no work: the jobs check a position and
  stop.
- Derived figures can lag the log by one check interval. The glance already
  carries the time it was computed for; any surface that shows a stored
  figure must carry that time too.
- There are now two ways each figure is computed. The parity tests in
  decision 5 are what keep them equal; a projection without one does not
  ship.
- The store grows by a few small tables. They hold derived numbers only,
  and dropping them loses nothing: they are rebuilt from the log.
