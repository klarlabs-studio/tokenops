# ADR 0003 — Vendor-reported cost outranks recomputed cost

- **Status:** Accepted — **scoped to the team plane** (amended 2026-09-19)
- **Date:** 2026-09-19
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0002 (researched pricing snapshots), `plans.AuthoritativeWindow`,
  `vendorusage/anthropic` (Admin API poller), the spend-denominated Enterprise plan

## Scope: this belongs to the team plane

This decision is sound and deliberately **not current work**. It depends on an
admin key, and an admin key belongs to whoever administers the organization —
IT, finance, a platform team. The operator TokenOps is built for today is an
individual developer, and an individual on an Enterprise account will not be
issued one.

That is a statement about the buyer rather than about the technology. A product
sold top-down to engineering leadership receives admin credentials at rollout as
a matter of course; a product that a developer installs on their own laptop
never does. TokenOps is solo-first and team-ready, and a team tier requires a
hosted plane. **When that plane exists, an administrator deploys it and supplies
the key**, and every step below applies unchanged.

Until then, the individual operator is served by what they can reach without
admin rights:

- the org spend limit they supply themselves (`plan_limits.<provider>.spend_limit_usd`),
  which is the right design for someone who knows their limit and cannot read it
  through an API;
- the Claude usage meter, which authenticates with the operator's own session
  and already stores `extra_usage_current` and `extra_usage_budget` on every
  event. Nothing reads them yet. If an Enterprise account populates them, that is
  the individual's own spend against their own limit, with no admin key at all.

A second finding narrows the design for the team plane itself. The Usage and
Cost API accepts **Claude Console** admin keys only (`sk-ant-admin01-…`). A
**Claude Enterprise** organization on claude.ai issues a different key
(`sk-ant-api01-…`) that reaches the Enterprise Analytics API (`read:analytics`)
and the Spend Limits API (`read:spend_limits`) instead. So `cost_report` serves
Console organizations, and a Claude Enterprise organization needs the Analytics
API for cost and the Spend Limits API for the limit — which the team plane can
read rather than ask for.

## Context

TokenOps computes every dollar figure itself. It takes token counts, multiplies
them by a public rate card (ADR 0002), and reports the product. For a flat-rate
subscription that is the right approach: the vendor never bills per token, so an
API-equivalent computed from list price is the only figure there is.

It is the wrong approach wherever the vendor *does* bill per token and will say
what it billed. Three open problems share that one cause:

1. **Spend-denominated plans compare an estimate against a real limit.**
   Usage-based Enterprise is measured against the org spend limit its admins set
   in the vendor console — a figure the vendor enforces against its own billing.
   TokenOps compares it to list-price maths. Enterprise contracts are frequently
   discounted, so the comparison overstates spend, and it does so invisibly.
   `plan_limits.<provider>.rate_factor` exists only to paper over that gap by
   asking the operator to type in their discount.
2. **Budgets and vendor limits cannot be reconciled.** An operator can set a
   budget in TokenOps and a limit in the console, and nothing compares them. A
   budget whose ceiling sits above the vendor's limit can never fire before the
   vendor cuts traffic off, and nothing says so.
3. **Codex Team and Enterprise have no org-level source.** TokenOps reads Codex's
   per-user transcripts. For an organization there is no account-wide view at
   all.

Both vendors already publish what TokenOps is estimating:

| | endpoint | returns |
|---|---|---|
| Anthropic | `/v1/organizations/cost_report` | cost, by workspace / model |
| OpenAI | `/v1/organization/costs` | cost, "reconciles to your billing invoice" |

Both need an admin key. TokenOps' existing Anthropic admin poller already holds
one — and reads `usage_report/messages`, which returns tokens, then recomputes
the cost it could have been told.

There is a precedent in the codebase for exactly this move. `AuthoritativeWindow`
lets a vendor's own rate-limit percentage outrank the message-count estimate when
one is available. This ADR applies the same rule to money.

## Decision

**When an admin key is present, the vendor's own cost figures outrank recomputed
cost wherever TokenOps reports money.**

1. **Ingest vendor cost.** Extend the Anthropic admin poller to read
   `cost_report`, and add an OpenAI poller for `/organization/costs` with the same
   shape. Store each as its own event type carrying the vendor's figure and its
   bucket, stamped with the source that reported it.
2. **Reconcile, do not replace.** Both endpoints bucket by day, so the vendor
   figure for today does not exist yet. Recomputed cost remains the live estimate;
   when a day's vendor figure arrives it supersedes the estimate for that day.
   Reports state which days are vendor-reported and which are still estimated. A
   figure that is partly stale and presented as current is the failure this
   project exists to remove.
3. **Spend-denominated plans use the vendor figure** when one covers the window,
   and `rate_factor` stops applying to those days — there is nothing left to
   scale. It remains for operators without an admin key.
4. **Budgets are checked against the plan's limit.** A budget whose ceiling is
   above the plan's spend limit is reported as unable to fire.

## Consequences

**Better.** Enterprise spend matches the bill. `rate_factor` becomes a fallback
rather than a requirement. Codex organizations get an account-wide view for the
first time. Budgets that can never fire say so.

**Costs.**

- A second event shape for money, and a reconciliation step wherever spend is
  summed. Summing vendor figures and recomputed figures for the *same* day would
  double-count; the rule is one or the other per day, never both.
- Up to a day of lag on the authoritative figure. This is inherent to the
  endpoints and is stated rather than hidden.
- Admin keys are org-scoped secrets with wider reach than a user key. They are
  read from config or the environment exactly as the existing Anthropic admin key
  is, and are never logged.

**Not decided here.** Seat-based Enterprise — an included per-seat allowance
followed by metered overflow — needs two denominators at once. Vendor cost makes
it tractable, because both halves come off one authoritative number, but the data
model change is its own decision.

## Implementation order

For the team plane, once it exists:

1. Anthropic `cost_report` ingestion (Console organizations). The poller, auth, config and wiring exist;
   this adds an endpoint and an event shape.
2. Per-day reconciliation, and spend-denominated plans reading from it.
3. Budget versus plan-limit check.
4. OpenAI `/organization/costs` poller.
5. Claude Enterprise: the Analytics API for cost and the Spend Limits API for
   the limit, replacing the operator-supplied figure for organizations whose
   administrator has connected the plane.
6. Seat-based Enterprise, under its own ADR.
