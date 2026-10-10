# ADR 0012 — The team plane: hosted, derived figures only, aggregate by default

- **Status:** Accepted 2026-10-08; amended 2026-10-10: the server is a private service klarlabs hosts.
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
machine: `tokenops team join|status|preview|sync|web|leave`, the daemon's
uploader, `internal/capability/teamshare` (which builds an upload),
`internal/infra/teamclient` (which sends it), the `team` configuration —
and `pkg/teamwire`, the upload contract the server is built against. That
split is deliberate: a member must be able to check what leaves their
machine without trusting the server, so the boundary below is documented
and enforced here, in public.

There is no default server: `tokenops team join <url> <invite>` takes the
address from the invite. Nothing is sent until a machine joins.

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

## What the service promises about what crosses

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
- **Credentials.** Device, invite and sign-in tokens are random and stored
  by the server only as hashes. The client sends only over HTTPS (or to
  loopback) and keeps its enrolment in `~/.tokenops/team.json`, mode 0600.
- **Hosting.** In the EU (Germany), under a GDPR Art. 28 data-processing
  agreement.

Widening what crosses — a new field in `pkg/teamwire` — needs a failing
`TestUploadHasNoFreeText`, an allow-list entry and an amendment to this
public ADR.
