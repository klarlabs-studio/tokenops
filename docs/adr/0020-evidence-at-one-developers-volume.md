# ADR 0020 — Evidence that can conclude at one developer's volume

- **Status:** Proposed
- **Date:** 2026-11-14
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0004 (learning is outcome-based and retractable; the
  autonomy ladder), ADR 0006 (one coach; the subagent hook rewrites the
  model), ADR 0007 (model policy filters first), ADR 0017 (the price of
  quota; the holdout is drawn after the price), ADR 0019 (derived figures
  follow changes), PR #824 (randomized holdout for subagent moves),
  PR #979 (power-aware move verdict)

## Context

The coach moves subagent work to a cheaper model on its own, and
`moveverdict` judges whether each kind of move (task kind, from-family →
to-family) kept quality. Since PR #979 the judge is honest about power: a
kind that shows no drop reads `not_enough_evidence` until its sample could
have detected a 15-point drop (`detectable_drop`), and only then `holds`.
The models power stays autonomous; that is the maintainer's choice and
this ADR does not change it.

Honest power exposed that, at one developer's volume, the evidence never
concludes. Measured on the maintainer's machine:

- About six subagent model moves a week, 88% of them one kind.
- No held-out run since the holdout shipped, so the randomized comparison
  (`judgeRandomized`, which needs eight held runs) has never engaged. Every
  verdict so far is the observational one, against comparable unmoved
  runs in the same project.
- The 28-day lookback (`moveverdict.Lookback`) holds about 24 moves. At
  the 10% holdout (`config.DefaultHoldoutPercent`, also the cap) that is
  about 2.4 held runs, a third of the eight the randomized comparison
  needs. And `moveverify.Store.Sessions` deletes observations older than
  the lookback, so the held runs that did accumulate would be thrown away
  before they could add up.

The power calculation is the one the judge already uses (`detectableDrop`):
a one-sided two-proportion test at α 0.05 with 80% power,

```text
MDE = (1.645 + 0.842) · √(p(1−p) · (1/n_moved + 1/n_held))
    = 2.487 · √(p(1−p) · (1/n_moved + 1/n_held))
```

where *p* is the failure rate the comparison plans for. Inside one 28-day
window:

| Holdout | Failure rate *p* ≈ 0.1 | *p* ≈ 0.25 |
| --- | --- | --- |
| 10% (2.4 held of 24) | 51 points | 73 points |
| 50% (12 held of 24) | 30 points | 44 points |

The rule retracts at 15 points and calls a kind held only when it could
detect 15. To detect even a 20-point drop at the 10% holdout takes about
155 moves at *p* ≈ 0.1 (about 26 weeks at today's rate), and about 320–375
moves at *p* ≈ 0.25–0.32. No single window will ever hold that many.

Three things make the gap structural rather than a matter of waiting:

1. **Evidence ages out.** The lookback was set so an unbounded history
   would not turn every rate into a measure of how long TokenOps has been
   watching. That is right for the observational comparison, whose
   baseline drifts with the work. It is wrong for randomized evidence:
   chance decided which runs were moved, so held and moved runs from the
   same period stay comparable however old they are, as long as the
   models are the same.
2. **Looking often inflates error.** The Stop hook judges every few
   minutes. A fixed-sample z test applied at every look rejects far more
   often than its nominal 5%. Today that is masked by the small samples;
   once evidence accumulates for months it would not be.
3. **The signal is narrow.** A subagent run counts as bad when, within 30
   minutes (`moveverdict.Settle`), it failed, was re-run, was reworked,
   was interrupted or was rejected. That is quick and local, but it misses
   damage that shows up later or in the parent: a parent that spends more
   tokens fixing the subagent's result, a revert the next day, a CI
   failure after push.

## Decision

1. **Randomized results are kept beyond the lookback, per model
   version.** Held and moved runs of a move whose holdout was drawn
   (`ft.EntryHoldout` and the moves recorded beside them) are kept as a
   running tally per (task kind, from-model id, to-model id), not per
   family. The tally outlives the 28-day lookback; it resets when either
   model id changes (a new snapshot, a new generation), because a new
   model is a new treatment. The observational comparison keeps its
   lookback and its family key: its baseline drifts, so its evidence
   should age. Per ADR 0019 the tally is a projection: it stores the
   counts and the ledger position they reflect, and advances only when
   new observations arrive.

2. **The randomized tally is judged with an always-valid sequential
   test.** The judge looks at every Stop hook, so it uses a method whose
   error rate holds at every look: a mixture sequential probability ratio
   test (mSPRT) or, equivalently for this purpose, a confidence sequence
   on the difference in failure rates. Its two outputs replace the fixed-
   sample z and `detectable_drop` for the randomized tally:
   - the *drop* is retracted as soon as the sequence's lower bound clears
     the 15-point threshold, so a severe drop still retracts early, on few
     runs, without waiting for a planned sample size;
   - the *upper bound* of the drop is reported at every look, so "not
     enough evidence yet" carries the drop the evidence still allows,
     instead of only the drop it could have detected.

   Always-valid bounds are wider than fixed-sample ones at any single
   look; this decision does not make evidence cheaper, it makes judging
   it continuously safe. The volume comes from (1) and (3).

3. **Whole-task outcomes are the primary measure; the 30-minute signal is
   the early warning.** A move is judged by the task it was part of: the
   parent's and the subagent's tokens from the move to the end of the
   task, and delayed outcomes tied to that task (a revert of its changes,
   a CI failure on its commits) where TokenOps can attribute them. The
   existing 30-minute signals stay and keep their power to retract early
   on a clear failure; the task outcome decides whether a kind holds.
   Pairs are pooled as a prior: the same from → to pair across task kinds
   informs each kind's judgment (as the mixing distribution of the mSPRT,
   which shapes power without touching the error guarantee), so a rare
   kind borrows strength from the common one rather than staying unjudged
   for a year.

4. **Autonomy can be gated on the bound.** A move kind may act
   autonomously only while the upper confidence bound of its drop stays
   below a tolerance the operator sets; above it, the kind falls to the
   rung below (`ask`, ADR 0006) until the bound narrows. This is the
   ADR 0004 ladder applied per move kind, from evidence rather than
   config. **The maintainer currently keeps autonomous model moves on**,
   and at today's volume the upper bound stays far above any useful
   tolerance for months, so a default-on gate would in practice turn
   autonomy off. This decision makes the gate available and explainable;
   whether it ships on or off, and at what tolerance, is left to be
   decided and is not part of accepting this ADR.

## Order

1. **Keep the randomized tally (1).** It is the only step that loses
   evidence by waiting: every week without it, held runs are pruned with
   the rest. Store full model ids on observations (today they carry only
   the family) and stop pruning randomized tallies with the lookback.
2. **Judge it sequentially (2).** Replace the fixed-sample verdict for the
   randomized tally only; the observational path keeps the PR #979 rule.
   Golden tests: a known severe drop retracts within a bounded number of
   runs, and simulated null data stays under 5% false retractions across
   thousands of looks.
3. **Whole-task outcomes and pooling (3).** Attributing tokens to task end
   reuses the task the work and story contexts already infer; delayed
   reverts and CI failures follow once their attribution is verified on
   real sessions.
4. **The gate (4), shipped off.** It reports what it would do (shadow,
   per ADR 0004) until its default is decided.

## Consequences

- A kind can, for the first time, conclude at one developer's volume:
  randomized evidence accumulates across months for as long as the models
  stay the same, and the pooled prior lets the common pair inform the
  rare kinds.
- A model release resets the randomized tally for every move that touches
  it. That is the honest cost of a new treatment; the observational
  comparison and the 30-minute signal still guard the move meanwhile.
- The holdout share stays at its 10% cap. A larger share (the 50% row
  above) would conclude faster but withholds more help; raising the cap is
  a separate decision this ADR does not make.
- Sequential bounds are wider at any one look than the fixed-sample test.
  The explanation surfaces (`tokenops explain moves`, the coach verdict
  lines) must say which bound a verdict comes from, and the observational
  and randomized verdicts must stay distinguishable (`Belief.Randomized`).
- Kept tallies are derived state, not evidence: they are rebuilt from the
  ledger and observations, never patched (ADR 0019 §4). Pruning
  observations still applies to their content; the tally keeps counts
  only, no prompts or file names.
- Until (4) is decided, autonomous model moves continue exactly as today,
  retracted only when a drop is found.
