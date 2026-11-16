# ADR 0012 — The team plane: hosted, derived figures only, aggregate by default

- **Status:** Accepted 2026-10-08; amended 2026-10-10: the server is a private service klarlabs hosts; amended again 2026-10-10: self-serve sign-up, administration and billing; amended 2026-10-17: upload schema 2 (plan use, waste, savings, plan advice) and its negotiation; amended 2026-10-28: beta billing (free while the Paddle billing is not yet configured), see *Beta billing*; amended 2026-10-30: upload schema 3 (spend by provider, units of work, vendor cost), see *Upload schema 3*; amended 2026-11-01: upload schema 4 (reverted commits on the units), see *Upload schema 4*; amended 2026-11-10: an upload with a part unread is not sent, see *An upload is sent whole* under *Upload schema 2*; amended 2026-11-15: upload schema 5 (what was prevented, per finished week and mechanism), see *Upload schema 5*; amended 2026-11-16: upload schema 6 (seat value), see *Upload schema 6*.
- **Date:** 2026-10-08
- **Deciders:** TokenOps maintainers
- **Related:** issue #250, ADR 0004 (local-first; no BI product), ADR 0010 §5 (the API's privacy line)

## Where this ADR lives now

The team plane is a paid service klarlabs hosts. Its server — the
service, its store, its deployment and the full design record (data model,
suppression construction, authentication, threat model) — moved to a
private repository on 2026-10-10 and is no longer published: no source,
no binaries, no image. The full ADR 0012 lives there. None of it was ever
in a release; this repository's history is not rewritten.

What stays here, open source, is everything that runs on a member's
machine: `tokenops team create|join|status|preview|sync|web|leave|admin`, the daemon's
uploader, `internal/capability/teamshare` (which builds an upload),
`internal/infra/teamclient` (which sends it), the `team` configuration —
and `pkg/teamwire`, the upload contract the server is built against. That
split is deliberate: a member must be able to check what leaves their
machine without trusting the server, so the boundary below is documented
and enforced here, in public.

There is no default server: `tokenops team join <url> <invite>` takes the
address from the invite, and `tokenops team create --server <url>` takes
it from the person starting a team. Nothing is sent until a machine joins.

## Self-serve (amended 2026-10-10)

An organisation is started and run without klarlabs' involvement:

- **Sign-up** is "Sign in with GitHub" in the browser — the server reads
  the account's verified primary e-mail address and its display name,
  nothing else — or an e-mail address and a password (argon2id at rest,
  at least 12 characters, optionally checked against breached passwords
  by k-anonymity prefix only), usable only once the address is confirmed
  by a single-use link; optional two-factor (TOTP) for password accounts.
  An address belongs to one account; a second way to sign in is added
  only from inside the signed-in account, never merged automatically.
  Someone with no organisation creates one and becomes its owner.
- **Linking a machine** (`tokenops team create`, `tokenops team admin
  login`) is a device authorization grant (RFC 8628): the CLI shows a
  short code, the person types it into a page they are signed in to, and
  the CLI polls until they approve. The code is never part of a link, so a
  link someone else sends cannot approve their machine. The machine's
  polling secret, the device token and the administrator's credential are
  random and stored by the server only as hashes; the administrator's
  credential expires and is kept in `team-admin.json` beside the
  enrolment, mode 0600.
- **Administration** — teams, invites with a role, members, roles,
  grants, billing and the audit log — is the owners' and admins' own, in
  the web view and with `tokenops team admin …` on the same API; every
  change is audited. The administration messages (`pkg/teamwire`, admin
  half) carry names, e-mail addresses and a grant's reason, never
  figures: `TestAdminMessagesCarryNoFigures` fails if one reaches an
  upload's types.
- **Billing.** A new organisation gets a 14-day free trial, then a
  subscription per active member per month through Paddle, the merchant of
  record (it holds payment details; the server holds Paddle's customer and
  subscription IDs). Without an active trial or subscription, uploads are
  refused with `402` and `code: uploads_paused`, which the client explains;
  the figures held stay viewable, read-only, for 30 days and are then
  deleted. Weekly totals appear three days after a week ends, and the web
  view and `tokenops team status` say when the first one will. While the
  beta is on (see *Beta billing*), the trial is replaced by the `beta`
  state, and the rest of this bullet applies from the end of the beta.
- **Personal data** the server now also stores: members' e-mail
  addresses and names from sign-up, password hashes and encrypted
  two-factor secrets for password accounts, billing identifiers, and the audit
  log of administration. Transactional e-mail (address confirmation,
  password reset, trial and billing notices) is sent through a processor
  under the same Art. 28 terms.

## Beta billing (amended 2026-10-28)

While the hosted plane is in beta, billing runs in a `beta` mode, a
server setting that is on by default:

- A new organisation is created in the `beta` billing state. It has no
  trial clock, no checkout, and is not billed. Uploads are never paused for
  billing, and the figures are kept and shown as in any other state.
- When the mode is switched on, every organisation still in its `trialing`
  state moves to `beta`. Each move is written to the audit log.
- Switching the mode off restores the 14-day trial for new organisations.

The reason is that Paddle, the merchant of record, is not yet configured
for production, so a subscription cannot be taken or charged. The team plane
is therefore free during its beta, and that is said plainly to members:
`tokenops team status` and `tokenops team admin billing` show "Free during
the beta" and suggest no checkout. The public client knows the plan status
`beta` (`pkg/teamwire.PlanBeta`).

Ending the beta requires two things, in this order:

1. Paddle in production, with the subscription per active member per month
   from *Billing* above working end to end.
2. Every beta organisation moved onto a trial or a subscription, with notice
   to its owners first. No organisation loses uploads, or is charged, without
   that notice having been sent and the change being visible to it in advance.

Until both are done, the beta state stays and this ADR's *Billing* bullet
describes the end state, not the current one.

## Context

TokenOps is local-first: every figure is computed on the machine that did
the work. A team wants the same figures rolled up across people — by team,
by repository, by kind of work. Three decisions were taken on 2026-09-15:

1. A team tier **requires a hosted plane**.
2. **Only derived metrics cross the boundary**: counts, durations, token
   counts, costs. Prompts, file contents, transcripts, paths, commit
   messages and model outputs never leave the machine.
3. **Aggregate by team, repository and kind of work by default**; drill-down
   to an individual is gated. In an EU works-council context this is a
   precondition: § 87 (1) 6 BetrVG makes any system suited to monitoring
   employees' performance subject to co-determination, and GDPR Art. 5
   requires data minimisation.

## The boundary: a type that cannot carry content

What crosses is `pkg/teamwire.Upload` (schema 2 adds the sections listed
under *Upload schema 2* below): per **UTC day × repository label ×
kind of work**, sums only — sessions, instructions, turns, tool calls,
seconds spent waiting on the agent, first-try / reworked / interrupted /
escalated / rejected counts, tokens, billed cost, API-equivalent value and
unpriced turns.

The privacy claim is enforced by structure, in three layers:

- **The type.** Every string in an upload is an enumeration (kind of work),
  a date, a UUID, a version label, or a repository label matched against
  `^name$|^owner/name$` over `[A-Za-z0-9._-]`, at most 64 characters a
  segment: no whitespace, no leading slash, at most one slash.
  `TestUploadHasNoFreeText` walks the type by reflection and fails on any
  field that is a string without such a rule, a map, an interface or a byte
  slice. Widening the boundary therefore takes a failing test, an entry in
  its allow-list, and an amendment to this ADR, made here in public.
- **The builder.** `internal/capability/teamshare` reads instruction text at
  scan time only to classify its kind of work (`taskclass.KindForTurn`), and
  session directories only to find the repository, reduced to its origin's
  `owner/name` (or directory name, or `hidden`, per `team.repo_names`). A
  canary test builds an upload from a machine full of prompts, secrets,
  paths, file names, model names and session IDs and asserts none appear in
  the JSON.
- **The server.** Ingestion decodes with unknown fields refused and runs
  the same `Validate` from this repository's `pkg/teamwire`, which it
  imports by version; its database repeats the label pattern and the kind
  enumeration as constraints.

`tokenops team preview --json` prints an upload byte for byte as it is sent,
so a member, or a works council, can check the claim instead of trusting
it.

Model names, session IDs, branch names and commit SHAs are deliberately not
in the upload: they add identification power without adding a team-level
question the counts cannot answer.

## Upload schema 2 (amended 2026-10-17)

Schema 2 (#685) adds four sections beside the buckets, each a set of
figures the machine already computes for its own surfaces, filed under
the same keys and validated the same way:

- **`plan_days`** — per UTC day × provider × plan: utilization (the mean
  peak of the plan's longest window kind among the windows that reset that
  day) and the number of those windows; coverage (the share of the
  provider's tokens the plan carried) with plan and billed tokens; the
  windows that ran out that day and the minutes blocked within it (#657).
- **`waste`** — per UTC day × repository label × kind of work × waste
  class: turns, tokens and API-equivalent value attributed to one of the
  six classes of the waste taxonomy, each token to at most one (#655).
- **`savings`** — per UTC day: interventions and holdout tips, re-reads
  refused with their tokens and estimated value, the holdout's measured
  saving (absent until a comparison concludes), the realized total and
  what stayed avoidable, so the share saved is taken after adding (#656).
- **`plan_advice`** — per provider: the plan in force, the verdict, the
  option kind and recommended tier, the monthly difference, lockouts and
  blocked minutes on the current and recommended tier, the confidence,
  the days replayed and the windows measured (#653). Not per day: each
  schema 2 upload replaces the device's advice whole.

Every new string is an enumeration: a provider in the provider registry,
a plan in its catalog (a provider's own plan, or `pay-as-you-go` or
`subscription`), a waste class, a verdict, an option kind or a
confidence. The advice's summary and caveats are prose and stay on the
machine; so do allocation tags (#658), which no field can hold. Every
figure is a non-negative sum, a share between 0 and 100, or a bounded
amount (the realized saving and the monthly difference may be negative).
`TestUploadHasNoFreeText` covers the new types; an optional figure is a
pointer to a number, and a flag is a boolean.

Day replacement is unchanged: an upload replaces each day in `days`
whole, the new sections included. A covered day with no row in a section
had none.

**An upload is sent whole (amended 2026-11-10).** Because a day is
replaced whole, an upload whose figures are incomplete erases what the
server held. So the client sends an upload only when every part of it
was read: the transcripts, the event store, the plan events and history,
the waste report, the units of work and each configured vendor's cost.
When one fails (a locked store, a read that ran out of time, a vendor's
API that refused) nothing is sent, the server keeps the days as they
were, `tokenops team status` and the daemon's log name the part, and the
next upload reads the same days again. `tokenops team preview` shows what
was read and says it would not be sent. A row that fails validation is
still dropped with a warning and the rest sent: it is missing from every
upload alike.

**Negotiation.** `Me` gains `max_schema`, the newest upload format a
server accepts. A server that omits it reads schema 1. The client asks
`Me` at most once a day (the answer is kept in the enrolment file, and
`tokenops team status` refreshes it), and again after a schema 2 upload
is refused with a 4xx. Below 2 it sends schema 1 exactly as before — the
new sections are omitted when empty, so a schema 1 upload has no key an
older server, decoding with unknown fields refused, does not know — and
does not compute the new sections at all. A server on this package's
`Validate` accepts both schemas and refuses a schema 1 upload that
carries any of the new sections.

## Upload schema 3 (amended 2026-10-30)

ADR 0016 puts three things on the team plane that schema 2 cannot carry:
the organisation's FOCUS dataset, unit economics across members, and
reconciling the estimate with what the vendor billed (ADR 0003). Schema
3 adds three sections, sent only to a server whose `max_schema` is at
least 3 and computed only then:

- **`spend`** — per UTC day × repository label × provider: turns, tokens,
  unpriced turns, the estimated bill (usage a plan covered bills nothing)
  and the API-equivalent value. The same turns as the day's buckets,
  split by provider rather than by kind of work. **No model name**: the
  organisation's dataset is by provider.
- **`units`** — per UTC day × repository label: the inferred tasks that
  started that day with recorded usage and their value (`tokenops story
  --runtime`), and the commits and merged pull requests the member's work
  was placed on, with the value placed on them (`tokenops spend units`).
  A commit's subject is read only to join it; it never leaves.
- **`vendor_cost`** — per UTC day × provider: what the vendor's cost API
  reports the organisation was billed (Anthropic `cost_report`, OpenAI
  `organization/costs`). It is read and sent only on the machine of an
  owner or admin (`Me.role`) that has an admin key configured, and only
  when the upload is built. **The admin key never leaves that machine**:
  the hosted server holds no organisation-wide credential. A server drops
  the section from anyone else's upload and says so
  (`vendor_cost_ignored`). The client then asks `Me` again before the
  next upload.

Every new string is a date, a repository label or a provider in the
registry. Every figure is a non-negative sum or a bounded amount, and a
unit's value is never placed on no unit. `TestUploadHasNoFreeText`
covers the new types. Day replacement and negotiation are as for schema
2: below 3 the client sends schema 2 (or 1) exactly as before.

## Upload schema 4 (amended 2026-11-01)

Units of work alone reward volume: more commits look like more done,
even when the work did not stand. Schema 4 adds the counterweight to each
`units` row: **`reverted_commits`**, the row's commits that a later
revert of the member's undid within 14 days (`This reverts commit <sha>`,
as `tokenops spend units` reads it), and **`reverted_commit_usd`**, the
value placed on them. A reverted commit still counts among `commits`; it
is counted again here, never more often than the commits and never with
more value than theirs. A revert that lands after a day was uploaded
counts once the day is uploaded again, within the upload's window.

No new string crosses. A server whose `max_schema` is below 4 gets
schema 3 with the two fields left out (they are omitted when zero), so
it sees no key it does not know.

## Upload schema 5 (amended 2026-11-15)

The waste report shows, beside what is still found avoidable, what the
mechanisms already prevented. Schema 5 carries the prevented side to the
team, as weekly totals and nothing finer:

- **`prevented`** — per finished UTC week × mechanism: `count` (how many
  times it acted), `tokens` (what it kept out, when known), `usd` (the
  dollars prevented, absent when the mechanism is not priced) and
  `blocked_minutes` (lockout only: the minutes of plan window the
  lockouts avoided kept the developer working, absent when zero). The
  mechanism is an enumeration: `read_guard`, `fmt`, `autopilot`,
  `boundary`, `loop_breaker`, `compaction`, `lockout`. A lockout avoided
  is a counterfactual estimate; it is counted and timed in blocked
  minutes, never priced in dollars. Each plan window counts once in its
  week, however many days it was reported on, so its count and its
  minutes are not repeated. `blocked_minutes` is an integer from 0 to
  10,080 (a week's worth). A week's total is capped there. The minutes
  measure the developer's own time, so a week cannot exceed a week's
  minutes; parallel windows across providers are not summed beyond that
  cap. The cap is a floor on honesty, not a measured union of the
  windows' blocked time, which the sender does not compute.
- **`prevented_weeks`** — the weeks (their Mondays) the upload is complete
  for: a listed week with no row for a mechanism had none.

Never per session, repository or person. A week is sent only once every
day of it is in the upload and it ended at least three days before the
upload was computed, the same three days the team view waits, so a week
sent never changes; the server rejects a week that is not a Monday or not
yet final. The suppression floor applies as to every other figure.

Two new strings cross, each constrained: the week (a date, a Monday) and
the mechanism (the enumeration above); `TestUploadHasNoFreeText` lists
both. A server whose `max_schema` is below 5 gets schema 4 with both keys
left out.

Schema 5 changed before its release, on 2026-11-15: `blocked_minutes` was
added to the prevented rows before any server accepted schema 5, so no
server has to take a field it did not expect. It is the third figure a
prevented row carries, and the walk of the upload type requires each
such figure to state its bound.

## Upload schema 6 (amended 2026-11-16)

A seat is a member, a provider and the catalog plan in force. To say
whether a seat is worth its fee, and whether its week will run out or reset
mostly unused, the team needs four things schema 5 does not carry. Each is
a figure the member's own `tokenops plan advise` already shows them, so the
team view adds nothing a person cannot see about themselves.

- **`plan_days[].plan_value_usd`** (optional, 0 to 10,000,000) and
  **`plan_unpriced_turns`** (0 or more, only beside a value): the day's
  plan-covered turns at API list price and the turns with no rate, the same
  figure the plan advice's list value sums (`plans.ListValue`). It replaces
  an approximation from the spend rows, which is wrong wherever the bill is
  not at list price and is not split by plan.
- **`plan_advice[].list_value_usd`** and **`options[]`**: the replay's
  list value and every option it priced, each `{kind, plan, monthly_usd,
  lockouts, blocked_minutes, peak_pct}`, at most 12 (a family's tiers, each
  with metered overflow, and the API). Comparing plan mixes across an
  organisation needs each seat's cost and lockouts under every candidate,
  not only under the recommended one. The kind is the existing enumeration
  and the plan a catalog plan of the advice's provider.
- **`plan_bindings[]`**, replaced whole with each upload: provider, catalog
  plan, `since` (the UTC date the plan history has the plan in force from,
  absent when it does not say) and `shared`. A plan day is sent only when
  something was measured and an advice only when it recommends something,
  so a seat nobody used, the clearest case for cancelling, would otherwise
  not be seen at all.
- **`plan_windows[]`**, replaced whole with each upload: one row per
  provider, for the plan's longest window, and only when that is weekly
  (`monthly` is reserved for a plan with a monthly allowance): `used_pct`
  and `projected_pct_at_reset` (0 to 999) in whole points, `runs_out`,
  `short_hours` (how long before the reset it runs out, in 6-hour steps,
  at most 168), `resets_on` (a UTC date), `basis` (`average` or `recent`)
  and `too_early`. It is the projection the member's status line and
  `plan advise` show (`plans.ProjectAtReset`), computed on the machine; the
  server does not forecast.

**Why 5-hour windows and timestamps stay home.** A 5-hour window starts
with the first request, so its reset says when someone started working,
and a live "locked out now" is monitoring a person, not fitting a plan.
No 5-hour window crosses, and no instant: the reset is a date, no finer
than the plan days already are, and the run-out is a shortfall in 6-hour
steps, not a time.

**The shared flag.** One subscription that two members use uploads the
same vendor meter twice, and no account identifier is uploaded, by design,
so the server cannot tell. `team.shared_plans` lets a member say so; the
upload carries a boolean, never who it is shared with. It defaults to false.

New strings cross, each constrained and listed in `TestUploadHasNoFreeText`:
the option's kind (enumeration) and plan (catalog), the binding's provider
(registry), plan (catalog) and since (a date not after the upload), and the
window's provider (registry), kind and basis (enumerations) and reset (a
date within the window's length of the upload). Every new figure states
its bound in the same test and is checked in `Validate`. A server whose
`max_schema` is below 6 gets schema 5 with all four left out, and the
server refuses a schema 5 upload that carries any of them. Schema 6 may
gain further derived fields before any release carries it.



These are commitments of the hosted service, recorded here so members and
works councils can hold it to them; the server enforces them and the
private ADR has the construction.

- **Aggregates, not people.** Everyone in an organisation sees totals by
  team, repository and kind of work, per day or week, for weeks that ended
  at least three days ago. Any group fewer than three people (or the
  organisation's higher floor) contributed to is withheld, and so are as
  many other groups as needed that no sum or difference of what is shown
  gives a withheld one back. A week is computed once and never changes.
- **Individual figures only by a visible grant.** A member's own figures
  are visible to them, and to someone else only under an owner's grant
  with a reason. The member sees every grant that covers them and every
  view, in the web view and in `tokenops team status`.
- **Retention and erasure.** Figures are kept 400 days by default (30 to
  3650 per organisation), the audit log 730. `tokenops team leave` revokes
  the device and erases its figures unless `--keep-history`; removing a
  member erases theirs.
- **Credentials.** Device, invite, sign-in, device-link and administrator tokens are random and stored
  by the server only as hashes. The client sends only over HTTPS (or to
  loopback) and keeps its enrolment in `~/.tokenops/team.json`, mode 0600.
- **Hosting.** In the EU (Germany), under a GDPR Art. 28 data-processing
  agreement.
- **Admin keys stay home.** Vendor cost is read on an owner's or admin's
  own machine and only the daily totals are uploaded; the service never
  asks for, receives or stores an organisation's admin key.

Widening what crosses — a new field in `pkg/teamwire` — needs a failing
`TestUploadHasNoFreeText`, an allow-list entry and an amendment to this
public ADR.
