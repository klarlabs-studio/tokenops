# ADR 0022 — A change of effort level is judged on the developer's own weeks

- **Status:** Accepted 2026-11-17
- **Date:** 2026-11-17
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0019 (derived figures follow changes), ADR 0020
  (evidence at one developer's volume), ADR 0021 (compaction judged on the
  developer's own weeks)

## Context

A model's price per token does not depend on its reasoning effort, but
the number of tokens does: reasoning is billed as output. The coach
suggests a lower default effort for a model where the developer's own
week shows a lower level doing as well for materially less (`tokenops
explain effort-suggestion`). It advises only; it never writes the
setting.

Whether the lower level then saved anything needs a before and after.
ADR 0021 sets the pattern for a setting that applies to all of one
developer's work at once: their own weekly series, four whole weeks
either side, a drop counted only when larger than their own
week-to-week movement. The effort lever differs in three ways, which is
why it has its own rule.

## Decision

1. **The change is observed, not made.** TokenOps does not write the
   effort setting, so there is no setting date. The change is the first
   week, per client and model, whose dominant level (the level most of
   the week's instructions ran at) differs from the previous counted
   week's, whoever made it: the developer following the suggestion, the
   developer on their own, or a client's new default. The latest change
   is the one judged.

2. **A week counts** when the model had at least 10 instructions in it
   and a list-price cost was read for them. Other weeks are left out of
   both the change and the comparison.

3. **The first week at the new level is on the after side.** The change
   is only known to the week, so the week it shows in is the first week
   after it. A change made mid-week puts some of the old level in the
   first after-week; that dilutes the after side, which errs toward not
   showing a saving.

4. **Four counted weeks either side**, as in ADR 0021. With fewer the
   verdict says how many it is waiting for.

5. **Every figure is shown with its interval.** Cost per instruction,
   first-try, rejected and interrupted rates are weekly means with a 95%
   interval (Student's t over the four weeks), before and after.

6. **A saving is realized** when the cost per instruction fell by more
   than its mean absolute week-to-week change before the change, **and**
   no outcome rate worsened by more than 5 percentage points (the
   suggestion's own margin). Unlike ADR 0021, outcomes need not improve:
   the claim is "as good for less", not "better". An outcome rate that
   worsened beyond the margin is reported as such whatever the cost did.

7. **Prevented spend is an estimate, apart from found waste.** A realized
   change contributes `effort` to the waste report's prevented figures:
   the fall in cost per instruction times the weekly instructions after
   the change, scaled to the part of the period after it, labelled an
   estimate. It is never counted as found waste. A change not judged, or
   judged not realized, counts nothing and says why.

8. **The weekly series is a small stored rollup** (ADR 0019),
   `effort-weeks.json` in the cache: the last 16 finished weeks, each
   computed once from the transcripts and the turns' list prices, and
   again only when a change in the event log touches it or the rollup's
   version changes. It is refreshed after the daily waste report.

9. **The judgement is comparison, not attribution.** It cannot rule out
   other changes in the same weeks (harder work, a new project), and
   every surface says so.

## Consequences

- The verdict shows in `tokenops coach`, the `tokenops_coach` MCP tool
  and the daemon's coach API (`effort`), and the realized saving in the
  waste report's prevented figures.
- A developer who changes level often will rarely get a judgement: each
  change restarts the four weeks after it.
- The judgement is pure (`internal/contexts/learning/effortwatch`); the
  rollup (`internal/capability/effortweeks`) is rebuilt from the
  transcripts and the event log, so dropping it loses nothing.
