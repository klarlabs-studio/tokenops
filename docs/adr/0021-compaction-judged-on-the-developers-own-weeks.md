# ADR 0021 — A compaction setting is judged on the developer's own weeks

- **Status:** Accepted 2026-11-15
- **Date:** 2026-11-15
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0006 (one coach; the context power), ADR 0017 (the price
  of quota), ADR 0019 (derived figures follow changes), ADR 0020 (evidence
  that can conclude at one developer's volume; the randomized holdout for
  model moves), ADR 0018 (one grade)

## Context

TokenOps can set where Claude Code and Codex compact on their own: the
compaction lever writes one number into the agent's settings file and
records what it replaced. The number used to be the configured plan value.
From this change it is the developer's own context line when one has been
measured: the lower edge of the first band of their instructions whose
rejections rise above the lowest band's (the same line the waste report
shows as `context line`).

Whether the setting helped cannot be judged the way model moves are
judged. A model move is randomized: a share of the moves is held back, so
held and moved work are comparable. A compaction setting applies to the
whole of one developer's work at once; there is no held-back group of
sessions that compacts at the old line. A holdout would also be a
deliberate worse setting for part of the developer's work, which is not a
change we want to make to reduce the cost of our own measurement.

A single developer's volume is small. A week of their work is a handful
of sessions, and the figures move with the work itself: a heavy week
carries more context regardless of any setting.

## Decision

1. **The setting is judged on the developer's own weekly series, before
   and after the day it was set.** Two figures are watched, each per week:
   - the context carried above the compaction line (the waste class
     `oversized_context`, in tokens), and
   - the reject rate of the instructions in the bands with enough
     instructions to judge.

2. **A judgement needs four whole weeks before the setting and four
   after it.** With fewer on either side it says how many it is still
   waiting for and does not claim a result.

3. **A saving is realized only when both figures fall by more than the
   baseline's own week-to-week movement.** The movement is the mean
   absolute change between consecutive weeks before the setting. A drop
   that is no bigger than the developer's usual swing is reported as not
   yet shown. A drop in tokens with an unchanged reject rate is not a
   saving: the context may simply have been shorter that week.

4. **The judgement is comparison, not attribution.** It says the
   developer's measures moved further than their own usual movement after
   the setting. It cannot rule out other changes in the same weeks, and
   the verdict says so when it is shown.

5. **Only a realized saving counts as prevented.** A setting that is not
   yet judged, or judged not realized, adds nothing to the prevented
   figures (ADR 0019 applies: the figure is recomputed when the weeks
   change, not stored as a claim).

6. **The lever keeps its rules.** The setting is written only where the
   context power acts; it is reverted to what it replaced; and a value the
   operator set is never changed.

7. **At the `ask` rung the coach proposes the line and writes it on the
   developer's yes.** The proposal is a follow-through approval offer,
   the record the models power already keeps when it asks before a move;
   `tokenops coach accept` is the yes and `tokenops coach decline` the no,
   each recorded as the offer's resolve. A declined line is not proposed
   again; a line that moves is. A proposal waits for its answer: it does
   not lapse with a session.

8. **Shadow until graduation.** Before the developer accepts, and while
   the compaction guardrail has not graduated (its evidence does not yet
   clear ADR 0005's bar to act, the same graduation `tokenops coach stats`
   shows), what would be set is recorded as a shadow in the savings ledger
   and nothing is written. An accepted line is written on the first daily
   step after the guardrail graduates. At `autonomous` the setting is
   written as before.

9. **The weekly series is a small stored rollup (ADR 0019).** After the
   daily waste report the daemon keeps, per finished week (Monday to
   Monday, UTC), the context carried above the line and the reject rate
   of the trusted bands, beside the week's snapshot. Only the weeks the
   judgement needs are kept: the last four finished weeks, and four either
   side of the setting; the week the setting fell in is on neither side.
   A finished week is computed once, from the readers the waste report
   uses, and again only when a change in the event log touches it, the
   line every week is measured against moves, or the rollup's version
   changes. A week with no band trusted enough to judge rejects is left
   out of the comparison. There is no per-day recompute of the report.

10. **A realized saving is a prevented mechanism.** The waste report's
    prevented figures carry it as `compaction`: the weekly drop in context
    carried above the line, scaled to the part of the period after the
    setting, at the period's average API price per token, with its basis.
    It is never counted as found.

## Consequences

- One developer's weeks can conclude: a clear drop larger than the usual
  week-to-week swing is visible after four weeks each side, where a
  randomized comparison would need hundreds of sessions.
- A noisy developer, or one whose work changes sharply, will see "not yet
  shown" more often. That is the honest answer for them, not a failure of
  the setting.
- The figures are the developer's own. They are not shared with a team
  unless the team upload already carries them; nothing about the verdict
  leaves the machine by itself.
- The judgement is pure: `internal/contexts/learning/compactwatch` reads
  nothing and writes nothing. The rollup that feeds it
  (`internal/capability/avoidable`, `compact-weeks.json` in the cache) is
  rebuilt from the log, so dropping it loses nothing.
- Lowering the context power from `ask` or `autonomous` to a rung that
  does not act restores what tokenops set; lowering `autonomous` to `ask`
  keeps it.
