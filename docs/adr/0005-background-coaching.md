# ADR 0005 — Background coaching is a local, heuristic, earned digest

- **Status:** Accepted 2026-09-27, with a build gate (see Decision 1)
- **Date:** 2026-09-27
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0001 (opt-in usage-coaching hooks; Phase 2 SessionStart
  brief, Phase 4 digest), ADR 0004 (loop and authority ladder),
  `internal/contexts/coaching/coaching` (async pipeline),
  `internal/contexts/coaching/waste` (detector), PR #438

## Context

Background coaching has been deferred "until opt-in, replay scope, and cost
policy are explicit". This ADR makes them explicit. Measured on the
maintainer's machine on 2026-09-27:

- **The async pipeline has never run.** `coaching.New`, `NewLLMEnricher`, and
  `RouteBackend` have no production caller, and the event store holds no
  `coaching` envelope among 274,256 events. Waste detection runs only on
  demand: `tokenops replay`, the MCP workflow tools, and the local API.
- **Detection is cheap.** Replay plus detection took 0.1–0.8 s of local CPU
  per workflow. The only money cost in the pipeline is the optional LLM
  enricher.
- **Precision was the blocker, and relevance still is.** Before #438 every
  one of 80 real workflows produced a finding, all measuring session length.
  After #438, 24 of 80 carry a genuine "oversized context" finding and
  nothing else. It is true and recurs, and for an operator who runs large
  contexts by choice it changes nothing.
- **The live channel exists.** The Stop-hook coach (ADR 0001) nudges during
  a session on budget tiers, on Claude Code, Codex, Cursor, and opencode.

Established practice for proactive findings points the same way:

- Google's static-analysis platform (Tricorder, ICSE 2015; "Lessons from
  Building Static Analysis Tools at Google", CACM 2018) holds each check to
  an **effective false-positive rate** under about 10%, where a finding the
  developer does not act on counts as false whether or not it is correct,
  collects "not useful" feedback on every finding, and moved results out of
  dashboards and batch reports into the moment a decision is made.
- The Google SRE book ("Monitoring Distributed Systems") limits alerts to
  what needs a human decision and has a clear response; everything else is
  a number on a dashboard.
- Clinical decision support shows the cost of ignoring this: override rates
  of 49–96% for drug-interaction alerts (van der Sijs et al., 2006), until
  alerts were tiered, deduplicated, and made acknowledgeable.
- Calm technology (Weiser & Brown, 1995; Case, 2015) puts informational
  signals in the periphery and makes silence the normal state.

## Decision

Background coaching is an **opt-in, local, heuristic digest of completed
sessions that reports only findings that have earned a place in it**. It
never calls a model, never interrupts, and never re-analyses history on its
own.

1. **Build gate.** Implementation starts only when at least **three finding
   kinds** meet the promotion bar in Decision 6. Until then coaching stays on
   demand plus the live hooks, and detector work comes first: findings that
   name a cause and a dollar amount (which work drove the growth, sessions
   running long without compaction, repeated identical tool calls) rather
   than a symptom.
2. **Opt-in.** `coaching.background.enabled` defaults to `false`.
3. **Scope: completed sessions, once each.** A session is complete when the
   client reports its end (Claude Code `SessionEnd`, opencode `session.idle`)
   or, where a client has no such event, after `coaching.background.idle` of
   inactivity (default **2h**: an early analysis sees half a session, a late
   one costs nothing). Each completed session is analysed once; the finding
   envelope's ID is deterministic in (workflow, kind), so the store's ID
   dedup is the watermark and a restart does not re-report. Sessions that
   completed before background coaching was enabled are not analysed unless
   the operator asks with `coaching.background.backfill` (a duration,
   default `0`).
4. **Cost: zero dollars by construction.** The background pass is heuristic
   only. The LLM enricher is not wired to it; rewriting a finding with a
   model stays an explicit, on-demand choice, so text derived from the
   operator's sessions never leaves the machine on a schedule.
5. **Digest content: new or worse, costed, few.**
   - Compared against the operator's own **trailing 7 days**, not the
     previous digest: digests are pulled at irregular intervals, so a
     per-digest baseline is noise.
   - Only findings that are **new or worsening** against that baseline
     appear. A steady state the operator already knows is not news.
   - Each item states its **dollar impact** and **one concrete action**.
   - At most the **top three by dollar impact**; the rest stay available on
     demand.
   - When nothing qualifies the digest says so in one line.
6. **Promotion bar: effective false-positive rate.** A finding kind enters
   the digest only after review on real sessions shows it is correct **and**
   acted on: after it is surfaced, the finding's rate in the operator's
   following sessions falls, or the operator confirms the change. Kinds that
   stay above about a 10% effective false-positive rate are demoted to
   on-demand. Promotion and demotion are reviewed changes with evidence, like
   #438.
7. **Acknowledge and mute.** The operator can acknowledge a finding kind
   ("I run 1M contexts on purpose"). A muted kind leaves the digest until its
   rate changes materially from the acknowledged level. Mutes are local
   config, visible in `tokenops status`, and every mute is recorded as a
   `not useful` signal against the kind's promotion evidence.
8. **Delivery follows `coaching.delivery`, at the point of decision.**
   - `observe`: findings are stored; nothing is surfaced.
   - `advise` and `intervene`: the preferred moment is the **start of the
     next session** in the same project, through the session-start hook
     where the client has one (ADR 0001 Phase 2). The digest is also one
     line in the operator's pull surfaces (`tokenops status`). Nothing
     interrupts a session; that remains the live hooks' job.
9. **The agent is not the audience by default.** `tokenops_status`,
   `tokenops_resource_glance`, and session-start hooks feed agents, and a
   digest there would steer agent behaviour. Agent-facing surfaces carry the
   digest only when `coaching.background.agent_visible` is `true` (default
   `false`); operator surfaces carry it whenever delivery is `advise` or
   `intervene`.
10. **The unused async pipeline is removed.** Its job queue, worker pool,
    per-run dollar budget, and model router serve work this decision rules
    out. The implementation replaces it with a small scheduled pass in the
    daemon's lifecycle modules. Until the build gate opens, the unused code
    may be deleted on its own.
11. **Verification.** The digest records which kinds it surfaced and when.
    Whether their rate falls in the following sessions is the outcome
    evidence ADR 0004 asks of every intervention, and it is the same
    evidence the promotion bar reads.

## Alternatives considered

- **Keep on-demand only.** Honest and free; this remains the behaviour until
  the build gate opens.
- **Build now with the one qualifying kind.** Rejected: the digest would
  repeat "oversized context" to an operator who chose it, which is the
  pattern the practice above warns trains people to ignore the channel.
- **Wire the existing pipeline as is.** Rejected: no completion or dedup
  notion, a dollar budget for model calls this decision excludes.
- **LLM-enriched background findings.** Rejected for the background pass:
  unapproved recurring spend and derived text leaving the machine on a
  timer. Remains available on demand.
- **Per-session proactive nudges.** Rejected: they would fire on about a
  third of sessions for a known condition; the live Stop-hook covers the
  in-session moment.
- **A fixed idle window of 30 minutes.** Rejected: long builds and long
  pauses would be analysed mid-session.
- **Digest visible to agents by default.** Rejected: it would steer agents
  through a channel nobody approved.

## Consequences

- Near term: detector work toward cause-and-cost findings, and deletion of
  the unused async pipeline, are the next coaching tasks. No background
  scheduler is built yet.
- When the gate opens: a `coaching.background` config block (`enabled`,
  `idle`, `backfill`, `agent_visible`) validated at load and covered by the
  config matrix; per-kind acknowledge and mute; `coaching` envelopes under
  retention's existing `coaching` key; a session-start delivery path per
  client that has one.
- Every finding kind carries promotion evidence (correctness on real
  sessions, action rate, mutes), so the digest's contents stay explainable.

## Execution notes

- **2026-09-28, finding kind 1 of 3: `compact_earlier`.** A session that
  ran 20+ steps in a row above a compaction line (600k on Claude Code,
  150k on Codex) without compacting, priced against compacting at the line
  (a sawtooth averaging halfway between the session's post-compaction size
  and the line) at API rates. Cause and cost are both named, and the action
  is one step. The 600k line is where the maintainer's measured rate of
  repeated identical tool calls rises (1.7% below, 2.6% above, z=9.0), so
  the finding carries a quality reason as well as a cost one. Measured on
  30 days of real sessions: 27 of 1,606 Claude Code workflows,
  ~$6.1k API-equivalent in total, largest single session ~$1.1k.
  **Correctness** holds by construction. **Action rate** is unmeasured: it
  needs delivery, and the follow-through ledger (ADR 0006) is where it will
  be recorded. The two remaining candidates, what drove the growth and
  repeated identical tool calls, need transcript content the event store
  does not hold.
