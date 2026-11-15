# ADR 0021 — A compaction setting is judged on the developer's own weeks

- **Status:** Proposed
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

7. **Approval at the `ask` rung is not built yet.** The intended rule is
   that the coach proposes the line and applies it on the developer's yes,
   and that it stays in shadow until the compaction guardrail graduates.
   The current code does not do that: an `ask` context power is advised
   instead, because no agent offers an approval point for compacting (ADR
   0006 and `coach` rung rules). Until that is decided, the setting is
   written only where the context power is `autonomous`, as before this
   change. The derived line changes the number written, not who may write
   it.

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
- The judgement is pure: `internal/capability/compactwatch` reads nothing
  and writes nothing. The weekly series and the caller that feeds it are
  separate work.
