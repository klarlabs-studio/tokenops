# Team plane

The team plane is a service klarlabs hosts in the EU. It rolls up what
TokenOps measures on each member's machine — by team, by repository and
by kind of work, per day or week — into the shared view a team lead or a
platform group asks for, without anyone's prompts, code or transcripts
leaving their laptop.

The part that runs on your machine is open source, in this repository:
the `tokenops team` commands, the daemon's uploader, and the upload format
itself (`pkg/teamwire`). The server is klarlabs' and is not published, so
you never have to trust it about what it receives: what leaves your
machine is decided, and checkable, here.

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

Your organisation's owner or an admin sends you an invite: a server
address and a single-use code, valid for seven days unless they chose otherwise. There is no default
server; `join` takes the address from the invite. Before joining, see what
would be sent:

```bash
tokenops team preview          # computed now from this machine; sends nothing
tokenops team join <url> <invite> --name "Ada Lovelace"
tokenops team status           # what the server holds, who may see it, who looked
tokenops team sync             # upload now instead of within the hour
tokenops team web              # a single-use link to the web view
tokenops team leave            # stop, and erase this machine's figures on the server
```

`team.enabled: false` (or `TOKENOPS_TEAM_ENABLED=0`) pauses uploads while
staying joined. See [Configuration](/guide/configuration#team-plane-team).

## Who sees what

- **Everyone in the organisation** sees totals by team, repository and kind
  of work, per day or week, for weeks that ended at least three days ago.
  Any group fewer than three people contributed to is withheld — a "team
  total" of one person is that person's figures — and so are as many other
  groups as needed that no sum or difference of what is shown gives a
  withheld group back. A week is computed once and never changes, so
  asking again, with another window or after more uploads, shows nothing
  new. The organisation can raise the threshold.
- **Your individual figures** are visible to you, and to nobody else unless
  an owner grants it: to a named lead, admin or owner, for one team or for
  everyone, with a reason. Being an owner is not enough by itself.
- **You are told.** Your page in the web view and `tokenops team status`
  list every grant that covers you, with its reason, and every time
  someone looked at your figures. Owners and admins see the whole audit
  log.

The server enforces these; they are recorded publicly in
[ADR 0012](https://github.com/klarlabs-studio/tokenops/blob/main/docs/adr/0012-team-plane.md),
so a works council can hold the service to them.

This is built for EU workplaces with a works council: the threshold, who
may hold grants and why, and how long figures are kept are the points a
works agreement settles, and each is visible to the people it concerns.

## Retention

Figures are kept 400 days by default (30 to 3650, per organisation); the
audit log 730 days. Leaving erases your machine's figures unless you pass
`--keep-history`; an admin removing you erases all of them.

## Getting the team plane for your organisation

The team plane is offered by klarlabs as a hosted service; it is not
available to self-host. klarlabs sets up your organisation, its owner and
your teams, and the owner invites members. Hosting is in Germany, under a
GDPR Art. 28 data-processing agreement. Single sign-on to the web view
with your OpenID Connect provider (Google Workspace, Microsoft Entra ID,
Keycloak, Okta, or Dex in front of GitHub) is available beside single-use
sign-in links; it signs in only existing members whose verified address an
owner set, and never creates anyone.
