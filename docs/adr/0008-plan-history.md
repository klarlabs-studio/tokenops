# ADR 0008 — Plan history, backdated switches, and plan prices

- **Status:** Accepted 2026-09-30
- **Date:** 2026-09-30
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0003 (authoritative cost), `internal/contexts/spend/plans`

## Context

The configuration's `plans:` block holds the plan in force now, and that
is all TokenOps knew about plans.

- **Cost is decided once.** It is stamped when usage is recorded: covered
  by a plan if the provider had any plan bound at that moment, billed per
  token otherwise. Binding a plan later never corrected usage recorded
  without one. On the maintainer's machine, OpenAI had no plan for about
  two hours on 2026-09-26. The 305 calls in that gap still count as $13
  billed, although the subscription covered them.
- **Past periods used today's plan.** A switch from ChatGPT Plus to Pro
  in the middle of a month had no record, so every report used the new
  plan for the whole month.
- **Plans had no prices.** The catalog held rate limits but no prices,
  so TokenOps could not say what the plans cost or how much value they
  returned.

## Decision

1. **Every switch is recorded with the date it takes effect.**
   - `tokenops plan set` and `plan unset` append a binding to
     `~/.tokenops/plan-history.jsonl` (0600).
   - The first recorded switch also records the plan in force before
     it, so earlier time keeps the plan it actually had.
   - A provider with no history keeps its configured plan for all time,
     which is what every report assumed before.
   - `tokenops plan history` lists the switches.

2. **A switch can be backdated.** `plan set <provider> <plan> --since
   <date>` records the plan from that date. It also re-marks the
   provider's usage recorded as billed per token since then as covered by
   the plan, and says how many calls it changed.
   - This is the one exception to "recorded cost never changes". It
     applies only when the operator states they were on the plan all
     along.
   - It is idempotent. It never touches trial usage or other providers.
   - It is written to the audit log (`plan_change`, with the count).
   - Unsetting is never backdated. Re-marking plan-covered usage as
     billed would invent charges the vendor may not have made.

3. **The catalog carries list prices.**
   - Each plan has a monthly price on monthly billing, in US dollars
     before tax. Per-seat plans are marked, and priced for one seat.
   - Each price cites the vendor page it was read from and the date.
   - A plan with no flat price (Enterprise: seats plus usage) has none,
     and reports say its cost is left out rather than showing $0.

4. **`tokenops spend` prices the plans in force over its window.** It
   prorates by day across switches, using the average month length, and
   reports list-price value per plan dollar.

## Consequences

- **Limits still use the plan in force now.** The live rate-limit window
  is measured now, so the current plan is the right one. Per-period limits
  over past windows follow the history only where a report asks for a
  past period.
- **Prices go stale.** Vendors change prices; OpenAI cut the Business seat
  price in April 2026. A price change needs a catalog PR with a refreshed
  source, like a limit change.
- **Prices are US list prices.** Regional pricing, tax and annual
  discounts are not modelled. A value-per-dollar figure is a comparison,
  not an invoice.
