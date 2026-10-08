# Team plane

The team plane rolls up what TokenOps measures on each member's machine —
by team, by repository and by kind of work, per day or week — on a server
your organisation runs or has hosted in the EU. It is the shared view a
team lead or a platform group asks for, without anyone's prompts, code or
transcripts leaving their laptop.

## What leaves a machine

Only after `tokenops team join`, every hour, the daemon sends the last 14
days recomputed, one row per **UTC day × repository × kind of work**:

| Figure | Meaning |
|---|---|
| sessions, instructions | agent sessions with work in the row; instructions typed |
| turns, tool calls | the agent's turns and tool calls answering them |
| active seconds | time spent waiting on the agent, summed |
| first try, reworked, interrupted, escalated, rejected | outcome counts, each a subset of instructions |
| tokens | input and output tokens |
| cost, value | what was billed, and the same work at API list prices |
| unpriced turns | turns on models with no list price |

The repository is a label: `owner/name` from its `origin` remote, the
directory's name, or `hidden` (`team.repo_names`); work outside a
repository is `none`. The kind of work — lookup, research, edit, deep or
unknown — is classified on your machine from the instruction's opening
words, and only the kind is sent.

**Never sent:** prompts, file contents, file names or paths, transcripts,
commit messages, branch names, model outputs, model names or session IDs.
The upload format has no field that could carry them, a test fails the
build if one is added, and the server refuses anything else
([ADR 0012](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0012-team-plane.md)).
Check it yourself:

```bash
tokenops team preview          # the next upload as a table; sends nothing
tokenops team preview --json   # byte for byte as it is sent
```

## Joining and leaving

An owner or admin sends you an invite. Then:

```bash
tokenops team join https://team.example.eu tot_inv_… --name "Ada Lovelace"
tokenops team status           # what the server holds, who may see it, who looked
tokenops team sync             # upload now instead of within the hour
tokenops team web              # a single-use link to the web view
tokenops team leave            # stop, and erase this machine's figures on the server
```

`team.enabled: false` (or `TOKENOPS_TEAM_ENABLED=0`) pauses uploads while
staying joined. See [Configuration](/guide/configuration#team-plane-team).

## Who sees what

- **Everyone in the organisation** sees totals by team, repository and kind
  of work. Any row fewer than three people contributed to is withheld — a
  "team total" of one person is that person's figures. The organisation
  can raise the threshold.
- **Your individual figures** are visible to you, and to nobody else unless
  an owner grants it: to a named lead, admin or owner, for one team or for
  everyone, with a reason. Being an owner is not enough by itself.
- **You are told.** Your page in the web view and `tokenops team status`
  list every grant that covers you, with its reason, and every time
  someone looked at your figures. Owners and admins see the whole audit
  log.

This is built for EU workplaces with a works council: the threshold, who
may hold grants and why, and how long figures are kept are the points a
works agreement settles, and each is visible to the people it concerns.

## Retention

Figures are kept 400 days by default (30 to 3650, per organisation); the
audit log 730 days. Leaving erases your machine's figures unless you pass
`--keep-history`; an admin removing you erases all of them.

## Running the server

The server is `tokenops-team`: a Go service with Postgres behind Caddy,
deployed with Docker Compose on one VPS. `deploy/team/README.md` in the
repository is the runbook — a Hetzner server in Germany, DNS, TLS,
creating the organisation and invites, grants, backups and upgrades.

| Variable | Meaning |
|---|---|
| `TEAMSERVER_DATABASE_URL` | Postgres connection URL (required) |
| `TEAMSERVER_PUBLIC_URL` | the address members use, e.g. `https://team.example.eu` (required) |
| `TEAMSERVER_LISTEN` | listen address, default `:8080` |
| `TEAMSERVER_TRUSTED_PROXIES` | CIDRs whose `X-Forwarded-For` is believed, default loopback and private ranges |
| `TEAMSERVER_AUDIT_RETENTION_DAYS` | audit log retention, default 730 |

### API

All JSON; errors are `{error, hint}`. A device token (from `join`) or an
admin token goes in `Authorization: Bearer …`.

| Method and path | Who | What |
|---|---|---|
| `POST /api/v1/enroll` | invite holder | join; returns the device token once |
| `POST /api/v1/ingest` | device | an upload; idempotent per batch and per day |
| `GET /api/v1/me` | device, admin | what is held about you, grants covering you, views of you |
| `DELETE /api/v1/devices/self[?keep=true]` | device | leave; erases unless `keep` |
| `POST /api/v1/login-links` | device, admin | a single-use web sign-in link |
| `GET /api/v1/aggregates?by=team\|repo\|kind&period=day\|week&since=&until=&team=` | any member | totals and rates, small groups withheld |
| `GET /api/v1/members` | any member | the members you may see |
| `GET /api/v1/members/{id}/metrics` | yourself, or a grantee | individual figures; recorded and shown to the member |
| `GET, POST /api/v1/teams` | admin to create | teams |
| `POST /api/v1/invites` | admin | `{team, role, ttl_hours}` |
| `GET, POST /api/v1/grants`, `DELETE /api/v1/grants/{id}` | owner (admin to list) | individual-view grants, with a reason |
| `DELETE /api/v1/members/{id}` | admin | remove a member and erase their figures |
| `GET /api/v1/audit` | admin | the audit log |
| `PUT /api/v1/settings` | owner | `{min_group_size, retention_days}` |
