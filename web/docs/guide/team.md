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

## Start a team

The team plane is a hosted service klarlabs runs; it is not available to
self-host. Hosting is in Germany, under a GDPR Art. 28 data-processing
agreement, and payment runs through Paddle as merchant of record.

Anyone can start a team for their organisation, with a **14-day free
trial** and no card:

```bash
tokenops team create --server https://<team server>
```

This opens the server's sign-up page in your browser. Either sign in with
GitHub — the server reads your verified primary e-mail address and your
name, nothing else from your account — or sign up with an e-mail address
and a password of at least 12 characters; the address must be confirmed
with the link sent to it before you can continue. Then name your
organisation; you become its owner.

Password accounts can turn on two-factor authentication (an authenticator
app) in the account settings, and reset a forgotten password by e-mail,
which signs out every other session. An e-mail address belongs to one
account: if you signed up with GitHub and later want a password for the
same address (or the other way round), sign in to the existing account
first and add it there — accounts are never merged automatically. The terminal shows a short code: type it into the page
to approve this machine. The code is never part of a link, so only
someone who can see your terminal can approve it. This machine then joins
the organisation and receives an administrator's credential for
`tokenops team admin`, kept beside the enrolment (mode 0600) and valid
for a limited time.

On another machine, or when the credential expired:

```bash
tokenops team admin login --server https://<team server>   # the same code-in-the-browser step
tokenops team admin logout                                 # revoke it
```

## Invite your team

```bash
tokenops team admin create-team platform
tokenops team admin invite --team platform --email ada@example.com   # e-mailed; also prints the join line
tokenops team admin invite --team platform --role lead
```

Each invite is single-use and valid for seven days (`--ttl` to change).
Nobody invites above their own role. With `--email` (or the address field
in the web view) the person gets a mail with the join line and a page that
explains installing TokenOps; otherwise send each person their own line.
They run `tokenops team join <url> <invite>` on their machine.

## Manage it

Owners and admins run their organisation themselves, in the web view (a
**Manage** section) or from the CLI, on the same API. Every change is in
the audit log.

```bash
tokenops team admin teams                 # teams and how many members each has
tokenops team admin members               # names, roles, teams, sign-in addresses
tokenops team admin role "Ada" lead       # member, lead, admin or owner
tokenops team admin remove "Ada" --yes    # revoke their machines and erase their figures
tokenops team admin grants                # who may see individual figures, and why (--all: revoked too)
tokenops team admin grant "Lars" --team platform --reason "1:1 coaching, agreed with the works council"
tokenops team admin revoke <grant-id>
tokenops team admin audit                 # newest first
tokenops team admin billing               # trial, subscription, seats
```

Members are named by ID, sign-in address or exact name. The listing commands
take `--json`. Granting is an owner's; the member covered sees the grant
and its reason on their page. `TOKENOPS_TEAM_ADMIN_TOKEN` with `--server`
uses an administrator's token you were given instead of `admin login`.

## Billing

The price is per seat per month, a seat being an active member; the
subscription's quantity follows members joining and leaving.

```bash
tokenops team admin billing --checkout    # subscribe (opens the server's billing page)
tokenops team admin billing --portal      # invoices, payment details, cancel (Paddle's customer portal)
```

The web view's billing page does the same. Owners get an e-mail, at the
verified address they signed up with, three days
before the trial ends, when it ended, when a payment failed and when the
subscription is active.

**When the trial ends without a subscription**, or a subscription ends,
uploads are paused: the server refuses them, and `tokenops team status`,
`tokenops team sync` and the daemon's log say so plainly, with what to do.
Nothing is lost on members' machines: each upload resends the last 14
days, so the first one after subscribing fills the gap up to that. The
figures already held stay viewable, read-only, for 30 days and are then
deleted.

## When figures appear

Totals are released per week, **three days after the week ends** (Monday
to Sunday, UTC), and only for groups at least three people contributed
to, so a new organisation's first week appears up to ten days after it
starts. The web view and `tokenops team status` name the date; until then the overview says what will appear and when, and each
member's own page shows their figures as soon as they are uploaded.

## Single sign-on

Single sign-on to the web view with your OpenID Connect provider (Google
Workspace, Microsoft Entra ID, Keycloak, Okta, or Dex in front of GitHub)
is available beside GitHub sign-in and single-use sign-in links; it signs
in only existing members whose verified address an owner set, and never
creates anyone.
