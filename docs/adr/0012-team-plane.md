# ADR 0012 — The team plane: hosted, derived figures only, aggregate by default

- **Status:** Accepted 2026-10-08. Implemented for issue #250: `cmd/tokenops-team`, `internal/teamserver`, `internal/contexts/team`, `pkg/teamwire`, `internal/capability/teamshare`, `tokenops team`, `deploy/team`.
- **Date:** 2026-10-08
- **Deciders:** TokenOps maintainers
- **Related:** issue #250, ADR 0004 (local-first; no BI product), ADR 0010 §5 (the API's privacy line), `docs/competitive-landscape.md` (G7, step 8), the positioning decisions of 2026-09-15

## Context

TokenOps is local-first: every figure is computed on the machine that did
the work, from its own transcripts and event store. A team wants the same
figures rolled up across people — by team, by repository, by kind of work —
and that is the one column where team products beat TokenOps on substance
(`docs/competitive-landscape.md`, G7).

Three decisions were taken on 2026-09-15 and are not reopened here:

1. A team tier **requires a hosted plane**. Self-hosted-only sells to people
   who enjoy running infrastructure.
2. **Only derived metrics cross the boundary**: rates, grades, token counts,
   costs, durations, counts. Prompts, file contents, transcripts, paths,
   commit messages and model outputs never leave the machine.
3. **Aggregate by team, repository and kind of work by default**; drill-down
   to an individual is role-gated. In an EU works-council (Betriebsrat)
   context this is a precondition, not a feature: § 87 (1) 6 BetrVG makes
   any technical system suited to monitoring employees' performance subject
   to co-determination, and GDPR Art. 5 requires data minimisation.

This ADR records how those decisions are built.

## Decision

### 1. Boundary: a type that cannot carry content

What crosses is `pkg/teamwire.Upload`: per **UTC day × repository label ×
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
  its allow-list, and an amendment to this ADR.
- **The builder.** `internal/capability/teamshare` reads instruction text at
  scan time only to classify its kind of work (`taskclass.KindForTurn`), and
  session directories only to find the repository, reduced to its origin's
  `owner/name` (or directory name, or `hidden`, per `team.repo_names`). A
  canary test builds an upload from a machine full of prompts, secrets,
  paths, file names, model names and session IDs and asserts none appear in
  the JSON.
- **The server.** Ingestion decodes with unknown fields refused, runs the
  same `Validate`, and the `metric_buckets` table repeats the label pattern
  and the kind enumeration as `CHECK` constraints.

`tokenops team preview --json` prints an upload byte for byte as it is sent,
so a member, or a works council, can check the claim instead of trusting
it.

Model names, session IDs, branch names and commit SHAs are deliberately not
in the upload: they add identification power without adding a team-level
question the counts cannot answer.

### 2. Data model

Postgres, schema in `internal/teamserver/pgstore/migrations`:

| Table | Holds |
|---|---|
| `orgs` | name, `min_group_size` (default 3), `retention_days` (default 400) |
| `teams`, `team_members` | teams and membership; a member may be in several |
| `members` | display name and role: `member`, `lead`, `admin`, `owner` |
| `devices` | one per enrolled machine; token hash, last upload, revocation |
| `invites` | single-use, expiring enrolment tokens (hash) for a team and role |
| `tokens` | web sessions, single-use sign-in links, admin API tokens (hash) |
| `grants` | who may see whose individual figures: grantee, team or everyone, reason |
| `metric_buckets` | the upload's rows, keyed by device, day, repo, kind |
| `device_days` | when each device's day was computed, so a late, older upload cannot overwrite a newer one |
| `ingest_batches` | batch IDs seen, so a replay is acknowledged and not applied |
| `audit_log` | every invite, enrolment, grant, revocation, removal, settings change and view of an individual |

**Ingestion is idempotent by construction**: an upload names the days it is
complete for, and for each of them replaces the device's rows whole, inside
one transaction, unless a newer computation of that day is already held.
Re-sending, retrying after a timeout, and recomputing a day after
reclassification all converge on one copy of the latest figures. The client
recomputes the last 14 days every hour (`team.days`, `team.interval`).

Rates are never stored or averaged: every figure is a sum, and rates are
derived after summing (`team.Totals.Rates`).

### 3. Who sees what

- **Aggregates** (by team, repository or kind of work, per day or ISO week)
  are visible to every member of the organisation. Any row fewer than
  `min_group_size` distinct people contributed to is **withheld**: shown as
  "fewer than 3 people", its figures zeroed before they leave the server.
- **Individual figures** are visible to the member themself, and otherwise
  only under a **grant**: created by an owner, naming a lead, admin or owner,
  a scope (one team, or everyone) and a reason. Roles alone grant nothing —
  an owner without a grant cannot open a colleague's figures. A grantee
  demoted below lead loses access without the grant being revoked.
- **The member is told.** Each member's page (web `/me`, `tokenops team
  status`) lists every grant that covers them with its reason, and every
  view of their figures with who and when. A view is written to the audit
  log *before* the figures are read; a view that cannot be recorded is not
  served.
- Owners and admins read the full audit log. Members cannot, beyond the
  entries about them.

This maps onto a works agreement (Betriebsvereinbarung) directly: the
council agrees the minimum group size, who may hold grants and for what
reasons, and retention; members can verify each.

### 4. Authentication

There are no passwords. Every credential is 256 bits from `crypto/rand`,
prefixed with its kind (`tot_dev_`, `tot_inv_`, `tot_lnk_`, `tot_ses_`,
`tot_adm_`), shown once, and stored only as its SHA-256. A slow hash buys
nothing at this entropy; a leaked database holds no usable credential.

- **Invite** → one enrolment, then spent; expires after 7 days (≤ 30).
- **Device token** → uploads, and member-level reads for that member
  (`/api/v1/me`, sign-in links, leaving). Kept on the machine in
  `~/.tokenops/team.json`, mode 0600, never in `config.yaml`, never logged.
- **Sign-in link** → `tokenops team web` (or `tokenops-team login-link` on
  the server) mints a 10-minute, single-use link. Opening it shows a button;
  only the POST redeems it, so mail scanners and link previews cannot use
  it up. It becomes a 12-hour session cookie: `__Host-` prefixed, Secure,
  HttpOnly, SameSite=Strict. Form posts also check `Origin`.
- **Admin API token** → owner/admin API calls (teams, invites, grants,
  audit, settings). Session cookies are not accepted on `/api/*`, so a page
  on another site cannot drive the API.

SSO (OIDC/SAML) is not in this version; see Consequences.

### 5. Retention and erasure

- Figures: deleted after the organisation's `retention_days` (30–3650,
  default 400 — a year plus a quarter for year-on-year comparison).
- Audit log: `TEAMSERVER_AUDIT_RETENTION_DAYS`, default 730.
- Spent or expired credentials, invites and batch receipts: purged.
- The purge runs at start and every six hours.
- `tokenops team leave` revokes the device and **erases its figures** by
  default (`--keep-history` keeps them in the team's history). An admin's
  `remove-member` erases the member's figures, devices, credentials and
  grants. The audit log keeps names against what was done, which is the
  accountability record the member is owed.

### 6. Hosting

One VPS at Hetzner in Germany (Falkenstein or Nuremberg): Docker Compose
with Caddy (TLS via ACME, HSTS), the `tokenops-team` Go service (distroless,
non-root, read-only filesystem, no capabilities), Postgres 17 on an
internal network, and a nightly `pg_dump`. Data stays in the EU; Hetzner
signs a GDPR Art. 28 DPA. `deploy/team/README.md` is the runbook.

The service is stateless apart from Postgres; rate limits are in memory,
which is right for one instance and documented as such.

## Threat model

| Threat | Mitigation | Residual |
|---|---|---|
| A modified or buggy client uploads prompt text or paths | Unknown fields refused; `Validate` and DB `CHECK`s admit only enums and constrained labels | A label can still be any short `owner/name`-shaped token; one word can be smuggled per row. Bounded, and not a channel for a prompt |
| A repository name is itself sensitive | `team.repo_names: hidden` or `directory` | Defaults to the origin `owner/name` |
| Manager singles out an individual through aggregates | Rows under `min_group_size` withheld; drill-down needs a visible, audited grant | Differencing across overlapping groups (a team total minus a sub-filter) can narrow to one person over time. Mitigated by the group floor on every row; not eliminated. A council may raise the floor |
| Owner grants themself silent access | Grants and every view are shown to the member and in the audit log | An owner with database access can read the tables directly; that is the hosting operator's trust boundary |
| Stolen device token | Uploads only for that device; revocable by `leave` or `remove-member`; rate limited | Thief can upload false figures for that device until revoked |
| Stolen database or backup | No credential is usable (hashes only); no content is present | Names and per-person figures are exposed: treat dumps as personal data |
| Brute-forcing tokens | 256-bit tokens; per-address rate limits on enrolment and sign-in | — |
| Login token leaks via logs or Referer | Request log omits query strings; Caddy access log off; `Referrer-Policy: no-referrer` | — |
| CSRF / clickjacking on the web view | SameSite=Strict, Origin check, CSP `frame-ancestors 'none'`, `X-Frame-Options: DENY` | — |
| XSS | `html/template` escaping; CSP `default-src 'none'`, no scripts at all | — |
| Replay or reordering of uploads | Batch IDs deduplicated; per-day `computed_at` refuses older data | — |
| Oversized or flooding uploads | 1 MiB body, 5000 rows, 62 days; per-device token bucket | — |
| MITM | Client refuses non-HTTPS except to loopback; HSTS | — |

## Consequences

- The privacy claim on the landing page stays absolute under hosting, and
  is checkable: `tokenops team preview --json`.
- A team gets rollups by team, repository and kind of work, day or week,
  with the same counts the local coach uses.
- New surface to operate: one VPS. Backups and upgrades are documented;
  nothing is automated beyond Docker's restart policy and the nightly dump.
- Every release ships the server: `tokenops-team_<version>_linux_<arch>.tar.gz`
  (amd64, arm64) in its own goreleaser build and archive, so the CLI
  archives, the cask and the npm packages are unchanged, and the image
  `ghcr.io/klarlabs-studio/tokenops-team:<version>`, built by a job that
  runs only after the release published, from those archives verified
  against `checksums.txt` (`scripts/build-team-image.sh`). It is the only
  release job that may write packages. CI runs goreleaser as a snapshot and
  the same script without pushing, so the image never builds for the first
  time on a tag. The Compose file runs the image by version; a build
  override (`docker-compose.build.yml`) builds from source.
- Not in this version: SSO, multi-region, a hosted multi-tenant signup flow
  and billing, per-team minimum group sizes, and export to BI tools (ADR
  0004 still rules out becoming a BI product).
