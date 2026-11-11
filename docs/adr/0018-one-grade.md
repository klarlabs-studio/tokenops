# ADR 0018 — One grade: the scorecard grades how the work went

- **Status:** Accepted 2026-11-11
- **Date:** 2026-11-11
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0004 (optimize total utility), ADR 0010 (the daemon
  API serves every surface), review of 2026-11-10 (section 6, "too many
  answers to one question")

## Context

On one day the same operator read three letters: `tokenops dx` said A,
`tokenops checkup` said A (the DX grade again), and `tokenops scorecard`
said C. Each was computed in its own place from its own inputs, and none
said how it related to the others.

The scorecard's letter was the worst of:

- **First-value time (FVT):** the latency of each session's first prompt.
  It was written for the first install, not for a week's work, and it says
  nothing the operator can act on.
- **Token efficiency uplift (TEU):** optimizer savings over input tokens.
  It exists only for traffic through the proxy, and it is already ungraded
  without optimizer events.
- **Spend attribution completeness (SAC):** the share of prompt events
  carrying a workflow, agent or session id. Every transcript reader
  records a session id, so on a machine that reads transcripts it is 100%
  by construction. It is a bar that cannot fail (the class of finding 4.2).
- Cache hit ratio, confirmation gates, rejected output, tool failures and
  destructive commands, all read from the transcripts.

The DX grade, the one built for how sessions go (turns, waiting, rework,
interrupts, first-try success, context growth, compactions), was not part
of it.

## Decision

The scorecard is the one grade, and every surface that shows a grade shows
the scorecard's.

1. **Session quality is a graded KPI of the scorecard.** Its letter is the
   DX overall grade (the worst of the DX metrics, with the metric that
   set it) for the operator's own instructions (`--role primary`). It comes
   from the daemon's session analysis, which covers the last seven days, so
   it is graded on a seven-day scorecard and reported as not measured on
   any other period, with `tokenops dx --days N` named for that period.
2. **FVT and SAC are reported, not graded.** Their values stay on the
   scorecard and in its JSON (`reported: true`), but neither counts toward
   the letter.
3. **TEU stays graded where it is measured**, which is only when the proxy
   carries traffic.
4. **The letter names what set it.** `overall_driver` is the KPI with the
   worst grade.
5. **`dx` and `checkup` show no letter of their own.** `dx` shows each
   metric's letter as before and its overall as the scorecard's session
   grade. `checkup` points at the scorecard.
6. **The scorecard speaks plainly.** No "operator wedge", and no acronyms
   on the lines a reader meets first. The JSON keys stay as they are.

## Consequences

- One letter per period, explained by the KPI that set it.
- The scorecard's letter can now fall for how sessions went, which is what
  an operator can change. It no longer rests on a measure that cannot fail
  or on onboarding latency.
- A scorecard over a period other than seven days has no session grade
  until the session analysis covers other periods.
- JSON consumers gain `sessions`, `overall_driver` and `reported`. No key
  is removed.
