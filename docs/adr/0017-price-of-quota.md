# ADR 0017 — The price of quota: routing weighs plan headroom, not a threshold

- **Status:** Accepted 2026-11-01
- **Date:** 2026-11-01
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0004 (optimize total utility, autonomy ladder), ADR 0006
  (one coach; the subagent hook may rewrite the model), ADR 0007 (model
  policy filters first), PR #824 (randomized holdout for subagent moves),
  PR #826 (per-turn advice reads the plan's pace)

## Context

Subagent routing and the per-turn advice both read the Claude plan's
windows. Until now they read them through a yes/no rule
(`plans.VendorWindow.Pressed`): a window on course to run out at least a
tenth of its length before its reset, or down to 25% when its pace cannot
be judged, moved edits and deep work one tier down. Everything else moved
nothing.

A threshold is hard to explain and wastes value at both ends. A week that
runs out 16 hours early is "not pressed" and keeps every edit on Opus; one
that runs out 17 hours early moves them all, however much the edit would
have gained from Opus. Nothing weighs what the tier up is worth against
the headroom it takes. On a subscription the marginal API price of a
plan-covered turn is zero; what a dearer model really spends is headroom
in a rolling window that resets on its own clock.

The research roadmap (November 2026, recommendation 4) asked for routing
that values headroom directly. No paper or competitor models rolling plan
windows in principled form; the formulation here is inspired by the dual
prices in WISERouter (arXiv 2607.23765) and PILOT (arXiv 2508.21141),
which price a budget constraint and route by quality minus priced cost.

## Decision

1. **Each plan window has a price of quota, λ, from 0 to 1**
   (`plans.VendorWindow.Price`). Once a tenth of the window has gone by
   (the same trust rule as before), the rest of it *wants*
   `wanted = used × remaining ÷ elapsed` points at the pace so far, and

   ```text
   λ = max(0, 1 − left ÷ wanted)
   ```

   It is 0 while the window lasts to its reset, and otherwise the share of
   the remaining time the window would stand empty: monotone in the
   projected overrun (`wanted ÷ left`), and below 1 until nothing is
   left. Earlier in the window, or with no reset known (the daemon's
   figures), `λ = max(0, 1 − left ÷ 33⅓)`. A used-up window is 1. It is a
   closed form rather than a running dual-ascent update so the hooks stay
   stateless and a person can recompute it from the glance.

2. **The binding window prices the plan** (`plans.BindingPrice`): the
   highest λ among the plan's 5-hour and weekly windows, since the plan is
   held to all of them at once. Model-scoped, stale and already-reset
   windows are left out, as before.

3. **Routing maximises expected quality minus priced use.** For the kinds
   a price can move (edits and deep work; lookups and research already sit
   on what they need), the hook weighs the tier the work fits (*upper*)
   against the one below (*lower*). With upper's quality and plan use as
   the unit, `U(upper) − U(lower) = gain − λ × extra`, so the work is
   escalated to upper only when

   ```text
   gain > λ × extra,   extra = 1 − price(lower) ÷ price(upper)
   ```

   *gain* is the share of the work's value lost a tier down: 0.12 for an
   edit, 0.20 for deep work. *extra* is the share of upper's plan use the
   tier below saves, from the rate card, because plan windows meter use at
   about the vendor's API-equivalent rates (0.5 when neither model is
   priced). With Claude's current prices (Sonnet 60% cheaper than Opus,
   Opus half of Fable) an edit steps down past λ 0.2 and deep work past
   λ 0.4. Both subagent routing and the per-turn advice use the same
   weighing (`routeguard.Escalation`), and it never moves more than one
   tier or ever up.

4. **Billed usage keeps its own rule.** A turn with no plan reading has no
   price, and routing goes by the work alone, whose rule (the cheapest
   model that fits) already minimises API dollars for a given quality. No
   cross-plan routing exists in the hooks (they rewrite only Claude Code's
   Agent tool, within one provider), so none is invented: a plan with room
   elsewhere cannot lower the price of a model billed to this one.

5. **The holdout is untouched.** The price changes which move is
   proposed; the randomized holdout (#824) is drawn afterwards, by the
   Agent call's id, and logged with its probability, exactly as before.

6. **Explainable on demand.** The move's reason names the price, its
   window, wanted against left, and the weighing; the follow-through record
   keeps both as evidence (`quota_price: …; escalation: …`), whichever way
   the weighing went. `tokenops explain price` shows the price now and the
   choice for each kind on the configured models, and the glossary term
   `quota-price` explains it to agents through `tokenops_explain`.

7. **The headroom note stays information.** The agent is told what is
   left and when it runs out; it is never a cap, and the price never
   blocks a call.

## Consequences

- Behaviour changes at the edges, on purpose. Mid-window the price is more
  patient than the old margin (a week half gone now needs to stand empty
  for a fifth of what remains before an edit leaves Opus, about 17 hours,
  instead of the old fixed 17 hours from the reset however near it was);
  near the reset a short run-out matters more than it did. Early in a
  window or from the daemon's figures, edits now step down at about 27%
  left and deep work at 20%, against the old flat 25%.
- `plans.WindowPressure` and `Pressed` remain for what still needs a yes/no
  answer: the new-session default model (ADR 0006) and the glance's
  routing hint. Moving them onto the price is a follow-up, not part of
  this decision.
- The gains are defaults, not measurements. The randomized holdout and
  moveverdict judge the moves the price makes, and a kind that does worse
  than its holdout is retracted as before; calibrating the gains from
  those verdicts is the natural next step.
- Ledger evidence for model moves now starts `quota_price:` instead of
  `plan_pressure:`; nothing parses that prefix.
