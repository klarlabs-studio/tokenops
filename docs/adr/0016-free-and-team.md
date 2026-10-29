# ADR 0016 — Free and Team: your own work is free, seeing across people is the paid plane

- **Status:** Proposed 2026-10-29
- **Date:** 2026-10-29
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0012 (the team plane: hosted, derived figures only),
  ADR 0003 (authoritative cost needs an admin key), ADR 0004 (local-first),
  ADR 0010 (the daemon API serves every surface)

## Context

TokenOps has two products. One is the open-source client (CLI, daemon,
menu bar, MCP server), which runs on the machine that does the work. The
other is the hosted team plane (ADR 0012), billed per active member per
month and free while it is in beta. What belongs in which has been
decided case by case: authoritative cost went to the team plane because
only an administrator holds the key, and allocation tags stayed local
because no upload field may carry free text. This ADR writes down the
rule behind those decisions so each new feature lands on the right side
without a new debate.

Three constraints already fix most of the answer:

1. **The client is Apache-2.0.** A licence check in it could be removed
   by anyone who builds from source. A paywall on a local feature would
   only inconvenience honest users.
2. **Entitlements stay out of the core domains** (repository guidelines).
   The only place an entitlement can live is the hosted server, which
   already refuses uploads with `402 uploads_paused`.
3. **The privacy promise is about content, not location** (ADR 0012).
   Holding back a local feature would push people towards tools that
   upload transcripts, which works against the reason TokenOps exists.

## Decision

**Free is everything about your own work, on your own machine. Team is
everything that needs more than one person's figures, or a credential
only an organisation holds.**

To place a feature, ask: *can one person compute it from their own
machine's data with their own credentials?* If yes, it is free. If it
needs figures from other members or an organisation's admin key, it is
Team.

### Free (open source, no account, no limits)

- Plan headroom for every provider: windows, pace and forecasts in the
  CLI, the status line, the menu bar and `tokenops_glance`.
- Spend: tracking, personal budgets and alerts, forecasts, plan-fit
  advice for your own plans.
- Allocation rules (project, client, cost centre). A freelancer billing
  clients is one person.
- Export of your own data: the FOCUS dataset (`tokenops spend export`),
  the OTLP exporter, the daemon's HTTP API, the MCP tools.
- Every intervention: coach, read guard, output formatting, routing,
  autopilot, action boundaries, experiments.
- Sessions, task attribution, outcomes, the scorecard, cost per task.
- The team client itself (`tokenops team …`, `pkg/teamwire`), so anyone
  can check what crosses without paying.

### Team (hosted, per active member per month)

- **Rollups across members:** spend, plan use, waste and savings by team,
  repository and kind of work, with the suppression floor of ADR 0012.
- **Plan and seat fit across the organisation:** which members are
  over-provisioned or blocked, and what moving tiers would save.
- **Organisation budgets and alerts:** a budget that spans members.
- **Unit economics across the organisation:** cost per task and per
  outcome by team and repository, built from the per-machine figures
  that free users already see for themselves.
- **Organisation FOCUS dataset:** one chargeback and showback file for
  the whole organisation, by team and repository label. It cannot be
  split by allocation tags, which never leave the machine.
- **Authoritative cost** (ADR 0003): reconciling local estimates with
  what the vendor bills, using the admin key the organisation holds.
- **Administration:** members, roles, grants, per-organisation SSO,
  retention, the audit log and the data-processing agreement.

### Never behind a payment

- Any privacy guarantee, redaction, or the right to see what crosses
  (`tokenops team preview`).
- Exporting your own data in any format TokenOps writes.
- Security fixes and provider readers.
- Figures already uploaded: when billing lapses they stay viewable,
  read-only, for 30 days (ADR 0012).

### Not now

A third tier (self-hosted or bring-your-own-cloud server, retention
beyond the current maximum, contractual SLAs) waits until a buyer asks
for it. Until then the server stays a private service that klarlabs
hosts (ADR 0012).

## Consequences

- A new feature's tier follows from the question above. It is recorded
  in its PR, not settled by a new ADR.
- The client never checks an entitlement. Only the hosted server
  decides, and only for uploads and the views built on them.
- Free must stay complete enough to stand alone. If a feature gets
  weaker for one person because a team version exists, this ADR has
  been broken.
- The team plane's value is the view across people. Its roadmap is
  rollups, reconciliation and administration, never local features held
  back.
