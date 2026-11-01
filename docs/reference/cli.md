# CLI reference

The `tokenops` binary wraps the daemon's local event store. Every
subcommand has a matching MCP tool (`tokenops_<name>`).

## At a glance

### `tokenops glance`

`tokenops` on its own, once set up, draws every plan as a card, laid out
like CodexBar's `codexbar cards`: the vendor, where the reading came from
and the plan; then for every window the vendor reports, its share used, a
bar, when it resets and its pace; then spend against a limit, credit left,
and cost today and over 30 days. Busiest plan first, in a grid that fits
the terminal.

```
TokenOps • AI Usage & Limits                                           Sun 4 Oct 09:30

╭────────────────────────────────────────╮  ╭────────────────────────────────────────╮
│ Codex [local] PLAN Pro Standard ($100) │  │ Claude [meter]                PLAN Max │
│ ────────────────────────────────────── │  │ ────────────────────────────────────── │
│ Weekly                         9% left │  │ Session                       96% left │
│ [ ━━━─────────────────────────────── ] │  │ [ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━─ ] │
│ Resets in 30m                          │  │ Resets in 4h 37m                       │
│ Pace: ahead (+37%) · out in 23h 47m    │  │ Pace: behind (-3%) · lasts to reset    │
│                                        │  │                                        │
│ Overage risk:                     HIGH │  │ Weekly · Fable               100% left │
│ Today:              $28.00+ · 198M tok │  │ [ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ ] │
│ 30 days:             $745+ · 2.24B tok │  │ Resets in 5d 17h                       │
│ At API prices; the plan covers it.     │  │                                        │
│ 15% of requests have no price yet.     │  │ Today:                       1.00B tok │
│                                        │  │ 30 days:            $3,214 · 6.10B tok │
│                                        │  │ At API prices; the plan covers it.     │
╰────────────────────────────────────────╯  ╰────────────────────────────────────────╯
```

Each window shows the **share left**, and its bar shrinks as the window
fills, coloured by how much is used — the menu bar and the statusline
read the same way.

**Pace** compares the share used with the share of the window gone by:
behind lasts to the reset; ahead says when the window runs out at the
rate so far. The daemon API, the menu bar and MCP report the same pace.

A window nothing has used yet has no reset to count down to. Codex keeps
reporting a full week from each reading until the first request starts
it, so the glance says **starts at your next request** in place of a
countdown, and such a window has no pace. The API marks it with
`not_started: true` and leaves out its reset.

**Early resets** are shown when a window resets well before the time its
vendor gave: more than an hour early, after it had been at least a fifth
used, and back to near empty. Nothing the operator did explains it, so the
glance carries an info finding for a day, such as *Codex's weekly window
reset 5d 12h early*, with the readings either side of it. A plan switch
between those readings is not counted. The findings and `early_resets` in
`/api/glance` report it.

**Plan use** is two figures per plan over the last 7 days. *Plan used* is
the average peak of the plan's longest window (usually the weekly one)
across the windows that reset in that week. It answers "is this seat
used?", and idle days count against it. *On the plan* is the share of that
provider's tokens a plan covered rather than an API key, a gateway or
overflow billed per token. The menu bar and `/api/glance` (`plan_use`)
carry the same figures, and `tokenops scorecard` reports them over its own
window.

**Cost** is at API list prices where a plan covers the usage. Requests on
models without a list price yet are left out of the money: a `+` marks a
figure that leaves some out, and where most have no price only tokens are
shown, since a total that looks complete but is not misleads.

**Coach** findings follow the cards, most urgent first: what the coach and
the session analysis observed, the figures behind it, and what to do.

```
Coach · 5 findings
  ▲ Codex's weekly window runs out in 23h 32m at this pace
    44% left, 38 points ahead of an even pace; it resets in 5d 18h
    → Put the work that can move on Claude (75% left) until it resets.
  ● Agents re-read 14 unchanged files in full in the last 7 days
    about 225k tokens of context spent on files already read, across 63 sessions
    → tokenops coach set waste autonomous lets the read guard refuse them.
  · The coach is observing and says nothing
    it would have given 560 tips so far
    → tokenops coach preset advise lets it speak, once per kind of thing.
```

They cover a quota window that runs out before it resets (and a plan with
room to take the work), a weekly window that resets within a day with 40%
or more unused (it does not carry over), files the agent re-reads unchanged, sessions past
their budget, the one change that would most improve how sessions go,
session sources that cannot be read, kinds of advice the coach stopped
giving because they were ignored, and what the coach would have said while
it only observes. The session analysis reads a week of transcripts, so the
daemon runs it in the background every three hours; the section says how
old it is. `tokenops glance --findings` lists every finding, and
`GET /api/findings` serves the same list. Findings carry figures only,
never prompt or file text.

**Unusual spend.** Every hour the daemon forecasts today and yesterday from
the four weeks before (Holt's method, as the spend forecast) and
flags a day whose spend, or usage at API prices, is above the forecast's
95% band by at least $5 and 25%. Nothing is flagged until there are 10
days of history, and at most one day's finding shows per day, spend before
usage. The finding ranks what drove the rise, by the dollars each model,
biller (with the route it went through), billing basis, repository, tool
and session added against its own daily mean, and names the likely cause
when it recognises one: a pricier model that took over, usage billed per
token while a plan should cover it (with the plan coverage against
before), or one session carrying the rise, as a runaway loop does (with
what the waste detector found in it).

```
  ● Spend on Wed 14 Oct was $24.00, $24.00 over the usual $0.00
    Likely cause: Claude usage was billed per token while its plan should cover it, through anthropic-api; plan coverage 20% against 99% before; ranked by what each added: model claude-sonnet-5 +$24.00 (100%), biller anthropic via anthropic-api +$24.00 (100%), …
    → Check the API key or base URL your client uses: Claude's plan covers only its own endpoint. If this is normal for you, `tokenops coach tip expected --id anomaly-spend-2026-10-14` stops flagging it; `ack` or `snooze` answer it too.
```

It sits with the other findings on glance, the menu bar and
`GET /api/findings` (its `id` is the anomaly's), and never interrupts a
session. Answer it like a tip (`tokenops coach tip`): `ack` takes it off,
`snooze` holds every spend finding for a while, and `expected` also stops
flagging the same pattern (the same model, the same provider billed per
token, a loop in the same repository) for 90 days. Only derived figures
are kept, in `~/.tokenops/cache/spend-anomalies.json`.

In a true-colour terminal the bars are solid cells running green, amber,
red along their length, in the Klarlabs palette; 16 colours elsewhere,
plain text for a pipe.

```bash
tokenops glance --brief          # a table: plan, window, used, resets, pace
tokenops glance --findings       # every coach finding, nothing else
tokenops glance --json           # the payload GET /api/glance serves
tokenops glance --color always | less -R
```

```
PLAN                       WINDOW            LEFT  RESETS    PACE
Codex Pro Standard ($100)  Weekly              9%  30m       +37% · out in 23h 47m
Claude Max                 Session            96%  4h 37m    -3% · lasts
                           Weekly · Fable    100%  5d 17h
```

Colour follows the terminal and `NO_COLOR`; `--no-color` (or `--color
never`) prints plain text. `$COLUMNS` sets the width.

### `tokenops otel`

Prints every metric the daemon pushes to the OpenTelemetry collector in
`otel.endpoint` — plan windows and pace, usage and its value, session
grades, the coach's findings, cost per commit — computed now from this
machine's data, and whether pushing is on. Figures only; see
[OpenTelemetry export](./configuration.md#opentelemetry-export).

```bash
tokenops otel
tokenops otel --json
```

### `tokenops team`

Shares this machine's derived figures with the [team plane](https://team.klarlabs.de/guide/team),
the service klarlabs hosts (the client is open source; the server is not):
per UTC day, repository and kind of work, counts, time, tokens and cost.
Nothing is sent until you join, and never a prompt, file, transcript,
path, commit message or model output.

```bash
tokenops team create --server <url>  # sign up with GitHub, start an organisation (free during the beta), join this machine
tokenops team join <url> <invite>   # enrol with an invite; the URL comes with it
tokenops team preview          # exactly what the next upload holds; sends nothing
tokenops team preview --json   # the upload byte for byte
tokenops team sync             # upload now (the daemon does it hourly)
tokenops team status           # who may see your individual figures, and who looked
tokenops team web              # single-use sign-in link to the web view
tokenops team leave            # revoke this machine and erase its figures
tokenops team leave --keep-history
tokenops team admin login           # owners and admins: approve this machine in the browser with a code
tokenops team admin teams|create-team|invite|members|remove|role|grants|grant|revoke|billing|audit
```

`create` and `admin login` show a short code to type into the server's
page; the code is never part of a link. `admin` commands take `--server`, the listing ones `--json`
(see [Team plane](https://team.klarlabs.de/guide/team#manage-it)).

`join` takes `--name` (how people granted to see individual figures see
you) and `--device`. The enrolment is kept in `~/.tokenops/team.json`
(mode 0600), not in `config.yaml`; `leave --force` forgets it locally when
the server cannot be reached.

### `tokenops menubar`

On macOS, installs the menu bar app that ships with the Homebrew install
to `~/Applications` and opens it: an icon with two rings for the busiest
plan, its week outside and its session inside, each filled to the share
left. Click it for a panel with every plan's windows, pace, usage and the
coach's findings; hover for each window's reset; right-click for Launch at
Login and Alerts. It alerts when a window drops to 20% left, to 5%, and when it is used up,
saying when it resets and, while it is still running, when it runs out at
this pace; a window that alerted says so again when it resets. Decisions a
boundary is waiting on are listed in the panel: Deny answers at once, and
with a presence key enrolled Approve shows the Touch ID dialog and answers
inline (without one it opens a terminal running `tokenops approve`).
Upgrades refresh the installed copy; `--refresh` stops only the app itself,
never the presence helper inside it, so a dialog you are answering is not
cut off.

### `tokenops checkup`

A week of agent work in one read, before you set anything up. It needs no
config, no daemon and no store: it reads what Claude Code, Codex, Gemini
CLI and opencode already wrote, and sends nothing anywhere. Without
TokenOps installed, `npx @klarlabs-studio/tokenops checkup` runs it from npm.

```
TokenOps checkup — last 7d

HARNESS      MODEL                           TURNS     TOKENS    API VALUE
Claude Code  claude-opus-5-5                 11997      5.40B     $1609.92
Codex        gpt-6.1-sol                      2715     363.5M       $81.53
Total                                        17125      6.13B    $1938.76+

Sessions: A  (1880 instructions; `tokenops dx` for the breakdown)

● Agents re-read 10 unchanged files
  11 re-reads of 10 files across 3 sessions, with no edit in between
  fix: tokenops hooks install --read-guard
```

- **API value** is what the tokens would cost on an API key. On a
  flat-rate plan it is a measure, not a bill. A model the rate card does
  not know is counted and marked `unpriced`, never guessed (the `+`).
- **Findings**, each with the one command that fixes it:
  - re-reads of a file with no edit in between;
  - instruction files (`~/.claude/CLAUDE.md`, a repository's `CLAUDE.md`
    and `AGENTS.md`, and their parents') re-read on every turn, as a share
    of all input;
  - what a Claude Code session carries before any work: the median
    context of each fresh session's first turn (Claude Code's system
    prompt, the tools of every MCP server and skill configured, the
    instruction files and the first instruction), with how many MCP
    servers and skills are configured. Every turn reads it again, so it
    is worth acting on from 40k tokens;
  - lookups ("show me…", "list…") answered on a flagship model;
  - catalog drift: your own plan windows on two tiers disagree with the
    catalog's ratio between them (Max at five times Pro, Ultra's week at
    ten). This one reads the event store and the plan history, so it
    needs `tokenops init` and a plan switch in the last 90 days; without
    them it is left out.

  A fix that is already installed says so instead of installing it again.
- **Catalog drift** measures how much list value one percent of a window
  held, per window, from the vendor's own readings and the requests
  recorded up to the fullest reading, and compares the median on each
  tier. It says nothing rather than guess. A window counts only when it
  was filled from 10% to 99%, sat wholly on one plan, opened after the
  read began, and was spent at least 90% on one model family (Opus,
  Sonnet, Haiku, Fable): a percent weighs models differently, so tiers
  are compared only within one family. Each tier needs 5 such windows.
  The finding is raised when the measured ratio is at least 25% from the
  catalog's and the catalog's lies outside the range the two tiers'
  quartiles allow; at 50% or more it is a notice. One tier alone is not
  checked: the catalog's base (Pro's 45 messages) is in chat messages,
  which the readings do not measure. Use on claude.ai or another machine
  fills the vendor's percent without reaching TokenOps' counts, so a
  change in that share between the two tiers moves the ratio too. The
  catalog changes by pull request (ADR 0002); the finding says to check
  the vendor's page and open an issue.
- **Sessions** is the same grade `tokenops dx` gives, from the same read.

`--days 30` widens the window; `--json` prints the report as data.
Instruction text is read in memory to classify it, and never written.

## Setup

### `tokenops init`

Scaffolds the config (sqlite + rules on, idempotent) **and wires this
machine**: it registers the MCP server with every installed host and
installs the Claude Code hooks. A new config also gets the
[headroom pause](https://team.klarlabs.de/guide/boundaries#the-headroom-pause), in shadow: it
records each session it would have paused once the tightest plan window
is down to 10% left and binds nothing until you promote it; init installs the pause
point (the boundary hook) to evaluate it. It also gets the
[loop breaker](https://team.klarlabs.de/guide/boundaries#the-loop-breaker), in shadow: it records
each session whose agent went round a loop of tool calls with no file
changed. Re-running init on an existing
config does not add it. `--detect` adds a report of the clients it
found; it does not make the command read-only. Use `tokenops detect` to
only look, or `--print-only` / `--no-wire` for a dry run.

The rules scan root is the repository `init` was run inside — it walks up
for a `.git` — not the directory your shell happened to be in. Running it
from your home directory is refused rather than binding the scanner to
every project you own.

```bash
tokenops init --detect          # write config + suggest plan-set commands
tokenops init --print-only      # render YAML to stdout, don't write
tokenops init --force           # overwrite existing config
tokenops init --currency EUR    # show totals in euros (default: your system region)
tokenops init --preset guided   # set up the coach (default for a new config: advise)
tokenops init --yes             # ask nothing; take every default
```

**Plans come from your clients.** Claude Code records the plan you are
signed in with (its account type and rate-limit tier) and Codex records
its `plan_type` in every session; `init` binds the plan they name and
says which client reported it. When you change plans and the client
reports the new one, TokenOps follows it: `init` does, and so does the
running daemon, which checks every 15 minutes without anyone re-running
`init`. The switch takes effect from when the client first reported the
new plan, is recorded in `tokenops plan history` and `tokenops audit`
(with the client as actor and what it reported), and is said once in
`init`'s output, the daemon log and, for a week, `tokenops glance`,
each with the command that undoes it:
`tokenops plan set <provider> <plan>`. A plan you set after the client
started reporting its plan is kept until the client reports a different
one. A report that fits two plans, or none TokenOps knows, changes
nothing.

**In a terminal it asks only what nothing on the machine could answer**,
each with a default Enter accepts:

- which plan, when the evidence fits two (a Claude Team seat, say);
- what you pay for each plan it bound, as on your bill (optional; it
  values your usage in your currency);
- the monthly spend limit of a usage-billed plan nothing reports;
- the coach preset, for a new config;
- whether to install the daemon unit, when nothing supervises ingestion.

With no terminal (MDM, CI), or with `--yes`, it asks nothing and takes
every default.

`init` also turns on the transcript readers for every client whose
sessions are on this machine (Claude Code, Codex, opencode), and says how
to switch one off. It only does this when no reader is enabled yet, so a
reader you turned off stays off.

The currency comes from your system region when you do not name it; on
macOS the region setting wins over the language, so an English-language
system in Germany gets euros. Re-running `init` keeps the currency and
coach you already have unless you name new ones.

### `tokenops detect`

Reports which AI clients are installed and changes nothing. Filesystem and
environment only: no network calls, no credential reads, nothing written.

```bash
tokenops detect
```

A bare API key is reported as metered usage with no plan to bind — binding
a subscription against metered traffic would make every headroom figure
wrong in a way that looks authoritative.

### `tokenops plan {list|set|unset|catalog|headroom|advise}`

```bash
tokenops plan list              # configured bindings + headroom
tokenops plan catalog           # all 30 plans and their list prices
tokenops plan set anthropic claude-ultra
tokenops plan unset cursor
tokenops plan headroom          # live consumption + overage risk
tokenops plan history           # every recorded switch, oldest first
tokenops plan advise            # which tier would have fit the last 30 days
```

#### Which tier fits: `plan advise`

`plan advise` replays the last 30 days (`--days 60`, up to 90) of your
plan's windows against the plan's other tiers, each tier with its overflow
billed at API list price, and no subscription at all. For each option it
shows how often you would have run out and for how long, the fullest window,
and the monthly cost, then recommends one:

```text
anthropic: Claude Max, replayed over the last 27 days (8 windows), confidence medium

  OPTION                               LOCKOUTS  BLOCKED  PEAK            PER MONTH  SAVES
  Claude Pro                           4         24.0d    400% of weekly  $20.00     $80.00
  Claude Max (now)                     0         -        80% of weekly   $100.00    -
  Claude Ultra                         0         -        40% of weekly   $200.00    -$100.00
  Claude Pro + metered overflow        0         -        400% of weekly  $233.06    -$133.06
  API at list price (no subscription)  0         -        -               $284.08    -$184.08

  Recommendation: stay. Claude Max fits: you peaked at 80% of the weekly window.
```

How it works: a weekly reading of 80% on Max is four Pro weeks of demand,
which is 40% of Ultra. Each reading is scaled by the plan in force when
it was taken (`plan history`), so a switch inside the period is handled.
Blocked time runs from the moment a window would have run out to its
reset, with overlapping windows counted once. Overflow and API prices
come from the rate card applied to the usage your plan covered.

Read it with its confidence label. Max (5x Pro every 5 hours and every
week) and Ultra (10x Pro every week, no 5-hour limit) are Anthropic's
stated multiples of Pro's allowance, and Pro's own allowance is unverified
(Anthropic no longer publishes it). So Claude advice is at best `medium`,
and `low` with less than two weeks or two weekly windows of readings. On
Ultra, which has no 5-hour window, nothing says how often Max or Pro would
have run out within 5 hours, so advice there is `low` too. If
you ran out on your current plan, the work you could not do left no
readings, and the larger tiers' figures are a floor.

It is advice: TokenOps never switches a plan. When a cheaper option would
have saved at least $10 and a tenth of the bill with no more than one
lockout a month, the glance, the menu bar and `/api/findings` say so once
as an info finding. `--json`, `GET /api/plans/advice?days=` and
`tokenops_glance (view=plan_advice)` give the same answer. Only Claude's
tiers are replayed so far; ChatGPT's tiers are not modelled as multiples
of each other, and `plan advise` says so.

#### Switching plans

Every `plan set` is recorded with the date it took effect, so a report
over a past period uses the plan in force then rather than today's. If
you switched before telling TokenOps, backdate it:

```bash
tokenops plan set openai gpt-pro-5x --since 2026-09-28
```

`--since` also re-marks that provider's usage recorded as billed per token
since the date as covered by the plan, and writes the change to the audit
log. A start date in the future is refused: record a switch once it has
happened.

`--price` records what you pay per month as your bill shows it — regional
price, tax included — in `--currency` (default: your `money.currency`).
Without it, plan cost uses the catalog's US list price, and `spend` says so.

```bash
tokenops plan set anthropic claude-ultra --price 214.60 --currency EUR
```

A spend limit is recorded with the date it took effect, so an earlier
month keeps its own; `plan history` shows it. A provider billed per token
with an account cap binds to `pay-as-you-go`:

```bash
tokenops plan set anthropic claude-enterprise --spend-limit 1500 --since 2026-09-01
tokenops plan set anthropic claude-enterprise --spend-limit 500 --since 2026-10-01
tokenops plan set fireworks pay-as-you-go --spend-limit 200
```

See [Switching plans](./configuration.md#switching-plans) and ADR 0008.

#### Plans billed at API rates

`claude-enterprise` has no rate-limit window: usage-based Enterprise is
billed at API rates from the first token, so there is no cap to be under.
Headroom is measured against your monthly spend limit.

The best source is Anthropic itself. With the
[Claude subscription telemetry](#tokenops-vendor-usage-setup-claude-subscription) set up,
claude.ai reports what you have spent this month and the limit you are
spending against, and headroom uses those figures — no admin key needed,
and nothing to type in:

```bash
tokenops vendor-usage setup claude-subscription
tokenops plan set anthropic claude-enterprise
```

Without subscription telemetry, give the limit yourself; the binding is refused with
neither, rather than measured against a number nobody chose:

```bash
tokenops plan set anthropic claude-enterprise \
  --spend-limit 5000 --limit-window monthly
```

`plan headroom` says which you are looking at: spend *reported by the
vendor*, or *estimated from token counts*.

From an agent, the same two steps are `tokenops_configure (setting=usage_meter)` and
`tokenops_configure (setting=plan)`. The setup tool never takes the session key as an
argument — it is a claude.ai login, and a tool argument lands in the
agent's transcript. It reads the key from
`TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY` or existing config; the terminal
`setup` reads it without echoing it.

```yaml
plan_limits:
  anthropic:
    spend_limit_usd: 5000
    window: monthly       # or weekly / daily, matching the console
    rate_factor: 0.8      # optional: 20% off list, for a negotiated contract
```

`rate_factor` matters on a discounted contract. TokenOps costs from the
public rate card, so without it your console limit is compared against an
overstatement and nothing says so.

Claude Team is two plans, five times apart: `claude-team-standard` (1.25x
Pro) and `claude-team-premium` (6.25x Pro). Seat-based Enterprise is an
allowance plus metered overflow — two denominators at once — and is not
modelled; naming it says so.

### `tokenops provider {list|set|unset}`

```bash
tokenops provider set anthropic https://api.anthropic.com
tokenops provider list
```

## Running the daemon

### `tokenops daemon {install|uninstall|status|restart}`

Supervises `tokenops start`, the daemon's foreground process (left out of
`--help`, since the unit runs it), so ingestion survives reboot.
macOS writes a LaunchAgent; Linux writes a systemd user unit.
Placeholders in `deploy/launchd` / `deploy/systemd` are filled with this
binary's path and `$HOME`. The daemon listens on `127.0.0.1:7878` by
default and also advertises `tokenops.local` over mDNS.

```bash
tokenops daemon install            # write + load
tokenops daemon install --no-load  # write only
tokenops daemon status
tokenops daemon restart            # re-read config after a config change
tokenops daemon uninstall
```

Install and upgrade wait until the supervised process answers `/healthz`
before reporting success. Supervisor acceptance alone is not treated as a
running daemon; a failed startup exits non-zero with the status and log path
to inspect.

The MCP server your client launches (`tokenops serve`, also left out of
`--help`) does not ingest. A reboot that kills an unsupervised `start`
while the client respawns `serve` is how a 27-day outage stayed invisible.

#### `tokenops daemon restart`

Restarts the supervised unit so it re-reads config. The daemon loads its
configuration once, at boot, so a config write that is not followed by a
restart has not taken effect yet. The command waits up to 20 seconds for the
new process to answer `/healthz`; success means the restart is live rather
than merely accepted by launchd or systemd.

```bash
tokenops daemon restart
```

The commands that write config — `plan set/unset`, `provider set/unset`,
`budget set/unset`, `mode`, `routing rule set/unset`,
`preferred-model set/unset`, `vendor-usage enable` — do this for you. Pass
`--no-restart` when writing several keys in a row, then restart once.

Unsupervised (no unit installed), there is nothing to bounce: those
commands tell you to Ctrl-C and re-run `tokenops start` instead of
claiming a restart that did not happen.

### `tokenops status`

Daemon health + `blockers[]` / `next_actions[]`. Falls back to a
self-report when the daemon cannot be reached so MCP-only deployments
don't hit a dead end. The self-report says why the connection failed
(`reachability.class`: `permission_denied`, `refused`, `timeout` or
`discovery`) and only calls the daemon "not running" when the
connection was refused; see
[Health and readiness](https://team.klarlabs.de/runbook/health#when-tokenops-status-cannot-reach-the-daemon).

## Spend + plan headroom

### `tokenops spend`

```bash
tokenops spend                                   # last 7 days, top 5 by model
tokenops spend --by provider --top 3 --since 24h
tokenops spend --by subagent                     # the session itself vs each kind of subagent
tokenops spend --by session --top 10             # the sessions that used the most
tokenops spend --forecast --forecast-days 14
tokenops spend --include-source=mcp-session     # include MCP activity pings
tokenops spend --json
```

Totals are shown in your `money.currency`, with the US-dollar figure beside
them and a line naming the rate used:

```
  api equivalent:  9370.58 EUR (10640.30 USD) (plan-covered usage at list price)
  plans:           244.14 EUR (your prices, prorated)
  value per plan EUR: 38.4x (api equivalent / plans)
  rate:            1 EUR = 1.1355 USD (ECB reference rate, 2026-09-30); converted amounts move with it
```

Usage is priced in dollars because vendors publish rate cards in dollars,
so a converted figure moves with the exchange rate even when usage does
not. Plans are prorated by day across switches.

#### Who used it

`tokenops spend --by subagent` splits the week between the sessions' own
turns and their subagents, by subagent type (Claude Code's `Explore`,
`general-purpose`, `Plan` and your own agents), so a week that went to
delegated work says so. `--by session` ranks the sessions themselves,
subagents included. Only what a client writes to its transcripts can be
counted: calls a client makes on its own behalf and does not record are
not in these figures.

Each row's **CACHE READ** is the share of its input read from the prompt
cache: the cache reads over all input, cache reads and writes included. A
session or project that keeps a stable prefix reads most of its input from
the cache; a low share is input paid at the full or cache-write rate,
and `tokenops spend waste` says why its caches were rebuilt. A row with no
input shows `—`, not 0%. Codex records cached reads but no cache writes,
so its share counts only what it read. The JSON carries it as
`CacheReadShare` (0 to 1) on each row, and `GET /api/spend/top` and the
`tokenops_spend` MCP tool as `cache_read_share`.

#### Cost per commit

`tokenops spend --by commit` lists what each of your commits cost: the
agent work that led to it, at API prices.

```
Cost per commit since 3 Oct 07:22
  400 commits · median $0.71 · mean $1.84 · 74% of the window's work placed on a commit

  WHEN         REPO           COMMIT            COST  TURNS     WORK  SUBJECT
  6 Oct 07:12  tokenops       3dcf8b0f96       $8.48     32       7h  fix(opencode): read opencode 1.18's store (#548)
  5 Oct 23:48  tokenops       616c5d5611       $1.33      8      13m  docs(menubar): say what a click and a right-cli…

  not yet committed:              $143  1271 turns
  outside a repository:           $115  972 turns
```

Work is joined to commits by place and time: a turn belongs to the
repository its session ran in (from Claude Code's and Codex's transcripts
and opencode's sessions) and to the next commit you made there within a
day. Commits come from your local branches, so a branch merged by squash
and deleted leaves its work on the merged change. What cannot be placed —
work after your last commit, with no commit within a day, outside a
repository, or from a session whose directory is unknown — is listed
apart, never dropped. A `+` marks a cost that leaves out turns on models
without a list price. `GET /api/spend/commits` serves the same report with
commit subjects withheld.

#### Unit costs: per commit, merged PR, outcome and change class

`tokenops spend units` prices finished work rather than tokens, and sets
each figure against your own four weeks before the period (the last 7
days unless you pass `--since`):

```
Unit costs since 8 Oct 00:05, against the 4 weeks before

                      PER UNIT  UNITS   4-WEEK BASELINE   CHANGE
  commit                 $9.00      3   $24.00 (2)          -62%
  merged PR             $13.50      2   $24.00 (2)          -44%
  recorded outcome      $10.50      2   —
  achieved outcome      $21.00      1   —

  By change class, per commit
  feature               $12.00      1   $30.00 (1)          -60%
  bugfix                 $9.00      1   $18.00 (1)          -50%
  refactor               $6.00      1   —

  Share of a plan window, per unit (estimated)
  merged PR          11.2% of the anthropic weekly window  (baseline 20.0%)
  achieved outcome   17.5% of the anthropic weekly window

  Not placed on a unit this period
  commit             84% placed · not yet committed $5.00
  merged PR          84% placed · no pull request merged yet $5.00
  recorded outcome   66% placed · no recorded outcome $11.00

  Work that did not stand: 12% of the period
  reverted within 14 days  $4.00 on 1 commit
  outcome not achieved     $6.00 on 1 execution
```

- **Work that did not stand**: the work on a commit of yours that a later
  commit of yours reverted within 14 days (git revert writes "This reverts
  commit <sha>" into its message), and the work under an execution whose
  recorded outcome is not achieved. It costs the same as work that stood,
  so its share of the period is the expensive failures. A revert by
  someone else, or one written by hand without that line, is not seen.

- **Merged PR**: a pull request of yours that landed on the default branch,
  read from git alone: a squash commit whose subject ends `(#N)` or a
  `Merge pull request #N` commit, on `main` or `origin/main` (whatever
  `origin/HEAD` names). A turn goes to the next one merged in its
  repository within 7 days. No forge is asked, so fetch to see the latest
  merges. Only pull requests with agent work on them are units; the others
  are counted apart.
- **Outcome**: an outcome recorded with `tokenops outcome record` or the
  `tokenops_outcome` tool. A turn goes to it when it ran under the
  execution, the workflow (`tokenops_prepare_work`'s `workflow_id`) or the
  session the outcome names. The cost per achieved outcome carries the
  work on every judged execution, including the ones that failed.
- **Change class**: from the Conventional Commits type of each commit's
  subject: `feat` is feature, `fix` bugfix, `refactor` and `perf`
  refactor, `test`, `docs`, and `chore`, `ci`, `build`, `style`, `revert`
  chore. A subject with no type is *other*.
- **Share of a plan window**: on a flat-rate plan the vendor reports
  windows as percentages, never tokens, so TokenOps measures how many
  tokens one percent of your longest window held from your own windows
  that reset in the last five weeks (each at least 5% full; the median).
  It is an estimate, and says so.

Every join is a heuristic, so the work no unit took is listed by why.
`--json`, `GET /api/spend/units` and `tokenops_spend` with `view: units`
return the same report; it holds derived figures only, never subjects,
paths or session ids. The team plane does not receive these figures yet:
its upload carries per-day buckets, which have no field for a count of
pull requests or outcomes.

#### Claude Code in your organisation, as Anthropic reports it

`tokenops spend claude-code` reads Anthropic's own Claude Code analytics
for your organisation, one UTC day at a time (the last 7 days, or
`--days`, up to 90), from every report a configured key reaches:

```
Claude Code in your Console organisation, 2026-10-26 to 2026-11-01 (UTC)

  DAY        USERS SESSIONS COMMITS  PRS    +LINES    -LINES  ACCEPTED   TOKENS    EST. $
  2026-10-31     4       12       5    2       900       300       90%     1.2M    $14.20
  ...
```

Anthropic reports it per user; TokenOps sums it and keeps no names or
addresses. ACCEPTED is the share of Claude Code's edit proposals (Edit,
MultiEdit, Write, NotebookEdit) that were accepted, and EST. $ is
Anthropic's estimated cost. `--json` and `tokenops_spend` with
`view: claude_code` return each day's figures by tool and by model.

| Key | Reads |
|---|---|
| Claude Console admin key: `vendor_usage.anthropic.admin_key`, `TOKENOPS_ANTHROPIC_ADMIN_KEY` | Claude Code billed through the Console organisation, with tokens and Anthropic's estimated cost by model |
| Claude Enterprise Analytics API key: `vendor_usage.anthropic.analytics_key`, `TOKENOPS_ANTHROPIC_ANALYTICS_KEY`, or `tokenops vendor-usage enable anthropic-analytics --analytics-key -` | Claude Code by the Enterprise organisation's claude.ai sign-ins. No cost in this report, and about a day behind; days not published yet are listed as pending |

With both keys, each report is printed on its own. Pro, Max and Team
subscriptions have no such report; TokenOps reads those from each
machine's transcripts. `GET /api/spend/claude-code` returns the same.

#### Avoidable usage: one waste taxonomy in dollars and window share

`tokenops spend waste` says how much of the period's usage was avoidable,
class by class, in API-equivalent dollars and, on a flat-rate plan, as a
share of the plan's window (the last 7 days unless you pass `--since`):

```
Avoidable usage since 8 Oct 01:52

  used           4.5M tokens     $5.31   19.6% of the anthropic weekly window
  avoidable      873k tokens     $1.38   3.8% of the anthropic weekly window  (19% of usage)

  CLASS                              TOKENS     API $  WINDOW  TURNS  GUARDRAIL
  redundant re-reads                    18k     $0.02    0.1%      2  read guard
  cache-miss rebuilds                   70k     $0.44    0.3%      1  none yet
    idle gaps over 1h                   70k     $0.44    0.3%      1
  over-tiered model                    259k     $0.28    1.1%      4  model routing
  retry loops and runaway growth       122k     $0.15    0.5%      2  none yet
  oversized context                       0     $0.00       —      0  compact tip
  abandoned sessions                   403k     $0.49    1.8%      6  session budget

  Realized $0.00 (holdout: no holdout comparison has concluded yet): 0% of what could be saved

  Next guardrail: session budget, still gathering evidence, addresses $0.49
```

Each class reads a source TokenOps already keeps:

- **Redundant re-reads**: files the read guard saw read again, in full and
  unchanged, and let through because it only observes.
- **Cache-miss rebuilds**: a turn that wrote to the prompt cache more than
  half of what the turn before had cached (the cache expired or was
  invalidated). Priced as writing that part instead of reading it. Each
  rebuild is split by why it happened, from token counts, times and model
  IDs only:
  - *model switches*: the turn ran on another model than the one before; a
    cache belongs to one model.
  - *idle gaps past the cache lifetime*: the turn's request went out (its
    recorded time less how long it took, so a long generation is not
    counted as idle) longer after the turn before than the cache lives: an hour when the session wrote its cache
    with the extended lifetime, five minutes otherwise. The row names the
    lifetime most of them outlived ("idle gaps over 1h").
  - *prompt prefix changes*: any other rebuild after the turn before read
    from the cache. Something early in the prompt changed: a timestamp, the
    tool or MCP set, edited instructions. A tool-set change is not told
    apart from the rest, because Claude Code's transcript records tools
    added after the prefix, not changes to the tool list it starts with.
  - *unexplained*: the turn before had no cache hits, or no times were
    recorded.

  Under the prompt prefix changes, `likely:` names what the
  [context lint](#tokenops-coach-lint-context) finds that could explain
  them, with how many of the rebuilds it was joined to:

  ```
      prompt prefix changes                 3k     $0.02    0.1%      3
        likely: SessionStart hook running `date` (3 of 3 rebuilds)
  ```

  The join is by place, not by proof. A rebuild is joined to an item when
  the item sits in the prefix the rebuilt session loaded: a volatile line
  in a context file (CLAUDE.md, AGENTS.md and what they import) or a
  SessionStart hook that prints something changing, in your own files or
  in the repository the session ran in, loaded by the harness the session
  ran in. A UserPromptSubmit hook is left out: its output is appended
  after the turns before it and does not rebuild their cache. The lint
  reads the files as they are now, so an item added or fixed since the
  rebuild can still be joined to it, and no record says which byte of the
  prefix changed; that is why it says "likely". A session whose directory
  no client recorded is joined to your own files only. The JSON carries
  the items as `likely` on the prompt-prefix-change cause, with the method
  as `likely_method`; each names the item, never its path.

  A turn whose context fell to under half of the turn before's is a
  compaction; its new cache is by design and not counted. Codex sessions
  record cached reads but no cache writes, so their misses are not counted
  here.
- **Over-tiered model**: turns that stayed on a model after the coach
  advised a cheaper one for that kind of work, priced as the difference to
  the advised model.
- **Retry loops and runaway growth**: what the waste detector finds in each
  workflow: an identical prompt sent again, one agent's steps past the loop
  limit, context growing faster than the per-step limit.
- **Oversized context**: context carried above the line a session should
  compact at, or above the context ceiling.
- **Abandoned sessions**: sessions over for a day with no commit of yours
  within a day of any turn and no recorded outcome, the same joins as
  `tokenops spend units`. A session outside a repository is not judged.

The classes overlap in what they look at, so each token is counted in at
most one, in the order listed. Dollars are each turn's API-equivalent value
in proportion to its tokens; the window share is measured as unit costs
measure it. Beside the avoidable total, **realized** is what the coach's
interventions saved over the same period: what the holdout measured, plus
the re-reads the guard refused. The guardrails are ranked by their
graduation (see `tokenops coach`): one whose evidence is ready comes first,
then by the avoidable dollars it addresses, and the next one is named with
the command that takes the step. Nothing is switched on for you.

The daemon runs the week's report once a day, and the glance, the menu bar
and `GET /api/findings` show it as one finding, in the currency you pay in.
On a flat-rate plan the avoidable usage cost nothing beyond the plan, so it
leads with the share of a window it took and lists each class as its share
of the same window: "Last 7 days: ~9.2% of a weekly window avoidable", with
"cache-miss rebuilds 6.4% · over-tiered model 2.2% · abandoned sessions 0.6%"
below it. Where a provider bills per use (an API key, pay as you go), the
avoidable part is money spent and leads in dollars ("~$4.10 avoidable"), or
follows the plan's share ("…, plus $4.10 billed per use"). Usage stored with
a cost counts as billed; usage without one only on a provider no plan's
windows cover. With neither reading, the dollars say they are API prices.
When cache rebuilds are among them, the finding names what most came after
("Most cache rebuilds came after idle gaps over 5 minutes."), or what they
came most often after when no cause has a majority; an unexplained top
cause is not named. What the guardrails already saved follows the classes
("Guardrails already saved ~$1.50 at API prices") and is left out while it is nothing. The
report's `billed_usd`, on the total and on each class, is the billed part.
`--json`,
`GET /api/spend/waste` and `tokenops_spend` with `view: waste` return the
same report; it holds derived figures only.

#### Spend by project, client or cost centre

`tokenops spend --by project` (or `client`, `cost_center`) sums the
window's spend per value of an allocation tag, from the
[allocation rules](./configuration.md#allocation-rules-allocation) in
your config:

```
Spend by client since 8 Oct 09:00
  81% of the window's work carries a client

  VALUE                    API EQUIV    BILLED  SESSIONS  TURNS    TOKENS CACHE READ
  Acme Corp                   $41.20     $0.00         6    212      3.1M        91%
  globex                      $12.75     $2.10         2     58      880k        64%
  (untagged)                  $12.40     $0.00              97      1.2M        88%
    $3.80 of it from sessions whose directory no client recorded
```

CACHE READ is the share of each value's input read from the prompt cache,
as on `--by session` (`cache_read_share`, 0 to 1, in the JSON).

Each session is matched by the directory and branch its client recorded,
the repository that directory is in and the harness it ran in; work no
rule tags stays visible as untagged. `--json` gives the report, which
`GET /api/spend/tags` and the `tokenops_spend` MCP tool (`view: tags`)
serve too. The rules are evaluated on this machine, and the tags never
leave it: no team upload carries them.

#### Export to FinOps tools (FOCUS)

`tokenops spend export` writes spend and usage as
[FOCUS](https://focus.finops.org/) 1.4 CSV, the billing format FinOps tools
such as Vantage, CloudZero and Cloudability ingest. `--focus-version 1.2`
writes FOCUS 1.2 for a tool that does not read 1.4 yet:

```bash
tokenops spend export --format focus > september.csv          # this month so far
tokenops spend export --since 2026-09-01T00:00:00Z --until 2026-10-01T00:00:00Z -o september.csv
tokenops spend export --focus-version 1.2 -o september-1.2.csv
```

FOCUS 1.4 requires `BilledCost` to be what the vendor invoiced, never an
estimate, and 0 on rows from anyone who issues no invoice, so that a
dataset merged with the vendor's own counts no charge twice. TokenOps
issues no invoice, so in 1.4:

| What | FOCUS 1.4 rows |
|---|---|
| A plan's fee | `Purchase`, `Recurring`, prorated over the days it was in force and split at calendar months; the monthly price is the `ListUnitPrice`, the prorated fee is `x_EstimatedCost` |
| Usage a plan covered | `Usage`, a row per kind of token at its API list rate; `ContractedUnitPrice` and `ContractedCost` are 0, so ListCost against ContractedCost is what the plan was worth |
| Usage billed per token | `Usage`, a row per kind of token at its API list rate; what it cost is `x_EstimatedCost`, split across the kinds by list price |

`BilledCost` and `EffectiveCost` are 0 on every row. The kinds of token
are input, cache read, cache write (5-minute and 1-hour) and output:
`SkuPriceId` is the model and the kind (`claude-opus-5/cache-read`),
`ListUnitPrice` the rate per million tokens on the rate card of that day,
and `ListCost` the rate times `PricingQuantity`. A model with no rate is
one row whose unit prices are written as 0, which its `ChargeDescription`
says. `ServiceProviderName`, `HostProviderName` and `InvoiceIssuerName`
are the vendor.

In FOCUS 1.2, usage is one row per model and day, and TokenOps' estimate
is the `BilledCost` and `EffectiveCost`:

| What | FOCUS 1.2 row |
|---|---|
| A plan's fee | `Purchase`, `Recurring`, prorated over the days it was in force and split at calendar months; your price where you gave one, the US list price otherwise |
| Usage a plan covered | `Usage` with `BilledCost` and `EffectiveCost` 0 and `ListCost` at the API price, so ListCost against EffectiveCost is what the plan was worth |
| Usage billed per token | `Usage` at what it cost |

In both versions, usage is per UTC day, provider and model. Tokens are the
`ConsumedQuantity` (`Tokens`); `PricingQuantity` counts millions of tokens,
the unit API prices are published in. `ServiceCategory` is
`AI and Machine Learning`, `ServiceSubcategory` `Generative AI`, `SkuId`
the model or plan. A plan's charges and per-token charges are separate
billing accounts (`anthropic/subscription`, `anthropic/pay-as-you-go`).
Calendar months stand in for billing periods.

With [allocation rules](./configuration.md#allocation-rules-allocation)
configured, the part of a day's charge that tagged work took is its own
row, its tags in `Tags` as a JSON object (`{"client":"Acme Corp","project":"storefront"}`);
the rest stays a row with `Tags` empty, and the rows still add up to the
charge. The keys are not prefixed: FOCUS asks for no prefix when a
dataset has one user-defined tag scheme.

Only derived figures are written: providers, models, plans, counts, money
and your own allocation tags. No session title, path or prompt is in it. `GET /api/spend/export`
serves the same CSV from the daemon; `?version=1.2` asks for FOCUS 1.2.

### `tokenops budget {list|set|unset}`

A budget is a calendar window (`daily`, `weekly` or `monthly`; new
budgets default to monthly) and a ceiling, upserted by name. In active
[mode](#tokenops-mode-passive-active) the daemon evaluates them and
reports breaches, warning at `--warn-at` (default 0.75 of the limit) and
alerting at `--crit-at` (default 0.95).

```bash
tokenops budget list --json
tokenops budget set monthly --limit-usd 200
tokenops budget set weekly-tokens --basis tokens --limit-tokens 50000000 --window weekly
tokenops budget set planner --limit-usd 50 --agent-id planner   # one agent only
tokenops budget set acme --basis equivalent --limit-usd 300 --tag client=acme   # one client's work
tokenops budget set mine --provider fireworks --limit-usd 50 --mode hard   # one provider's bill
tokenops budget unset monthly
```

On a flat-rate plan real spend is $0 at the margin, so a USD limit can
never trip: use `--basis tokens` with `--limit-tokens`, or
`--basis equivalent` to watch the API list-price value the subscription
absorbed. `--workflow-id` and `--agent-id` scope a budget to one workflow
or agent, and `--tag key=value` to the work an
[allocation rule](./configuration.md#allocation-rules-allocation) tags
(project, client or cost_center). A tagged budget gets threshold alerts,
not forecasts.

`--provider <id>` limits a budget to what one provider billed: the
provider whose bill each request counts against, not the vendor of the
model that answered. A request Claude Code sends through a gateway counts
for the gateway that billed it. Use it when a provider account is shared
with others and you want a limit of your own on it, whether or not the
provider sets a per-member cap; the budget is yours and independent of any
cap the provider reports. The id is one from the provider registry
(`tokenops provider list`); an unknown one is refused with the ids it was
likely meant to be. Scopes narrow together: `--provider` with `--tag`
counts that tag's work the provider billed, and the same holds with
`--workflow-id` and `--agent-id`. `budget list`, the glance and the menu
bar's tab for that provider say whose bill a budget counts.

A budget can hold several windows at once, say $10 a day and $100 a
month. `--add-window` adds one; on a budget with several windows,
`--window` names the one an edit changes, and `unset --window` removes
one. Each window is **soft** or **hard**:

- **Soft** (the default) notifies. The coach says, once per window, when
  it reaches its warning and critical thresholds and when it is spent,
  through the same tips you can snooze or answer and within the daily
  interruption budget. `tokenops budget list` and the glance show each
  window's share used.
- **Hard** (`--mode hard`) is enforced by a spend
  [boundary](#tokenops-boundaries-status-promote-demote-headroom-pause-loop-breaker) named `budget-<name>-<window>`, which
  asks (`--require ask`, the default) or refuses (`--require deny`) before
  further steps once the window is spent. Like every boundary it starts
  in shadow, recording what it would have done, until you promote it.

```bash
tokenops budget set team --limit-usd 100                      # $100 a month, soft
tokenops budget set team --add-window --window daily --limit-usd 10 --mode hard
tokenops boundaries promote budget-team-daily                 # now it binds
tokenops budget unset team --window daily
```

The daemon API (`POST /api/budgets`) and the `tokenops_budget_set` tool take
the same fields (`add_window`, `remove_window`, `mode`, `require`), but a
budget that holds a hard window is changed or removed only from a terminal:
the agent a hard window limits can reach both.

### `tokenops scorecard`

Operator wedge KPI scorecard. Three classic KPIs (FVT, TEU, SAC)
plus five agent-workflow KPIs added in v0.19–v0.21:

  - **CHR** — Cache Hit Ratio (≥90/70/50)
  - **CGR** — Confirmation Gate Rate (≤10/20/30)
  - **RGR** — Regenerate Rate (≤5/10/20)
  - **TCS** — Tool Success Rate (≥95/85/70)
  - **DAR** — Destructive Action Rate (≤0.5/2/5)

All grade A–F against tuneable thresholds. CHR is computed from
events.db; CGR + RGR from JSONL prompt extracts; TCS + DAR from
JSONL `tool_use` + `tool_result` blocks. Returns `warming_up`
with a 3-step activation checklist when no real data backs the KPIs.

Each measured KPI is set against **your own trailing baseline**: the 28
days before the window, the same stretch unit costs use. The line shows
the baseline, the change and its direction given which way the KPI is
healthy, so a falling regenerate rate reads `better` and a falling cache
hit ratio `worse`; a move under a percentage point (for first-value time,
a second or 5%) is `unchanged`. A KPI you override with `--fvt-seconds`,
`--teu-pct` or `--sac-pct`, or one not measured in both windows, has no
comparison. It is you against yourself only: the scorecard shows no team
or peer figure and ranks nobody.

```
Against your own baseline: the 28 days before (Sep 10 – Oct 8)

FVT — First-Value Time (seconds):           5.4 [A] · baseline 140.1 (-134.7, better)
SAC — Spend Attribution Completeness (%):   84.2 [B] · baseline 96.2 (-12.0, worse)
CHR — Cache Hit Ratio (%):                  97.5 [A] · baseline 98.5 (-1.0, worse)
```

`--json`, `GET /api/scorecard` and the `tokenops_scorecard` MCP tool carry
the same: `self_baseline` (the window), and on each KPI `polarity` and
`vs_baseline` (`baseline`, `delta`, `direction`).

With [allocation rules](./configuration.md#allocation-rules-allocation)
configured, a line under SAC says how much of the period's spend (at API
prices) they tag: `allocation tags on 81.0% of spend (project 81%, client 64%)`.
It is reported, not graded. SAC itself counts calls traceable to a
session, workflow or agent, which every tagged call already is.

Below the grades, **Plan use** lists each provider's plan utilization
(the average peak of its longest plan window that reset in the period)
and coverage (the share of its tokens a plan covered). They are reported,
not graded: a seat used at 40% is a fact for whoever pays for it, not a
fault. Against the baseline they move `up` or `down`, never better or
worse.

```
Plan use (last 7 days):
  anthropic    utilization 64% (weekly, 1 window) (baseline 52%, up) · coverage 93% of tokens on a plan (baseline 93%, unchanged)
  openai       utilization 41% (weekly, 1 window) · coverage 100% of tokens on a plan
  openrouter   utilization n/a (no plan window reset yet) · coverage 0% of tokens on a plan
  all          coverage 81% of tokens on a plan
```

Under it, **Unit costs** gives the cost per commit, per merged pull
request and per achieved outcome over the same days, against the four
weeks before (see [`tokenops spend units`](#unit-costs-per-commit-merged-pr-outcome-and-change-class)).
Reported, not graded: a dearer pull request may be a harder one. The
direction reads cheaper as `better` and a change under 5% as `unchanged`.

```
Unit costs (last 7 days, against the 4 weeks before; API prices):
  commit             $9.00 each · 3 units · baseline $24.00 (-62%, better)
  merged PR          $13.50 each · 2 units · baseline $24.00 (-44%, better)
  achieved outcome   $21.00 each · 1 unit
```

```bash
tokenops scorecard --days 30        # text table
tokenops scorecard --days 30 --json # structured output
```

### `tokenops task {start|done|list}`

Operator-marked task boundaries persisted to `~/.tokenops/tasks.jsonl`.
`task list --metrics` enriches every task with the events-store
rollup over its `[StartedAt, CompletedAt]` window: turns,
input/output tokens, cache-aware cost, TTFUO, cost-per-turn.

```bash
tokenops task start "fix auth middleware"
# ...do the work...
tokenops task done
tokenops task list --since 7d --metrics
tokenops task list --since 30d --json   # MCP-host friendly
```

## Vendor-side usage

### `tokenops vendor-usage setup claude-subscription`

Connects claude.ai's subscription telemetry — what the app itself shows: the
5-hour and 7-day utilisation on Pro and Max, or on Claude Enterprise, what
you have spent this month against your spend limit. It is the only
authoritative reading TokenOps can get for a Claude subscription;
everything else is estimated.

The canonical setup name is `claude-subscription`. It is intentionally not
`claude-code`: `claude-code-jsonl` is the separate per-turn coding-activity
source, while subscription telemetry can cover Claude web, Claude Code,
Pro, Max, Team/Business, and Enterprise. The former
`claude-usage-meter` command spelling remains an accepted compatibility alias.

Only what Anthropic reports is recorded. A window your plan does not have
is left out, never shown as 0%, and a reply in a shape this version cannot
read is logged and skipped rather than stored as zeros.

```bash
tokenops vendor-usage setup claude-subscription
```

By default it reads the session from the browser you are already signed in with —
Chrome, Arc, Brave, Edge, Chromium or Firefox — so there is nothing to copy
or paste. On macOS the system asks you to allow reading the browser's
keychain entry; that prompt is the permission step. TokenOps reads only the
Claude session and Cloudflare clearance cookies, from a copy of the store, and never writes to your browser
profile.

```bash
tokenops vendor-usage setup claude-subscription --browser Chrome  # pick one
tokenops vendor-usage setup claude-subscription --paste           # type it instead
tokenops vendor-usage setup claude-subscription --paste-request   # import a usage request
```

Some operating systems protect browser cookie databases even after the
keychain is unlocked. `--paste-request` is the portable fallback: open
`claude.ai/settings/usage`, select the content-free
`/api/organizations/.../usage` GET in Developer Tools → Network, choose
**Copy as cURL**, and paste it at the hidden prompt. TokenOps rejects other
endpoints and extracts only `sessionKey`, a closed allowlist of Cloudflare
session cookies (`cf_clearance`, `__cf_bm`, and `_cfuvid`), `User-Agent`, the
organization ID, and a closed allowlist of non-secret browser client hints
(`Accept-Language`, `Priority`, `Sec-CH-UA*`, and `Sec-Fetch-*`). Those hints
let the supervised daemon replay the same browser-shaped request Cloudflare
accepted. Authorization and arbitrary headers, request bodies, the copied
command, and responses are never stored or replayed. Every imported cookie is
treated as a credential and redacted from CLI, MCP, and API configuration views.
The clearance is short-lived, so repeat setup if source health later reports
that Claude's bot check refused it.

With no browser session found it falls back to explaining where the cookie
is and reading it without echoing it into your scrollback. Either way it
**verifies the key against Anthropic**, prints your real percentages, and
only then writes config. A mistyped or expired key fails here rather than
sitting in config producing nothing — session cookies rotate, so a stale
copy is the usual cause.

When a supervised daemon is installed, setup restarts it automatically and
waits for the command to complete. The MCP server watches the same config and
reloads it in-process, so there is no MCP-client restart step. `--no-restart`
is available only for advanced workflows that deliberately batch several
configuration writes.

From an agent, `tokenops_configure (setting=usage_meter)` connects the meter
from a key already in `TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY` or the config.
It never reads a browser and never asks for the key in the conversation;
without a key it tells the agent to ask you to run the command above.

#### When TokenOps asks for Keychain access (macOS)

Only a setup command you run can make macOS ask, and each says which item
it reads before it does:

| What | Keychain item | Prompts? |
|---|---|---|
| `vendor-usage setup claude-subscription` | your browser's `… Safe Storage` (for example `Chrome Safe Storage`): the key it encrypts cookies with, used to read two claude.ai cookies, `sessionKey` and `cf_clearance` | yes, when you run it; one refusal stops it asking for other browsers; `--paste` or `--paste-request` skips the Keychain |
| `vendor-usage setup claude-code --keychain` | `Claude Code-credentials`: Claude Code's own sign-in, sent only to `api.anthropic.com` | yes, when you run it; without `--keychain` only `~/.claude/.credentials.json` is read |
| the daemon, for a meter connected from the browser or Claude Code connected with `--keychain` | the same items | **never**: it reads quietly, and when macOS would ask (after an upgrade, or when Claude Code renews its sign-in) it skips the read and the stale-reading finding says to run the setup again |

Nothing else reads the Keychain: not `init`, not an agent through the MCP
tools, and not the daemon for a session you pasted. `keychain: {disabled:
true}` in the config, or `TOKENOPS_KEYCHAIN_DISABLED=1`, turns every
Keychain read off; Firefox, which keeps its cookies outside the Keychain,
and the credentials file still work. macOS ties **Always Allow** to the
program it was given to, so after a TokenOps upgrade the setup may ask
again.

Prefer this over `vendor-usage enable claude-subscription --session-key`,
which writes whatever you give it without checking.

### `tokenops vendor-usage status`

Reads config + counts source-tagged envelopes per source over a
configurable window. Surfaces a hint per source pointing at the
missing config knob when a source is dark.

```bash
tokenops vendor-usage status                 # 24h window
tokenops vendor-usage status --window 7d --json
```

### `tokenops vendor-usage backfill`

One-shot pull of historical Anthropic Admin API usage into the
local store. Deterministic envelope IDs — re-running or running
alongside the live poller never double-counts.

```bash
tokenops vendor-usage backfill --hours 168   # full week (Admin API cap)
tokenops vendor-usage backfill --hours 24 --dry-run
```

### Fireworks account

When a Fireworks key is on the machine, the daemon reads the account's
spend and limit from Fireworks' documented API every 15 minutes. Nothing
needs setting up, and `plan headroom` shows Fireworks as `pay-as-you-go`
until you bind another plan.

- **The key** is `FIREWORKS_API_KEY`, or the key Nexus CLI (FireConnect
  before 0.10.0) connected Claude Code with: the `X-Fireworks-Api-Key`
  header it writes into Claude Code's settings, or, on older versions, its
  `apiKeyHelper` (TokenOps runs that helper only when it is `nexus key
  export` or `fireconnect key export`). Otherwise a Fireworks key that
  opencode or Codex uses is taken. The key is fetched for each reading
  and is never stored or logged.
- **The figures** on a company account are your own spend against your
  per-user cap (`/users/{you}/usageLimits`). On your own account they are
  the month's spend (`/billing/summary`) against the `monthly-spend-usd`
  quota. That figure is the whole account's, every member together, so
  where it is shown it is labelled "Account spend" with
  `account <id>, all members` (`spend_scope: account` and `spend_scope_label` in the API), not
  as your own extra usage. The account comes from the key (or Nexus CLI's
  files). Your user comes, in order, from `--user-id`
  (`vendor_usage.fireworks.user_id`), from `minted-key.json` in
  `~/.nexus` (`~/.fireconnect` on older versions), from
  `nexus status --json` when it names your user, or by email: the
  account's members are looked up by `--email`
  (`vendor_usage.fireworks.email`), else by the email
  `nexus status --json` reports, else by one read from an email field in Nexus CLI's
  `config.json`, `key-storage.json` or the JSON files under `cli/` (only
  such fields; key fields are never read). A user you set that the
  account does not have, or an account that is not your key's, is named
  as the reason instead of showing the account's figure as if it were
  yours. The user found is kept in the daemon's memory
  for a day, or until the key changes, and never written to your config.
  When your own figure cannot be read, the account's is shown and says
  why, in `tokenops glance`'s card ("account-wide: your user wasn't
  found — set it with
  `tokenops vendor-usage enable fireworks --user-id <id>`"), in
  `tokenops vendor-usage status` and as `spend_scope_reason` in the API.
- **A block.** When your cap blocks requests, the card says "Blocked
  until 1 Nov" (your local date) and the API carries `limit_reached` and
  `blocked_until`.
- **Calls** go to `api.fireworks.ai` only. Turn them off with
  `tokenops vendor-usage enable fireworks --disable`.

### Other gateways and API vendors

Every provider you pay per token for and used this month shows in
`plan headroom` as `pay-as-you-go`, with spend estimated from usage.
Where the vendor documents an account endpoint, TokenOps also reads the
vendor's own figures every 15 minutes:

<!-- begin generated: provider-accounts -->
| Vendor | Endpoint | Shows |
|---|---|---|
| ai& | `GET /logs?range=30days` | the organisation's USD spend in the last 30 days, summed from its request logs |
| Atlas Cloud | `GET /public/v1/balance` | the account's available USD balance |
| Amazon Bedrock | Cost Explorer `GetCostAndUsage` | this month's Bedrock spend, every 8 hours (Cost Explorer bills $0.01 a request) |
| Deepgram | `GET /v1/projects/{id}/balances` | prepaid USD balance left, across the key's projects |
| DeepInfra | `GET /payment/checklist` | spend since the last invoice, the limit, prepaid credit |
| DeepSeek | `GET /user/balance` | prepaid USD balance left |
| GroqCloud | `GET api.groq.com/platform/v1/organizations/{org}/activity` | the organisation's GroqCloud spend this UTC month |
| Hugging Face | `GET /api/settings/billing/usage-v2` | Inference Providers charges this month, against the spending limit when set |
| Charm Hyper | `GET hyper.charm.land/v1/credits` | Hypercredits left |
| Charm Hyper | the same, with hyper.charm.land's session | Hypercredits left |
| LithosAI | `GET /api/me`, `/api/billing`, `/api/billing/spend` (console.lithosai.cloud) | the organisation's prepaid balance and its spend this UTC month |
| Xiaomi MiMo | `GET /api/v1/balance`, `/api/v1/tokenPlan/detail`, `/api/v1/tokenPlan/usage` | the balance (in its currency) and the Token Plan's monthly credits used |
| Moonshot (Kimi API) | `GET /v1/users/me/balance` | prepaid USD balance left |
| Neuralwatt | `GET /v1/quota` | prepaid USD credit left, and the subscription's kWh allowance used this period |
| OpenAI | `GET /v1/organization/costs` | the API organisation's spend this month (Administration API) |
| OpenRouter | `GET /api/v1/key` | the key's spend, against its credit cap when it has one |
| Poe | `GET /usage/current_balance`, `GET /usage/points_history` | the point balance left and the points spent over the last 30 days (points, not dollars) |
| Replicate | `GET /api/{users|organizations}/{name}/invoices` | spend this month, and the prepaid credit left |
| TypeSafe | `POST /settings/billing` (getBillingOverview action) | spend this billing cycle and the credit balance left |
| Venice | `GET /api/v1/billing/balance` | USD balance left, and the DIEM epoch allocation used when staking |
| Vercel AI Gateway | `GET /v1/credits` | the team's credit balance |
| xAI | `GET /v1/billing/teams/{team_id}/prepaid/balance`, `POST /v1/billing/teams/{team_id}/usage` | the team's posted prepaid USD credit, and its spend over the last 30 days |
| X API (console.x.com) | `GET /api/me`, `/api/accounts/{id}/credits` (console.x.com) | prepaid credit left, purchased plus free (below zero when overdrawn) |
<!-- end generated: provider-accounts -->

Coding plans report their usage windows instead. A provider whose reader
reports one, with no plan bound, shows as `subscription` with the
vendor's windows, busiest first:

<!-- begin generated: provider-subscriptions -->
| Vendor | Endpoint | Shows |
|---|---|---|
| Abacus AI (ChatLLM) | `GET /api/_getOrganizationComputePoints`, `POST /api/_getBillingInfo` | compute credits used this billing month, resetting at the next billing date |
| Alibaba Cloud Coding Plan | `POST /data/api.json` (queryCodingPlanInstanceInfoV2, Model Studio or Bailian console) | Coding Plan 5-hour, weekly and monthly quotas (where the console accepts the plan's API key) |
| Alibaba Cloud Coding Plan | `POST /data/api.json` (console gateway, with the console's sec_token) | Coding Plan 5-hour, weekly and monthly quotas |
| Alibaba Cloud Token Plan | `bl usage token-plan --output json` | the 5-hour, weekly and monthly windows, from the signed-in Bailian CLI |
| Alibaba Cloud Token Plan | `POST /data/api.json` (tokenplan/personal/api/v2/usage, or GetSubscriptionSummary for a Team plan) | Personal/Solo 5-hour, weekly and monthly windows, or a Team plan's credit pool |
| Amp | `amp usage` | subscription agent usage and orb hours, Amp Free's daily allowance, individual credits |
| Amp | `POST ampcode.com/api/internal?userDisplayBalanceInfo` | the same figures, with an access token |
| Antigravity | the running app's local language server (`RetrieveUserQuotaSummary` on 127.0.0.1) | 5-hour and weekly quota for Gemini models and for Claude and GPT models, while the app runs |
| Augment Code | `auggie account status` | credits used of the month's allowance |
| Augment Code | `GET app.augmentcode.com/api/credits` | the same credits, with the app.augmentcode.com session |
| Chutes | `GET /users/me/subscription_usage` | the 4-hour and monthly caps |
| ClinePass | `GET /api/v1/users/me/plan/usage-limits` | 5-hour, weekly and monthly windows |
| Codebuff | `POST /api/v1/usage` | credits used against the quota until it resets; with the CLI's sign-in, the weekly rate limit too |
| CodeRabbit | `coderabbit usage` | reviews this billing period, a count with no allowance (no percentage) |
| Command Code | `GET /internal/billing/credits`, `/internal/billing/subscriptions` (api.commandcode.ai); with an API key, the same under `/alpha` | 5-hour and weekly limits, the monthly credit grant used, and purchased credits |
| Devin | `GET app.devin.ai/api/<org>/billing/quota/usage` | the daily and weekly quota used, and the extra-usage balance |
| DevPass (LLM Gateway) | `GET /v1/key` | the cycle's plan credits and the premium weekly window; on pay-as-you-go, the key's spend against its limit |
| Doubao | `arkcli usage plan --format json` | the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan), from the signed-in arkcli |
| Doubao | `POST /?Action=GetCodingPlanUsage` | the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan) |
| ElevenLabs | `GET /v1/user/subscription` | the subscription's credits used this period |
| Factory (Droid) | `GET /api/billing/limits`, `/api/organization/subscription/usage` (api.factory.ai) | 5-hour, weekly and monthly windows (and the Core fallback's), or the Standard and Premium token allowances; the extra-usage balance |
| Factory (Droid) | the same, with app.factory.ai's session | the same windows and balance |
| GitKraken AI | `GET api.gitkraken.dev/v1/ai-tasks/usage` | weekly AI credits, and the organization's pool with GITKRAKEN_ORG_ID |
| Grok (SuperGrok) | `GET cli-chat-proxy.grok.com/v1/billing?format=credits` | the SuperGrok credit window used, and the prepaid balance |
| Grok (SuperGrok) | `POST grok.com/grok_api_v2.GrokBuildBilling/GetGrokCreditsConfig` (gRPC-web) | the credit window used |
| Helmcode | `GET /api/usage/quota` | each model's token quota used, and the prepaid balance |
| IBM Bob | `GET /admin/v1/profile`, `GET /admin/v1/teams/{team}/users/{user}` | Bobcoins used this month against the team budgets |
| JetBrains AI | the IDE's `options/AIAssistantQuotaManager2.xml` and `idea.log` | monthly AI credits used, from the most recently used IDE |
| Kilo | `GET /api/trpc/user.getCreditBlocks,kiloPass.getState` | Kilo Pass credits used this billing period, and prepaid credit left |
| Kimi Code | `GET /coding/v1/usages` | 5-hour, weekly and monthly windows |
| Kiro | `kiro-cli chat --no-interactive /usage` | monthly plan credits used, and bonus credits |
| Kiro | `POST GetUsageLimits` (CodeWhisperer) | plan credits used this month, and overage credits against their cap |
| Langdock | `GET /api/trpc/usageSettings.getPersonalUsage` (app.langdock.com) | the included 5-hour session and weekly limits used |
| LongCat | `POST /api/pay/quota/metering/token-packs/summary` | the token pack's share used, and the tokens left with pending fuel packs |
| Manus | `POST /user.v1.UserService/GetAvailableCredits` | monthly and daily-refresh credits used, and the credit balance |
| MiniMax Token Plan | `GET /v1/token_plan/remains` | the interval and weekly windows |
| Mistral | `GET /api/billing/v2/usage`, `/subscription`, `/api/billing/credits` (admin.mistral.ai) | the included-API and Vibe allowances' shares used this month, and the credit balance in its currency |
| Muse Code | `POST api.meta.ai/muse-code/key` | the 5-hour and weekly quota used |
| Muse (muse.ai) | `POST muse.ai/` (the page's fetchSubscriptionAction server action) | the weekly token allowance used |
| Notion AI | `POST /api/v3/getCreditRateLimitStatus` | the Notion AI allowance used, rolling and this billing period (Business and Enterprise workspaces) |
| Nous Portal (Hermes Agent) | `GET /api/oauth/account` | the monthly credit grant used this period and the top-up credit left |
| Ollama Cloud | `GET /api/balance` (ollama.com) | Monthly included allowance used, its reset, and purchased credit balance |
| Ollama Cloud | `GET ollama.com/settings` (the page; there is no usage API) | Ollama Cloud's monthly usage (or the older session and weekly windows) |
| opencode Go | `GET /zen/go/v1/usage` | the 5-hour, weekly and monthly windows |
| Perplexity | `GET /rest/billing/credits` | the plan's monthly credit grant used, and the credit balance |
| Qoder | `GET /api/v2/me/usages/big_model_credits` (qoder.com or qoder.com.cn) | big-model credits used against the plan's (and the team's shared) total, until the next reset |
| Qwen Cloud Token Plan | `POST /data/api.json` on cs-data.qwencloud.com (tokenplan/personal/api/v2/usage) | the Individual Token Plan's 5-hour, weekly and monthly windows |
| Raycast | `GET /frontend_api/current_user/ai_credits` | the month's AI credit allowance used, and the credits left |
| Sakana AI | `GET /billing` and `/billing?tab=payAsYouGo` (server-rendered pages) | 5-hour and weekly quota windows, and the pay-as-you-go credit balance |
| StepFun Step Plan | `POST /api/step.openapi.devcenter.Dashboard/QueryStepPlanRateLimit` (platform.stepfun.com) | a Coding Plan's 5-hour and weekly windows, or a Token Plan's credit pool |
| Synthetic | `GET /v2/quotas` | the subscription's request quota |
| T3 Chat | `GET /api/trpc/getCustomerData` | the 4-hour Base window and the monthly Overage budget used |
| v0 | `GET /v1/user/billing`, `GET /v1/rate-limits` | the billing cycle's balance used and the request quota used |
| Warp | `POST /graphql/v2?op=GetRequestLimitInfo` | credits used since the last refresh against the plan's limit, and the add-on credits left |
| Windsurf | Windsurf's `state.vscdb` (`windsurf.settings.cachedPlanInfo`) | daily and weekly quota (or messages and flow actions) as Windsurf last cached them |
| Windsurf | `POST windsurf.com/_backend/.../GetPlanStatus` | daily and weekly quota, live |
| WorkBuddy (Tencent) | `POST /billing/meter/get-user-resource-summary` (www.workbuddy.cn) | the share of the credit packages' cycle used, and when it ends |
| xKiro | `GET /v1/usage` | the plan's spend windows, today's free tokens and the wallet balance |
| z.ai GLM Coding Plan | `GET /api/monitor/usage/quota/limit` | 5-hour and weekly coding-plan windows (token or credit rows) |
| Zed | `GET cloud.zed.dev/client/users/me` | edit predictions used of the plan's allowance this billing cycle |
| ZenMux | `GET /api/v1/management/subscription/detail` | rolling 5-hour and 7-day quotas, and the pay-as-you-go balance |
| Zhipu AI | `GET /api/monitor/usage/quota/limit` | 5-hour and weekly coding-plan windows |
| ZoomMate | `GET /ai-computer/api/v1/credits/status` | AI credits used against the budget cap this billing cycle, and the credits left |
<!-- end generated: provider-subscriptions -->

z.ai's and Kimi's endpoints are the ones their own clients call; they are
not in their published API docs. MiniMax documents its endpoint but not
the answer's fields. Treat their figures as the vendor's, read through
an interface the vendor may change. ZenMux is not read: its usage API
takes a separate management key, not the one your harness uses.

A harness pointed at a gateway, often one you run yourself, sends it a
key at an address TokenOps does not know. TokenOps first asks the
address's health route, without the key, what it is; only a recognised
gateway is then sent the key, at that same address, to read the key's
own budget:

<!-- begin generated: provider-gateways -->
| Gateway | Recognised by | Shows |
|---|---|---|
| Aixy | the hosted host, or its name (`AIXY_API_KEY`, setup) | `GET /v1/usage`: each budget that applies to the key, spent and reserved against its limit |
| Bifrost | `GET /health` | `GET /api/governance/virtual-keys/quota`: each budget's share used |
| ClawRouter | its host, or `GET /v1/health` | `GET /v1/usage`: the policy's spend against its monthly budget |
| LiteLLM proxy | `GET /health/liveliness` | `GET /key/info`: the key's spend against its budget, and when it resets |
| LLM Proxy (LLM-API-Key-Proxy) | `GET /`, or its name (`LLM_PROXY_BASE_URL`, setup) | `GET /v1/quota-stats`: the tightest quota group left on the credentials it pools, and when it resets |
| sub2api | `GET /setup/status`, or its name (`SUB2API_BASE_URL`, setup) | `GET /v1/usage`: the key's quota and 5-hour, daily and 7-day limits, a subscription group's daily, weekly and monthly limits, or the wallet balance |
<!-- end generated: provider-gateways -->

Portkey and Cloudflare AI Gateway are not asked: neither lets the key in
use read its own spend.

- **The key** is the one your harnesses already send that vendor: the key
  next to a gateway in Claude Code's settings, Codex's
  `[model_providers]` (`experimental_bearer_token` or `env_key`),
  opencode's `auth.json` and `provider.*.options.apiKey`, a conventional
  environment variable (below), or one you typed into
  `tokenops vendor-usage setup <provider>`. A key is sent only to the
  vendor whose endpoint it was found for, and is never logged; only a key
  typed into setup is stored, in your config file.
- **Calls** go only to vendors whose key is on the machine. Turn them off
  with `tokenops vendor-usage enable vendor-accounts --disable`.
- **Scopes**: Kilo and v0 read the key's default scope (the personal
  account, the default project) unless you name another:
  `tokenops vendor-usage setup kilo --scope <organisation ID>` or
  `tokenops vendor-usage setup v0 --scope <project ID or slug>`. The scope
  is checked with the key, stored as
  `vendor_usage.accounts.scopes.<provider>` in your config, and passed to
  that vendor as is (Kilo's `X-KILOCODE-ORGANIZATIONID` header, v0's
  `?scope=`). A later setup without `--scope` keeps it; `--scope ""`
  clears it. It also applies to a key found in a harness or variable.

  ```yaml
  vendor_usage:
    accounts:
      scopes:
        kilo: org_123
        v0: my-project
  ```

<!-- begin generated: provider-env-vars -->
The environment variables read for a key: `AIAND_API_KEY`, `AIXY_API_KEY`, `AI_GATEWAY_API_KEY`, `ALIBABA_CODING_PLAN_API_KEY`, `ALIBABA_CODING_PLAN_COOKIE`, `ALIBABA_QWEN_API_KEY`, `AMP_API_KEY`, `ATLASCLOUD_API_KEY`, `BOBSHELL_API_KEY`, `CHUTES_API_KEY`, `CLINEPASS_API_KEY`, `CLINE_API_KEY`, `CODEBUFF_API_KEY`, `COMMAND_CODE_API_KEY`, `DASHSCOPE_API_KEY`, `DEEPGRAM_API_KEY`, `DEEPINFRA_API_KEY`, `DEEPSEEK_API_KEY`, `DEVPASS_API_KEY`, `ELEVENLABS_API_KEY`, `FACTORY_API_KEY`, `FIREWORKS_API_KEY`, `GITKRAKEN_API_TOKEN`, `GROK_OAUTH_TOKEN`, `HF_TOKEN`, `HUGGING_FACE_HUB_TOKEN`, `HYPER_API_KEY`, `KILO_API_KEY`, `LLMGATEWAY_API_KEY`, `LLM_PROXY_API_KEY`, `LONGCAT_MANUAL_COOKIE`, `MANUS_COOKIE`, `MANUS_SESSION_TOKEN`, `MOONSHOT_API_KEY`, `NEURALWATT_API_KEY`, `NOUS_PORTAL_ACCESS_TOKEN`, `OLLAMA_API_KEY`, `OPENAI_ADMIN_KEY`, `OPENCODE_API_KEY`, `OPENROUTER_API_KEY`, `PERPLEXITY_COOKIE`, `PERPLEXITY_SESSION_TOKEN`, `POE_API_KEY`, `QWEN_CLOUD_COOKIE`, `SAKANA_COOKIE`, `STEPFUN_TOKEN`, `SUB2API_API_KEY`, `SYNTHETIC_API_KEY`, `V0_API_KEY`, `VENICE_API_KEY`, `VENICE_KEY`, `WARP_API_KEY`, `WARP_TOKEN`, `XI_API_KEY`, `XKIRO_API_KEY`, `ZENMUX_MANAGEMENT_API_KEY`.
<!-- end generated: provider-env-vars -->

#### Every provider

Generated from the provider registry (`docs/providers.md` says how to add one):

<!-- begin generated: provider-list -->
| Provider | Source | Reads | Credential | Checked |
|---|---|---|---|---|
| Abacus AI | `abacus-web` | compute credits used this billing month, resetting at the next billing date | browser session | CodexBar's source, the vendor's docs and fixtures |
| ai& | `aiand-account` | the organisation's USD spend in the last 30 days, summed from its request logs | API key | CodexBar's source, the vendor's docs and fixtures |
| Aixy | `aixy-account` | each budget that applies to the key, spent and reserved against its limit | API key | CodexBar's source, the vendor's docs and fixtures |
| Alibaba Cloud | `alibaba-account` | Coding Plan 5-hour, weekly and monthly quotas (where the console accepts the plan's API key) | API key | CodexBar's source, the vendor's docs and fixtures |
|  | `alibaba-web` | Coding Plan 5-hour, weekly and monthly quotas | browser session | CodexBar's source, the vendor's docs and fixtures |
| Alibaba Token Plan | `alibabatokenplan-cli` | the 5-hour, weekly and monthly windows, from the signed-in Bailian CLI | the vendor's CLI | CodexBar's source, the vendor's docs and fixtures |
|  | `alibabatokenplan-web` | Personal/Solo 5-hour, weekly and monthly windows, or a Team plan's credit pool | browser session | CodexBar's source, the vendor's docs and fixtures |
| Amp | `amp-cli` | subscription agent usage and orb hours, Amp Free's daily allowance, individual credits | the vendor's CLI | CodexBar's source, the vendor's docs and fixtures |
|  | `amp-account` | the same figures, with an access token | API key | CodexBar's source, the vendor's docs and fixtures |
| Claude | `claude-code-statusline` | the 5-hour and 7-day windows Claude Code gives its status line | local files | against a real account |
|  | `claude-code-jsonl` | per-turn tokens from Claude Code's transcripts | local files | against a real account |
|  | `claude-usage-meter` | Anthropic's own 5-hour and 7-day utilisation | browser session | against a real account |
|  | `claude-code-oauth` | the plan windows, with Claude Code's own sign-in (opt-in) | another app's sign-in | public docs and fixtures |
|  | `vendor-usage-anthropic` | the organisation's token usage from the Admin API | admin key | public docs and fixtures |
|  | `claude-code-stats-cache` | daily totals from Claude Code's stats cache (deprecated) | local files | against a real account |
| Antigravity | `antigravity-local` | 5-hour and weekly quota for Gemini models and for Claude and GPT models, while the app runs | another app's sign-in, once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Atlas Cloud | `atlascloud-account` | the account's available USD balance | API key | CodexBar's source, the vendor's docs and fixtures |
| Augment | `augment-cli` | credits used of the month's allowance | the vendor's CLI | CodexBar's source, the vendor's docs and fixtures |
|  | `augment-web` | the same credits, with the app.augmentcode.com session | browser session | CodexBar's source, the vendor's docs and fixtures |
| Azure OpenAI | — | in the catalog only: billed per token by Azure; a resource's key reads no spend, quota or usage (Azure Cost Management needs an Entra ID sign-in), so its turns are attributed and priced but its account is not read | — | — |
| Amazon Bedrock | `bedrock-cost-explorer` | this month's Bedrock spend, every 8 hours (Cost Explorer bills $0.01 a request) | the vendor's credential chain, after setup | CodexBar's source, the vendor's docs and fixtures |
| Bifrost | `bifrost-account` | each budget's share used | API key | public docs and fixtures |
| Cerebras | — | in the catalog only: Cerebras Code's plans and limits are known; nothing reads its usage yet | — | — |
| Chutes | `chutes-account` | the 4-hour and monthly caps | API key | the vendor's client source and fixtures |
| ClawRouter | `clawrouter-account` | the policy's spend against its monthly budget | API key | public docs and fixtures |
| ClinePass | `clinepass-account` | 5-hour, weekly and monthly windows | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Codebuff | `codebuff-account` | credits used against the quota until it resets; with the CLI's sign-in, the weekly rate limit too | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| CodeRabbit | `coderabbit-cli` | reviews this billing period, a count with no allowance (no percentage) | the vendor's CLI | CodexBar's source, the vendor's docs and fixtures |
| Command Code | `commandcode-web` | 5-hour and weekly limits, the monthly credit grant used, and purchased credits | browser session | CodexBar's source, the vendor's docs and fixtures |
| Cursor | `cursor-hook` | per-turn consumption Cursor's stop hook records | local files | public docs and fixtures |
|  | `cursor-web` | the weekly pool, or the billing cycle's included usage (legacy: requests), from cursor.com | browser session | CodexBar's source, the vendor's docs and fixtures |
| Deepgram | `deepgram-account` | prepaid USD balance left, across the key's projects | API key | CodexBar's source, the vendor's docs and fixtures |
| DeepInfra | `deepinfra-account` | spend since the last invoice, the limit, prepaid credit | API key | public docs and fixtures |
| DeepSeek | `deepseek-account` | prepaid USD balance left | API key | public docs and fixtures |
| Devin | `devin-web` | the daily and weekly quota used, and the extra-usage balance | API key | CodexBar's source, the vendor's docs and fixtures |
| DevPass | `devpass-account` | the cycle's plan credits and the premium weekly window; on pay-as-you-go, the key's spend against its limit | API key | CodexBar's source, the vendor's docs and fixtures |
| Doubao | `doubao-cli` | the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan), from the signed-in arkcli | the vendor's CLI | CodexBar's source, the vendor's docs and fixtures |
|  | `doubao-account` | the Coding Plan's 5-hour, weekly and monthly windows (the Agent Plan's when there is no Coding Plan) | API key | CodexBar's source, the vendor's docs and fixtures |
| ElevenLabs | `elevenlabs-account` | the subscription's credits used this period | API key | CodexBar's source, the vendor's docs and fixtures |
| Factory | `factory-account` | 5-hour, weekly and monthly windows (and the Core fallback's), or the Standard and Premium token allowances; the extra-usage balance | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
|  | `factory-web` | the same windows and balance | browser session | CodexBar's source, the vendor's docs and fixtures |
| Fireworks | `fireworks-usage` | the month's spend against the account's or the member's cap | API key | against a real account |
| Gemini | `gemini-cli` | per-turn tokens per model from Gemini CLI's chat recordings (Gemini Code Assist Standard or Enterprise, or an API key) | local files | public docs and fixtures |
| Copilot | `github-copilot` | the month's AI Credits (premium requests on annual plans) against the allowance; an org seat with none says so | another app's sign-in | the vendor's client source and fixtures |
| GitKraken AI | `gitkraken-account` | weekly AI credits, and the organization's pool with GITKRAKEN_ORG_ID | API key | CodexBar's source, the vendor's docs and fixtures |
| Grok | `grok-account` | the SuperGrok credit window used, and the prepaid balance | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
|  | `grok-web` | the credit window used | browser session | CodexBar's source, the vendor's docs and fixtures |
| Groq | `groq-web` | the organisation's GroqCloud spend this UTC month | browser session | CodexBar's source, the vendor's docs and fixtures |
| Helmcode | `helmcode-account` | each model's token quota used, and the prepaid balance | browser session | CodexBar's source, the vendor's docs and fixtures |
| Hugging Face | `huggingface-account` | Inference Providers charges this month, against the spending limit when set | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Charm Hyper | `hyper-account` | Hypercredits left | API key | CodexBar's source, the vendor's docs and fixtures |
|  | `hyper-web` | Hypercredits left | browser session | CodexBar's source, the vendor's docs and fixtures |
| IBM Bob | `ibmbob-account` | Bobcoins used this month against the team budgets | API key | CodexBar's source, the vendor's docs and fixtures |
| JetBrains AI | `jetbrains-local` | monthly AI credits used, from the most recently used IDE | local files | CodexBar's source, the vendor's docs and fixtures |
| Kilo | `kilo-account` | Kilo Pass credits used this billing period, and prepaid credit left | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Kimi | `kimi-account` | 5-hour, weekly and monthly windows | API key | the vendor's client source and fixtures |
| Kiro | `kiro-cli` | monthly plan credits used, and bonus credits | the vendor's CLI | CodexBar's source, the vendor's docs and fixtures |
|  | `kiro-overage` | plan credits used this month, and overage credits against their cap | another app's sign-in, once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Langdock | `langdock-web` | the included 5-hour session and weekly limits used | browser session | CodexBar's source, the vendor's docs and fixtures |
| LiteLLM | `litellm-account` | the key's spend against its budget, and when it resets | API key | the vendor's client source and fixtures |
| LithosAI | `lithosai-web` | the organisation's prepaid balance and its spend this UTC month | browser session | CodexBar's source, the vendor's docs and fixtures |
| LLM Proxy | `llm-proxy-account` | the tightest quota group left on the credentials it pools, and when it resets | API key | CodexBar's source, the vendor's docs and fixtures |
| LongCat | `longcat-account` | the token pack's share used, and the tokens left with pending fuel packs | browser session | CodexBar's source, the vendor's docs and fixtures |
| Manus | `manus-account` | monthly and daily-refresh credits used, and the credit balance | browser session | CodexBar's source, the vendor's docs and fixtures |
| Xiaomi MiMo | `mimo-web` | the balance (in its currency) and the Token Plan's monthly credits used | browser session | CodexBar's source, the vendor's docs and fixtures |
| MiniMax | `minimax-account` | the interval and weekly windows | API key | the vendor's client source and fixtures |
| Mistral | `mistral-web` | the included-API and Vibe allowances' shares used this month, and the credit balance in its currency | browser session | CodexBar's source, the vendor's docs and fixtures |
| Moonshot | `moonshot-account` | prepaid USD balance left | API key | public docs and fixtures |
| Muse Code | `muse-account` | the 5-hour and weekly quota used | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Muse | `museai-web` | the weekly token allowance used | browser session | CodexBar's source, the vendor's docs and fixtures |
| Neuralwatt | `neuralwatt-account` | prepaid USD credit left, and the subscription's kWh allowance used this period | API key | CodexBar's source, the vendor's docs and fixtures |
| Notion AI | `notion-account` | the Notion AI allowance used, rolling and this billing period (Business and Enterprise workspaces) | browser session | CodexBar's source, the vendor's docs and fixtures |
| Nous Portal | `nous-account` | the monthly credit grant used this period and the top-up credit left | API key, or another app's sign-in once granted (`--use-app-login`) | CodexBar's source, the vendor's docs and fixtures |
| Ollama | `ollama-api` | Monthly included allowance used, its reset, and purchased credit balance | API key | CodexBar's source, the vendor's docs and fixtures |
|  | `ollama-web` | Ollama Cloud's monthly usage (or the older session and weekly windows) | browser session | CodexBar's source, the vendor's docs and fixtures |
| Codex | `codex-app-server` | the 5-hour and weekly windows `codex app-server` reports, Codex signing its own request | the vendor's CLI | against a real account |
|  | `codex-jsonl` | per-turn tokens and the rate_limits in Codex's rollouts | local files | against a real account |
|  | `openai-admin` | the API organisation's spend this month (Administration API) | admin key | CodexBar's source, the vendor's docs and fixtures |
| opencode | `opencode` | per-message tokens for every provider, from opencode's SQLite store | local files | against a real account |
| opencode Go | `opencode-go-account` | the 5-hour, weekly and monthly windows | API key | CodexBar's source, the vendor's docs and fixtures |
| OpenRouter | `openrouter-account` | the key's spend, against its credit cap when it has one | API key | public docs and fixtures |
| Perplexity | `perplexity-account` | the plan's monthly credit grant used, and the credit balance | browser session | CodexBar's source, the vendor's docs and fixtures |
| Pi | `pi-sessions` | per-turn tokens per provider and model from Pi's and OMP's session transcripts | local files | CodexBar's source, the vendor's docs and fixtures |
| Poe | `poe-account` | the point balance left and the points spent over the last 30 days (points, not dollars) | API key | CodexBar's source, the vendor's docs and fixtures |
| Qoder | `qoder-web` | big-model credits used against the plan's (and the team's shared) total, until the next reset | browser session | CodexBar's source, the vendor's docs and fixtures |
| Qwen Cloud | `qwencloud-web` | the Individual Token Plan's 5-hour, weekly and monthly windows | browser session | CodexBar's source, the vendor's docs and fixtures |
| Raycast | `raycast-account` | the month's AI credit allowance used, and the credits left | browser session | CodexBar's source, the vendor's docs and fixtures |
| Replicate | `replicate-account` | spend this month, and the prepaid credit left | browser session | CodexBar's source, the vendor's docs and fixtures |
| Sakana AI | `sakana-web` | 5-hour and weekly quota windows, and the pay-as-you-go credit balance | browser session | CodexBar's source, the vendor's docs and fixtures |
| StepFun | `stepfun-account` | a Coding Plan's 5-hour and weekly windows, or a Token Plan's credit pool | session from a password sign-in | CodexBar's source, the vendor's docs and fixtures |
| sub2api | `sub2api-account` | the key's quota and 5-hour, daily and 7-day limits, a subscription group's daily, weekly and monthly limits, or the wallet balance | API key | CodexBar's source, the vendor's docs and fixtures |
| Synthetic | `synthetic-account` | the subscription's request quota | API key | public docs and fixtures |
| T3 Chat | `t3chat-account` | the 4-hour Base window and the monthly Overage budget used | browser session | CodexBar's source, the vendor's docs and fixtures |
| Together AI | — | in the catalog only: billed per token; its endpoint and models.dev prices are known, its account is not read | — | — |
| TypeSafe | `typesafe-account` | spend this billing cycle and the credit balance left | browser session | CodexBar's source, the vendor's docs and fixtures |
| v0 | `v0-account` | the billing cycle's balance used and the request quota used | API key | CodexBar's source, the vendor's docs and fixtures |
| Venice | `venice-account` | USD balance left, and the DIEM epoch allocation used when staking | API key | CodexBar's source, the vendor's docs and fixtures |
| Vercel | `vercel-account` | the team's credit balance | API key | public docs and fixtures |
| Warp | `warp-account` | credits used since the last refresh against the plan's limit, and the add-on credits left | API key | CodexBar's source, the vendor's docs and fixtures |
| Windsurf | `windsurf-local` | daily and weekly quota (or messages and flow actions) as Windsurf last cached them | local files | CodexBar's source, the vendor's docs and fixtures |
|  | `windsurf-web` | daily and weekly quota, live | API key | CodexBar's source, the vendor's docs and fixtures |
| WorkBuddy | `workbuddy-web` | the share of the credit packages' cycle used, and when it ends | browser session | CodexBar's source, the vendor's docs and fixtures |
| xAI | `xai-account` | the team's posted prepaid USD credit, and its spend over the last 30 days | API key | CodexBar's source, the vendor's docs and fixtures |
| X API | `xapi-web` | prepaid credit left, purchased plus free (below zero when overdrawn) | browser session | CodexBar's source, the vendor's docs and fixtures |
| xKiro | `xkiro-account` | the plan's spend windows, today's free tokens and the wallet balance | API key | CodexBar's source, the vendor's docs and fixtures |
| z.ai | `zai-account` | 5-hour and weekly coding-plan windows (token or credit rows) | API key | the vendor's client source and fixtures |
| Zed | `zed-account` | edit predictions used of the plan's allowance this billing cycle | another app's sign-in, from the Keychain at setup | CodexBar's source, the vendor's docs and fixtures |
| ZenMux | `zenmux-account` | rolling 5-hour and 7-day quotas, and the pay-as-you-go balance | API key | CodexBar's source, the vendor's docs and fixtures |
| Zhipu AI | `zhipuai-account` | 5-hour and weekly coding-plan windows | API key | the vendor's client source and fixtures |
| ZoomMate | `zoommate-account` | AI credits used against the budget cap this billing cycle, and the credits left | browser session | CodexBar's source, the vendor's docs and fixtures |
<!-- end generated: provider-list -->

### `tokenops vendor-usage enable <source>`

Writes a vendor-usage source's config block to the active config
file so operators don't hand-edit YAML. Nine sources covered:
`claude-subscription`, `cursor`, `github-copilot`, `codex-jsonl`,
`claude-code-jsonl`, `opencode`, `anthropic-admin`, `fireworks`,
`vendor-accounts`. Secrets accept env-var fallback so they don't leak
through shell history. It restarts the supervised daemon so the change is
live; `--no-restart` skips that.

The local transcript readers (`claude-code-jsonl`, `codex-jsonl`,
`opencode`, `gemini-cli`, `pi-sessions`) need no command: `tokenops init`
and the daemon turn on each one whose sessions are on this machine.
`--disable` on one of them is remembered (`switched_off: true` in the
config), and nothing turns it back on until you enable it again.

```bash
# Auto-discovers OAuth token from ~/.config/github-copilot
tokenops vendor-usage enable github-copilot

# Reader, no secret
tokenops vendor-usage enable claude-code-jsonl
tokenops vendor-usage enable codex-jsonl

# Secret via env to keep it out of shell history
TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY=sk-… \
  tokenops vendor-usage enable claude-subscription

# Flip a source off without clearing the persisted secret
tokenops vendor-usage enable claude-subscription --disable
```

Available env vars:
`TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY`,
`TOKENOPS_CURSOR_COOKIE`,
`TOKENOPS_COPILOT_OAUTH_TOKEN`,
`TOKENOPS_ANTHROPIC_ADMIN_KEY`.

## Pricing

### `tokenops pricing {refresh|show|diff|lint}`

Model prices as sourced, timestamped snapshots rather than one
hand-maintained table that drifts silently. `refresh` fetches rates from a
pluggable source (default: LiteLLM plus models.dev), stores each fetch
with its provenance under `~/.tokenops/pricing/snapshots/`, and diffs it
against the previous snapshot, so a change like
`anthropic/claude-opus-4-8 cache_read 0.50 → 1.50 (+200%)` shouts instead
of hiding. The embedded `pricing.yaml` is always there as the offline
baseline.

```bash
tokenops pricing refresh --dry-run            # fetch, lint and diff; write nothing
tokenops pricing refresh --source litellm     # default | litellm | models.dev
tokenops pricing show --json                  # latest snapshot, else the baseline
tokenops pricing diff --from baseline --to latest
tokenops pricing lint                         # exits non-zero on anomalies
```

`refresh` reaches the network. On any fetch error it says so and exits
non-zero **without** writing a snapshot; offline callers keep working on
the baseline. Snapshot selectors are `latest`, `baseline` or an RFC 3339
timestamp.

`lint` runs the consistency guard: every row gets a conservative sanity
check (cache-read must not exceed input), and `anthropic/*` rows also get
the family-ratio check (cache-read ≈10% of input, output ≈5× input). It
exits non-zero on anomalies, so it can gate CI.

## Explain

### `tokenops explain [term | decision-id | moves | price]`

What a figure means, in plain words: what it measures, how it is worked
out, how to read it, and for `dx` figures the grade bands. Given a
recorded decision's ID instead, it shows the evidence, the alternatives,
the policy that applied and the result, as JSON.

```bash
tokenops explain                # every term, by area
tokenops explain wall-clock
tokenops explain "first-try rate"
tokenops explain decision:6f1c…  # why TokenOps decided what it did
```

Agents get the same answers from the `tokenops_explain` and
`tokenops_explain (decision_id)` MCP tools.

`tokenops explain price` shows the price of quota on the Claude plan now
(λ, 0 to 1; ADR 0017): the binding window it comes from, the share the
rest of that window wants at the pace so far against the share left, and
for edits and deep work whether the tier they fit is worth its extra plan
use at that price or they go a tier down. `--json` gives the same.

```text
Price of quota (λ): 0.74, from the Claude weekly window (binding: the highest price of the plan's windows)
  At the pace so far the rest of the window wants ~212% of the plan; 54% is left.
  λ = 1 − left ÷ wanted: 0 while the window lasts to its reset, nearer 1 the longer it would stand empty.
A tier up is taken only when its quality gain exceeds λ × its extra plan use:
  edit work not escalated to claude-opus-5: its 0.12 quality gain over claude-sonnet-5 does not exceed its quota cost 0.45 (price 0.74 × 60% more plan use)
```

## Status line

### `tokenops statusline`

TokenOps' line under Claude Code's prompt, in Klarlabs colours:

```
Opus 5.5 high · ▰▰▰▰▱▱ 5h 62% ↻12:49 · wk 41% ↻Mon · ctx 71% → 60% · cache 95% · €3.11 value
● compact before the next task (/compact)
```

- the quota windows Claude Code reports (5-hour, weekly, or a gateway's
  spend limit), coloured green, amber and red as they fill;
- the context against **where the session compacts** (your coach's
  setting), so it warns before compaction rather than at 100%;
- the prompt cache hit ratio, or, when under two minutes are left before
  the cache expires, **cache cold in 1m**, and **cache cold** once it has:
  the next prompt then writes the whole context to the cache again. In
  brackets is how much that is, the prompt the cache holds (**cache cold
  in 1m (182k)**), so you can tell whether it is worth sending the next
  prompt before it expires. It never suggests `/compact`: Claude Code
  compacts an idle session itself, and a warning is information, not a
  reason to interrupt. The cache lives five minutes after the last
  request, or an hour when the session writes it with the extended
  lifetime; the line reads both from the timestamps and token counts at
  the end of the session's transcript, never its text;
- the session's cost in your currency — marked "value" when a plan
  covers it, since then it is what the work was worth, not what you paid;
- the coach's open tip, unless the coach is quiet or off.

It reads only what Claude Code hands it (the transcript path among it) and
files TokenOps already keeps, never the event store or the network, and
takes about 12 ms. Subagent
rows show each subagent's model, effort and context. `NO_COLOR` turns
the colours off.

It also keeps the newest windows Claude Code reported in
`~/.tokenops/claude-limits.json`, and the daemon stores them as Anthropic's
own reading. That gives `plan headroom`, the session budget and the coach
your 5-hour and weekly windows, or a Claude apps gateway's spend limit
(dollars too, when the limit is monthly), with no claude.ai login. When the
claude.ai usage meter is also connected, the newer figure wins for each
window, and windows only the meter reports, such as a model's own weekly
limit, are kept.

`tokenops init` sets it up, keeping a status line you already have under
TokenOps' line. To set it up or take it out by hand:

```bash
tokenops statusline install     # wraps your existing status line, if any
tokenops statusline uninstall   # puts yours back exactly
```

Uninstalling is remembered (`statusline.enabled: false` in TokenOps'
config), so a later `tokenops init` leaves it out; `statusline install`
turns it back on. A company can set the same key centrally.

## Coach

### `tokenops coach`

With no subcommand, shows the coach: its verbosity, and for each power
(`inform`, `waste`, `models`, `context`) the autonomy you set, what it can
actually do on this machine, why those differ, and the live quota window.
See ADR 0006.

#### Graduation: shadow → advise → act

Each capability moves up a ladder: **shadow** (it records what it would do),
**advise** (it tells you), **act** (it does it, on its own or after you
approve). `tokenops coach` publishes the bar for each capability's next step
and where your evidence stands against it, from the savings ledger and the
follow-through record:

| Next step | Criteria |
|---|---|
| shadow → advise | at least 50 shadow calls, at least 90% of them right (a held or observed tip whose session did not fix the problem on its own) |
| advise → act (read guard) | at least 50 re-reads it would have refused, at least 90% right, and the tokens they cost above zero |
| advise → act (tips, routing) | at least 50 pieces of advice with an outcome, at least 90% acted on, a saving the [holdout](#tokenops-coach-holdout-off-on-percent) realized above zero, and no harm found |

```text
  graduation (shadow → advise → act; you take each step):
    read guard      advise → act · ✓ shadow calls: 55 (needs at least 50) · ✓ shadow precision: 100% (55 of 55) (needs at least 90%) · ✓ realized saving: ~220k tokens (needs above zero)
                    ready: tokenops coach set waste autonomous
    compact tip     advise → act · ✗ advice with an outcome: 12 (needs at least 50) · …
```

Nothing is promoted on its own. When a step is ready, `tokenops glance` says
so ambiently ("Read guard is ready to enforce") with the command, and the
step is yours to take. The 90% bar is ADR 0005's: a kind of advice above about
a 10% effective false-positive rate is not worth more autonomy. While the
coach only observes, the session-budget tips it would have given are recorded
as shadow calls, which is the evidence for letting it speak.

#### Tips at decision points

The coach speaks to the agent where a choice is made, naming the one
thing the tip is about, rather than in a line after the turn:

| Decision point | Where it comes from | What it names |
|---|---|---|
| A tool call | the read guard on Claude Code (PreToolUse) | the file re-read or the search repeated ([loop tips](#loop-tips)) |
| A subagent launch | the route guard on Claude Code's `Agent` tool (PreToolUse), while `models` is `advise` | the subagent type, and the cheaper model its work fits: `this Explore subagent runs on claude-opus-5; its work looks like lookup, which claude-haiku-4-5 fits. Pass model: "haiku" when you launch the next Explore subagent like it.` While the plan runs short it names the window too |
| The turn's model choice | the route guard (UserPromptSubmit) | the model advised for the turn |
| `tokenops_prepare_work` | the MCP tool's `coach_tips` | the plan window the task may not fit or that is nearly used or running out at its pace (`window_fit`, `window_low`), or the cheaper model the work fits (`model_fit`) |

A tip at a tool call or a subagent launch is a note to the agent
(`additionalContext`) with no permission decision: it never answers the
operator's permission prompt and never changes the call. Tips follow the
coach: none when `coach.powers.inform` is `off` or `coach.verbosity` is
`quiet` (the model advice follows the `models` power and its own quiet
rule).

**Throttle.** A session hears at most `coach.tips.per_hour` decision-point
tips in any hour (6 by default), and the same tip, its kind and the thing
it names, not again within `coach.tips.cooldown` (1h by default). Tips
from `tokenops_prepare_work` carry no session and share one budget.
`verbose` still gives the model advice every applicable turn.

**What the agent did instead.** What the agent does next settles a tip,
and the follow-through ledger keeps the context, never contents or
prompts: the same file read or the same search run again within 5 tool
calls ignores a loop tip ("read store.go again"); the next launch of the
same subagent type on the same model ignores a subagent tip ("launched
Explore on opus again"), and on a cheaper one follows it; advice the
session stays on the same tier through ignores it ("stayed on
claude-opus-5"); `tokenops_review_work` settles the model tip from the
models the workflow's steps ran on. `tokenops coach` shows, for each
kind, the last ignored offer's decision point and what the agent did
instead.

**Retired kinds.** A kind of tip or advice that was seen and settled at
least 20 times in the last 30 days, and ignored at least 80% of those
times, is retired: the coach stops giving it, and `tokenops coach` and the
glance's findings say so with the figures:

```text
  follow-through:
    inform tips on reread_churn: 2 followed, 18 ignored · last ignored at a tool call: read store.go again · retired: ignored 18 of the 20 times it was seen in the last 30 days (`tokenops coach reinstate reread_churn` brings it back)
```

Only evidence of being seen counts. A tip at a decision point counts as
seen once the agent acts after it; a Stop-hook tip only when it was shown
on the status line, so a tip nobody saw is unseen, not ignored, and never
retires anything. Tips about work about to stop (a window at 90% or
forecast to run out) are never retired. Retirement is reversible:
[`tokenops coach reinstate <kind>`](#tokenops-coach-reinstate-kind-all)
brings a kind back, and only evidence after that counts again.

### `tokenops coach reinstate <kind|all>`

```bash
tokenops coach reinstate reread_churn   # a retired tip kind
tokenops coach reinstate edit           # a model move retracted for quality
tokenops coach reinstate all
```

Brings back a tip or advice kind the coach [retired](#tips-at-decision-points)
for being ignored, and a model move it retracted because work it moved did
worse (`tokenops explain moves`). The evidence before it no longer counts
toward retiring or quieting the kind again.

### `tokenops coach preset [observe|advise|guided|autopilot]`

Sets every power and the verbosity in one choice, then makes the machine
match it: installs the coach, read-guard and route-guard hooks on every
installed agent (Claude Code, Codex, Cursor, opencode) as far as each
supports them, and sets where each agent compacts.

| Preset | What it does |
|---|---|
| `observe` | records what the coach would say or do; says and does nothing |
| `advise` | tells you what you could do better, once per kind of thing; changes nothing |
| `guided` | refuses redundant re-reads and asks before moving subagents to a cheaper model |
| `autopilot` | acts on its own and stays quiet |

With no argument it lists the presets and marks the one in effect.

### `tokenops coach autonomy|set|verbosity|off`

```bash
tokenops coach autonomy ask          # default for every power: off | advise | ask | autonomous
tokenops coach set models advise     # override one power
tokenops coach verbosity quiet       # quiet | normal | verbose
tokenops coach off                   # record only; say and do nothing
```

Autonomy decides who acts; verbosity how much the coach says. A rung the
coach cannot deliver here is shown one rung lower, with the reason.
Approval requests are always shown. A [`model_policy`](./configuration.md#model-policy-model-policy)
applies whatever the coach is set to.

### `tokenops coach stats`

What the coach saw and did, from its two ledgers: each session's spend
against budget fractions, the quota and compact tips it gave and what it
held back, and how the run-out forecasts fared once their windows reset
(forecasts that came true, and windows that ran out unforecast); and files
read again, which re-reads were waste, and which the read guard refused. Both ledgers fill while the coach only observes, so
this shows what you would hear before you let it speak. A *Tip volume*
section lists, for the last 7 days, the tips given per day by kind and the
share nothing showed were seen, and the tips the coach stood down for
because the vendor's own surface says them
([the coach stands down where the vendor speaks](./configuration.md#the-coach-stands-down-where-the-vendor-speaks)).
`--json` gives `budget`, `read_guard`, `savings` and `tip_volume`.

The savings ledger lists every intervention by kind with what it was
expected to save and what it is shown to have saved. Each tip shown, each
re-read refused and each model move suggested writes a row with its expected
saving, the figures it rested on, and its decision ID (no prompt, path or
file content). Expected is the coach's estimate when it acted: for a compact
or budget tip, the context a compaction would remove, re-read at the model's
cache-read rate on every later turn. Realized comes only from the holdout
below, so a potential saving is never shown as a delivered one.

```text
Savings ledger — expected when acting, realized as the holdout measured it
  compact tips: 41 shown, 5 held back · expected ~$0.31 per later turn, if acted on · realized $3.80 per tip (spend after: $6.10 shown vs $9.90 held back)
  re-reads refused: 212 · expected ~1.9M tokens · realized not established: no intervention of this kind was held back, so there is nothing to compare against
```

It also shows the session budget the tips are measured against and where
it came from. Unless you set one (`tokenops hooks install --coach --budget
80`; any figure but the $50 default counts as yours), the budget is read
off your own sessions: the p90 of the API-equivalent cost of the sessions
you ran in the last 30 days, once at least 20 are known, rounded to whole
dollars and kept between $10 and $500. It is re-derived once a week and
moves at most 25% each time, so a run of expensive weeks does not quietly
become the new normal. A session keeps the budget it started with. With
fewer than 20 sessions the $50 default stands.

```text
  session budget: $45 — your p90 session, from 113 sessions in 30 days (derived 2026-10-14)
    held from $72: moves at most 25% a week (was $36)
```

The derivation is kept, with the figures it was read from, in
`~/.tokenops/coach-hook/session-budget.json` and in `--json` as
`budget.session_budget`.

### `tokenops coach tip ack|expected|snooze`

Answers a tip, by default the newest one you have not answered (`--id`
names another). A tip moves through new (given), seen (shown on the
status line), then your answer:

| Answer | What it does |
|---|---|
| `ack` | You have seen it. Nothing more of that kind is said in that session: the 50%, 75% and 100% budget tips are one episode. |
| `expected` | It is normal for you. It closes the episode the same way and is recorded as an outcome: once you have answered at least 3 budget tips in 30 days and at least half of those answers were `expected`, the session budget is read off your p95 session instead of your p90. |
| `snooze` | Not now. Tips of that kind wait `--for` (default 4h), in every session. A warning that a plan window will run out, or is used up, still speaks. |

```bash
tokenops coach tip snooze --for 2h
# Snoozed the tip at 50% of the session budget: tips like it wait until Tue 16:40.
```

An unusual-spend finding (`--id anomaly-…`, shown on glance) is answered
the same way: `ack` takes it off the findings, `expected` also stops
flagging the same pattern, and `snooze` holds every spend finding.

An answered tip counts as neither followed nor ignored, so it never quiets
the coach. `tokenops coach` shows the answers per kind of tip with the share
snoozed, and `tokenops glance` flags a kind you snooze most of the time,
because a tip snoozed that often fires when it is not wanted. The
`tokenops_coach` MCP tool takes the same answer as `tip`, on your word.

Each day the coach interrupts at most `coaching.quiet.max_per_day` times
(default 6) across sessions. Past that, tips wait on the status line.

### `tokenops coach holdout [off|on|<percent>]`

To learn whether a tip pays, the coach holds a few sessions' cost and drift
tips back: the 50% and 75% session-budget tips and the compact tip. A held
tip is not said, not put on the status line, and not counted against the
quiet policy; it is logged as shadow, with the same expected saving and
evidence, and settled the same way (a compaction, a model switch, nothing).
Which sessions are held is random but fixed per session, so a held session
stays held for the episode.

It is on at 10% and never more. Tips about work about to stop are never
held: a plan window forecast to run out or used up, and the session budget
spent. `tokenops coach holdout off` opts out, `on` restores 10%, and a
number from 0 to 10 sets the share; a preset keeps your choice. The
`tokenops_coach` MCP tool takes the same setting.

The same share of the subagent moves the coach makes on its own
(`coach.models: autonomous`) is held back: the subagent runs on the model
the agent asked for, and the move it would have been is recorded. Each
kind of move (task kind, from-model → to-model) is then judged against the
runs chance held back rather than against whatever unmoved work happened
nearby, once at least 8 have settled; until then it falls back to the
nearby comparison. A kind that does worse than its holdout by 15 points or
more, at z 1.645, is retracted as before. `tokenops explain moves` says
which comparison each verdict rests on.

`tokenops verify` and `tokenops coach stats` compare the sessions that got a
tip with the ones it was held from: the spend after the tip, whether the
context was compacted, and whether a commit landed in the session's
repository within a day. The difference in spend is the tip's realized
saving. Until each side has at least 5 sessions it is "not established".

### `tokenops coach headroom-note [on|off]`

An agent budgets only what it can see. With the route-guard hook installed,
the agent is told how much of the plan is left, in one line with its
prompt:

```text
Plan headroom: 5h window 38% left (runs out ~13:38 at this pace, resets 14:20), week 61% left.
```

Only the binding window is named, the one with the least left, plus another
within 25 points of it. A window that will not last to its reset at the
pace it has been used so far says when it runs out. The line is said at a
session's first prompt and again only when a window crosses 50, 25 or 10%
left, starts running out before its reset, or the binding window changes;
otherwise nothing is added.

The per-turn model advice reads the same pace, as a price of quota (see
[`tokenops explain price`](#explain)):
an edit or deep work is advised a tier lower once the tier it fits is no
longer worth its extra plan use at that price, as subagent routing does,
and the advice names the price and the window it comes from. The note
itself stays information for the agent, never a cap.

It travels in the prompt hook's `additionalContext`, which Claude Code
inserts where the hook fired, after the part of the conversation already
cached, and keeps in the transcript as written. It is never put in
`CLAUDE.md`, `AGENTS.md` or a session-start block, where a figure that
changes would invalidate the cached prefix. On by default; `off` turns it
off (`coach.headroom_note: false`), and so does the inform power being off.
A preset keeps your choice, and the `tokenops_coach` MCP tool takes the
same setting (`headroom_note`).

### `tokenops coach delivery`

The older setting the coach replaced; it still works.
`tokenops coach migrate` writes the equivalent `coach` block.

Shows or sets how far coaching goes: `observe` (answers when asked),
`advise` (also nudges, never blocks — the default), or `intervene` (also
lets `read-guard` refuse redundant re-reads).

```bash
tokenops coach delivery              # print the current level
tokenops coach delivery intervene    # let the guard start blocking
```

One key for every coaching channel, applied at call time — no need to
re-run `tokenops hooks install`. See
[Configuration](./configuration.md) for how it differs from `mode`.

### `tokenops coach prompts`

Heuristic prompt-quality feedback. Walks
`~/.claude/projects/**/*.jsonl` AND `$CODEX_HOME/sessions/**/*.jsonl` (`~/.codex` when unset),
extracts human-typed turns, reports length distribution
(under-5-word, 5-15, 15-50, 50-200, >200), vague-short count
(<15 chars / ≤3 words), pure acknowledgements (yes/no/ok/continue),
short questions, repeated prompts (verbatim 3+ times), and
concrete recommendations tuned to your pattern. **Prompt text is
read at scan time and is never persisted to the event store.**

```bash
tokenops coach prompts --since 7d            # both Claude Code + Codex
tokenops coach prompts --since 30d --json    # JSON for agent hosts
tokenops coach prompts --session-id <id>        # restrict to one session
tokenops coach prompts --quote               # add example prompts (redacted)
```

Output leads with a **BIGGEST WIN** panel — the highest-impact
recommendation, plus projected savings (turns / tokens / dollars /
hours) computed from your per-turn averages. By default the report
quotes none of your prompts, in text or `--json`; `--quote` adds
examples from your data, each passed through the secret redactor and
cut to 120 characters.

### `tokenops coach replies`

Output-side sibling of `coach prompts`. Walks the same JSONLs but
extracts assistant replies and scores per session: article density,
filler density, average word length, code-block ratio, and a
"caveman-likely" verdict + rough estimated token savings. Useful to
detect when an output-compression skill is engaged and how many
tokens it suppressed.

```bash
tokenops coach replies --since 7d
tokenops coach replies --json
```

### `tokenops coach lint-context`

Finds what in every session's context changes between sessions or turns,
and so breaks the prompt cache. A provider caches a prompt up to the first
byte that differs from an earlier one: a timestamp near the start of the
context makes every session rebuild the cache from that point, however
stable the rest is. Keep the always-loaded context stable and put what
changes after it, or leave it out and let the agent fetch it.

It reads only what the harness loads into every session:

- Claude Code: `~/.claude/CLAUDE.md`, the project's `CLAUDE.md`,
  `.claude/CLAUDE.md` and `CLAUDE.local.md` (and its parents' up to the
  repository root), and the files they `@import`, one level deep, never
  twice; and the `SessionStart` and `UserPromptSubmit` hooks in the
  managed, user and project settings.
- Codex and opencode: the project's `AGENTS.md`, `~/.codex/AGENTS.md`
  and opencode's `AGENTS.md`.
- What hooks printed: the newest Claude Code transcripts (20 by default)
  record each hook's output. A `SessionStart` hook whose output carries a
  timestamp, a UUID or a commit hash, or differs from one session to the
  next, is reported even when its command is a script. When the outputs
  differ only in the order of a JSON value's keys (or its spacing), the
  finding says so: the hook prints the same facts from a hash map in
  whatever order it iterates, and sorting the keys fixes it. Only what the
  transcripts show is judged; a command that could print unordered JSON
  but has not differed yet is not flagged, and a reordered JSON array is
  reported as output that varies, since array order can be content.

- What changed during a session: the same transcripts record when the
  tool or MCP server set changed between turns and when an always-loaded
  file (`CLAUDE.md`, `AGENTS.md`, or a file the session's instructions
  listed, such as an auto-memory `MEMORY.md`) was edited, by you or by the
  agent. Each change is measured: the cache-read share of the turn just
  before it against the turn just after, under "Mid-session changes" (and
  `mid_session_changes` in the JSON). A change whose next turn wrote most
  of the cached prompt again, with no idle gap past the cache's lifetime,
  model switch or compaction to explain it, is a medium finding; one that
  met a cache already cold (a session resumed an hour later) is counted
  apart and left out of the shares. Claude Code appends these changes after
  the turns before them rather than rewriting the prompt, so on the
  machine this was built on 163 tool set changes and 18 context edits over
  150 transcripts rebuilt nothing (cache read 92% before, 89% after; 99%
  and 99%); the measure is there to show it if a harness behaves
  otherwise. A change to the tool definitions that Claude Code makes
  without recording it is not seen.

Hooks are never run: the command text is read (`date`, `uuidgen`,
`git status`, `git log`, `gh pr list`, `curl`, `ps`, `df`, `$RANDOM`,
`datetime.now` and the like, unless the output goes to a file). Each finding names its file and line or hook,
the matched token and nothing around it, why it breaks the cache and the
fix. Nothing is sent anywhere.

| Severity | Means | Example |
|---|---|---|
| high | Opens every session's context and differs each time | a `SessionStart` hook that runs `date` |
| medium | Changes often | a `UserPromptSubmit` hook that prints the time (each turn's copy is new input; earlier turns stay cached); a "Last updated: …" line a tool rewrites |
| low | Changes only when someone edits the file | a date written in `CLAUDE.md` |

Version numbers (`go 1.26`, `v1.2.3`), ports, colours and issue numbers
are not flagged. A high finding also shows in the coach's findings
(`tokenops glance`, the menu bar, `GET /api/findings`), from your own
files and hooks only.

```bash
tokenops coach lint-context                       # this project, your files and hooks
tokenops coach lint-context --root ~/src/app      # another project
tokenops coach lint-context --transcripts -       # skip recorded hook output
tokenops coach lint-context --json                # the report, for scripts
```

The same report is `tokenops_rules` with `view: lint_context` over MCP and
`GET /api/coach/lint-context` on the daemon.

## Agent DX

### `tokenops dx`

What your agent sessions are like to work with: how many turns a typical
instruction costs, how often the agent redoes its own work, how often you
interrupt it. Derived from the transcripts the client already writes —
no proxy needed. Work is grouped by operator instruction: a prompt you
typed, and everything the agent did before the next one.

```bash
tokenops dx                       # last 7 days, every client found
tokenops dx --days 30 --source codex
tokenops dx --json                # {window, metrics, grades, recommendation, cohort}, as tokenops_agent_dx
tokenops dx --role primary        # your own instructions only
tokenops dx --source codex --role auto_review   # Codex's automatic reviews only
tokenops dx --model gpt-6.1-sol   # instructions mostly served by one model
```

**Choose the cohort.** `--model` keeps instructions mostly served by one
model (case-insensitive exact id). `--role` keeps one kind of instruction:
`primary` is what you typed, `auto_review` is a review the harness ran by
itself (Codex's reviewer, or any turn served by `codex-auto-review`), and
`subagent` is work a harness delegated. The default, `all`, pools them,
and the header says when it did:

```
Agent DX — last 7d
  640 instructions across 38 sessions
  410 of these are automatic reviews; --role primary measures your own instructions alone
```

An automatic review is a couple of seconds of a small model reading a
diff. Pooled with your own work it sets the median, so the median then
describes neither. The same filters exist on `tokenops story`, on the
`tokenops_sessions` MCP tool (`source`, `model`, `role`) and on the
daemon's `/api/dx`, `/api/story` and `/api/story/runtime`; one selection
gives the same answer on each.

The **by model and effort** table splits instructions by the model and
the reasoning effort that handled them (Claude Code, Codex and opencode
record both), so comparing `high` with `medium` on the same model is a
matter of running a week at each. A row with fewer than 30 instructions
is shown but marked as too small to compare. Sessions in throwaway directories (`/tmp`, benchmark clones)
are excluded unless you pass `--include-scratch`.

## Story

### `tokenops story`

Reconstructs your work as an account of it, one task at a time: the
instruction you typed, everything the agent did before the next one, what
it cost, and the specific moments it went wrong.

```bash
tokenops story                      # last 7d, 10 most recent tasks
tokenops story --days 1 --limit 3
tokenops story --json               # same account, for an agent to read back
tokenops story --idle-gap 30m       # coarser task boundaries
tokenops story --for report         # for someone you bill or report to
tokenops story --for handoff        # for a teammate picking the work up
tokenops story --runtime            # where each task's time went, with coverage
tokenops story --runtime --json     # the same, as a derived-only export
tokenops story --runtime --role primary --source codex
```

```
Your work — last 2d
  3 tasks

─ fix the parser panic on empty input
  Tue 09:24 → 09:28 (3m)  · split by idle gap
  4 instructions · 73 turns · 38 tool calls · context peaked at 681k
  touched internal/parse/lexer.go, internal/parse/parse_test.go
  where it went sideways:
    · you told it the 1st answer was wrong
    · the 2nd instruction edited the same file twice
```

**Sessions run in throwaway directories are excluded.** A benchmark
harness, a temporary clone, anything under `/tmp` or the system temp
root: `story`, `dx` and `coach prompts` all claim to describe how *you*
work, and a simulated session has no operator. Pass `--include-scratch`
to read them anyway.

This is not a rounding error. On one real machine a simulation harness
was **94% of a 7-day window**, and `dx` graded it: a median of 2.0 turns
per instruction against the operator's actual 17.0, zero tool calls
against 8.0, and straight A's over a corpus that contained almost none
of their work.

Spend is deliberately *not* filtered this way. A simulated session still
spent real tokens against a real plan, so the ledger and the headroom
math are right to count it. What it never did was say anything about how
its operator works.

**A task is inferred, not declared.** A new session always starts one, and
a pause longer than `--idle-gap` (default 10m) splits one. Every task says
which of those split it, because a boundary you can see is one you can
argue with. The gap is measured from when the agent *stopped working*, not
from when you typed — a unit that ran for an hour did not leave you idle
for an hour. `tokenops task start|done` still marks a task exactly when you
want to be precise.

Units are grouped per session before any boundary is inferred. Concurrent
sessions are ordinary — two terminals, two projects — and their
transcripts interleave in wall-clock time, so a global time order would
see the session change at nearly every instruction.

**`context peaked at` is not a cost.** It is the largest context a single
turn carried — how heavy the work got, counting the context once. The
summed figure (`context_carried_tokens` in JSON) double-counts by
construction, because every turn re-sends the accumulated context; that is
the number that reads about nine times what cache-aware pricing says. It is
there for comparing tasks against each other, never as spend.

#### `--runtime` — where the time went, and what is not known

A task's interval, from its first instruction to the last thing the
transcript recorded, is easy to reconstruct and easy to misread. It holds
model requests, tools running, you answering a question, you not having
typed yet, and stretches nothing recorded at all. `--runtime` splits it
into what the transcripts actually carry. It is a coverage report, not a
tracer:

```
Task runtime — last 7d
  20 tasks: 18 completed, 1 last observed, 1 open
  runtime samples: 19 of 20 (1 open, 0 never answered, left out)

BY COHORT  (observed interval: first instruction to last activity)
  HARNESS      MODEL                    ROLE          TASKS  SAMPLES    MEDIAN       P90
  claude-code  claude-opus-5-5          primary          20       19     35.4m      1.7h

WHERE THE TIME WENT  (median share of the observed interval, over the samples that record it;
  each task's shares add up to its interval, but medians of them need not add up to 100%)
  model_request        10%   recorded in 19 of 19 samples
  tool_execution       23%   recorded in 19 of 19 samples
  approval_wait         0%   recorded in 19 of 19 samples
  idle                  3%   recorded in 19 of 19 samples
  unattributed          1%   recorded in 19 of 19 samples
```

**Status.** `completed` means the harness recorded the end of the task's
last turn (Claude Code's `turn_duration`, Codex's `task_complete`): the
turn ended, not that the work was right. `open` means no end was recorded
and the session was active within the idle gap, so the task may still be
running; its interval is right-censored and it is no runtime sample.
`last_observed` is everything else: the interval runs to the last activity
seen. A task no turn answered has no interval at all. Summaries say how
many tasks they cover and why the rest were left out.

**Categories.** Every instant of a task's interval is assigned to exactly
one category, in this order when recorded intervals overlap: approval
wait, tool execution, model request, idle. What none covers is
unattributed. So a task's categories always add up to its interval, and
nothing is counted twice.

| Category | Where it comes from | Recorded by |
|---|---|---|
| `model_request` | the input written before a request (your instruction or a tool result) to the last entry the request wrote, grouped by request id. Includes queueing and streaming; it is **not** time to first token. A span longer than the idle gap (a machine asleep mid-request) is left unattributed | Claude Code. Codex, Cursor and opencode record no request boundaries: **unknown** |
| `tool_execution` | each tool call's entry to its result's entry, paired by call id | Claude Code, Codex. Cursor and opencode record calls without results: **unknown** |
| `approval_wait` | tools that ask you something (`AskUserQuestion`, `ExitPlanMode`). A **lower bound**: no client records a permission prompt, so waiting on one sits inside its tool's span | Claude Code. Codex rollouts record no approval requests: **unknown** |
| `idle` | from the agent's last activity on one instruction to your next instruction in the same task | every client |
| `unattributed` | the rest, including any time a client does not record | every client |

An unknown category is `null` in the JSON, never `0`, and its time sits
visibly in `unattributed`. Tool time comes two ways: the **critical
path**, the wall time at least one tool was running, which is what the
task waited on, and the **summed resource time** of every call, which
counts concurrent calls once each. Time to first token appears only where
the harness records it (Codex's `task_complete`); it is never inferred
from transcript timestamps, which are written when a block finishes.

**Reading it.** Medians and p90 are nearest-rank, over the runtime
samples, per harness, model and role. Never add task durations up as
time spent waiting: concurrent tasks overlap, and idle and unattributed
time is nobody's work. The friction count is a heuristic read from the
transcript, labelled as one, and `verified_success` is always unknown:
transcripts record what was attempted, not whether it was right.

**Cost per task.** Each task also carries what its recorded turns used
and cost, from the event store: turns, input, output and cache tokens,
what was billed, and the API-equivalent value at list prices. A recorded
turn goes to the task of its session that started last at or before it,
so a subagent's turns count toward the task that started it. A task whose
session the event store recorded no turns for has a null `usage`, never
zero, and the summaries give the median and p90 of value, billed cost and
tokens over the samples that have one (`usage_samples`), counting the ones
with an unpriced turn (`usage_unpriced`), whose value understates.

`--runtime --json` is the derived-only export, the same document as the
`tokenops_sessions` tool's `runtime` view and `GET /api/story/runtime`:
per task a stable hashed `task_key` and `attempt_keys`, harness, model,
role, status with its evidence, the categories with `quality`, `origin`,
`complete` and their coverage (`timed` of `seen`), the tool figures, the
usage, and the summaries. It carries no instruction text, file paths or session ids.

#### `--for` — one account, four readings

The structure is one task. The renderings differ because the audiences
do: four people want the same work described and none of them wants the
same document.

| `--for` | Reader | Optimised for |
|---|---|---|
| `me` *(default)* | you | candour — what happened, including where it went sideways |
| `agent` (`--json`) | a machine reading its own history back | fields, enumerated |
| `report` | someone you bill or report to | **defensibility** — evidence readable by someone who was not there |
| `handoff` | a teammate picking the work up | **state of the world** — what to open first |

`--for report` is a record, not a confession. It carries what the
transcript actually holds — when the work happened, for how long, at what
volume, against which files — grouped by day, and it omits the friction
narrative on purpose: "you told it the third answer was wrong" is candour
aimed at you, and in front of a client it turns an account of work into an
apology for it. The footer says what the figures are *not*: wall-clock
from local transcripts is not a timesheet and not a judgement about what
is billable.

`--for handoff` is ordered by what needs attention rather than by time —
work that ended on friction first, then what ran clean, then the files the
work kept coming back to. It never says a task "landed": a transcript
records what was attempted, and whether the code works is a question only
the tests answer.

Both replace your home directory with `~` and elide long paths from the
left. A document meant for someone else should not carry your username.

Titles are your own instructions in both, quoted rather than
paraphrased — a summariser can be wrong and a quote cannot. An unknown
`--for` is an error rather than a silent fall back to the default, because
that fallback is how someone emails a client the candid rendering.

Prompt text is read at scan time and **never persisted**. The account is
rebuilt from transcripts on every run.

## Hooks

TokenOps wires four hooks into a client, each independent:

| Hook | Fires | What it does |
|---|---|---|
| `--coach` | end of turn | the coaching nudge, against a per-session budget |
| `--read-guard` | before a file read, search, edit or Bash command | refuses a redundant re-read of an unchanged file and, in active mode, a [repeated read-only command](#repeated-commands); tells the agent when it re-reads a file or repeats a search ([loop tips](#loop-tips)) |
| `--route-guard` | when you submit a prompt | states the case for a cheaper model when the one in use is over-spec, and the [plan headroom note](#tokenops-coach-headroom-note-on-off) |
| `--boundary` | before every tool call | checks your [action boundaries](https://team.klarlabs.de/guide/boundaries); Claude Code and opencode only |

`--client` selects which client to act on, and **install, status and
uninstall all take it**:

| `--client` | Where the hooks live |
|---|---|
| `claude-code` *(default)* | `~/.claude/settings.json` |
| `codex` | `$CODEX_HOME/hooks.json` (default `~/.codex/hooks.json`) |
| `cursor` | `~/.cursor/hooks.json` |
| `opencode` | a generated plugin in `~/.config/opencode/plugins` |

```bash
tokenops hooks install --route-guard        # wire the model-fit guard
tokenops hooks status                       # what's wired for Claude Code
tokenops hooks status --client cursor       # ... and for Cursor
tokenops hooks uninstall --route-guard      # remove one hook
tokenops hooks uninstall                    # remove every tokenops hook
```

`uninstall` with no hook named removes all four. Naming one removes only
that one, on every client — including opencode, where the generated plugin
is rewritten with the remaining halves rather than deleted, and deleted
only once nothing is left in it.

`status` reports the hook, its event, and the binary each entry calls, and
flags an entry pointing at a different binary than the one you ran — which
is what a stale `hooks install` from an older install looks like.

### Loop tips

On Claude Code the read guard is installed on two matchers: `Read`, and
`Grep|Glob|Edit|MultiEdit|Write|NotebookEdit`. The second lets it notice
the agent going round in circles and say so at the tool call where it
happens, rather than in a line after the turn that is easily missed:

- **Re-read churn:** the same file read 3 times in the agent's last 10
  tool calls the hook saw, changed or not. A ranged read counts only
  against the same range, so paging through a large file is not churn.
- **Search loop:** the same `Grep` or `Glob` (tool, pattern and path, and
  for `Grep` its `glob` and `type`) run 3 times in the last 10 tool calls
  with no `Edit`, `MultiEdit`, `Write` or `NotebookEdit` in between.

The tip is a short note to the agent (`additionalContext`), never a
refusal, and it never changes whether a read is allowed:

```
tokenops: you have read readguard.go (internal/cli/readguard.go) 3 times in the last 10 tool calls. Keep the part you need in view, or say what you are looking for in it, instead of reading it again.
```

It names the file, or the search with its pattern cut at 60 characters,
and quotes nothing else: no file contents, no prompt text. Each is given
once per file or search per session, and a subagent's calls are counted
apart from the main agent's. The hook sees the edit tools and every Bash
command; a Bash command that may change things resets a search loop like
an edit. A file changed by the operator or another process is invisible
to it, so the tip is a prompt to step back, not a verdict. Session length is
not counted against anyone.

Loop tips follow the coach: nothing is said when `coach.powers.inform` is
`off` or `coach.verbosity` is `quiet`, and the [decision-point
throttle](#tips-at-decision-points) bounds how many a session hears. Each
tip is recorded in the coach's follow-through ledger as delivered to the
agent at a tool call, with the file's base name or the search. The agent's
next calls settle it: the same file read, or the same search run, again
within 5 tool calls is ignoring it; an edit after a search loop, or 5
calls without the loop, is following it. A session that ends first leaves
it unseen. An install from before loop tips gains the second matcher when
`tokenops init` or `tokenops hooks install --read-guard` runs again.

#### Repeated commands

The read guard is installed on Bash as well. A read-only command (`git
status`, `git diff`, `git log`, `ls`, `cat`, `grep`, `rg`, `find`
without an action that changes things) run again in the same working
directory, within 10 minutes and the agent's last 10 tool calls, with no
edit and no other Bash command in between, already has its answer in the
agent's context. A command that chains, pipes, redirects or substitutes
is never read-only.

It follows the read guard's mode. In observe mode the repeat is recorded
and runs; in active mode (the waste power at `autonomous`) it is refused
with a pointer to the earlier run: "`git status` ran 2m ago (the previous
tool call), and nothing has changed since that this session made; its
output is above". A change made outside the session is invisible to the
hook, so the refusal says to run it again with a flag or path added.
`tokenops coach stats` counts the repeats seen and refused; the command
itself is not recorded. On the machine it was built on, 1 in 296
read-only commands repeated this way (`tokenops dx` measures it), so it
is a small saving.

An install from before this moves read-guard's second matcher to
include Bash, without leaving the old one behind.

### `tokenops hooks install --client codex`

Codex keeps hooks in `$CODEX_HOME/hooks.json` (`~/.codex/hooks.json` when `CODEX_HOME` is unset), in the same nested shape
Claude Code uses inside `settings.json`, and sends the same `Stop` payload
under the same field names. So the coaching nudge is one handler for both
clients:

```bash
tokenops hooks install --coach --client codex
```

Two things differ and both are handled explicitly.

**The entry is a single command string**, not `command` plus an `args`
array. Codex documents it that way; assuming otherwise would run the bare
binary with no subcommand — a hook that installs, reports success, and
does nothing.

**Codex will not run it until you trust it.** A non-managed hook is
skipped, silently, until its exact definition has been reviewed via
`/hooks` in Codex. The installer says so rather than printing "Wrote …"
and stopping:

```
Not armed yet. Codex skips a hook until you trust it:
  run `/hooks` in Codex, review the tokenops entry, and trust it.
Until then the hook is written but silently not run.
```

`--read-guard --client codex` is **refused**, with the reason: Codex has
no file-read tool, so there is nothing to intervene in.

### `tokenops hooks install --client cursor`

Cursor keeps hooks in `~/.cursor/hooks.json` and fires `stop` when the
agent loop ends:

```bash
tokenops hooks install --coach --client cursor
```

Cursor is the cheapest of the three to support and the easiest to get
wrong.

**Cheapest,** because its `stop` payload carries the turn's token counts
inline — `input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`. There is no transcript to find or parse.

**Easiest to get wrong,** because its cache accounting is a third
distinct convention:

| client | relationship |
|---|---|
| Claude Code | `input_tokens` and the cache figures are **disjoint** |
| Codex | `cached_input_tokens` sits **inside** `input_tokens` |
| Cursor | **both** cache figures sit inside `input_tokens` |

Cursor's own team put it plainly: *"input_tokens is inclusive of
cache_read_tokens and cache_write_tokens… only 14 of the 1.18M input
tokens were genuinely uncached."* Pricing that the Claude Code way bills
1.18M tokens at the full input rate instead of 14.

Two more things the implementation accounts for. Cursor's numbers are
**cumulative per turn** and it sends identical values on `stop` and
`afterAgentResponse` for the same `generation_id` — so turns are
deduplicated on that id rather than summed. And the token fields are
**optional**: absent means "not reported", never zero-cost.

Installing it is also what gives Cursor **per-turn spend**. Cursor keeps
no per-turn record on disk and its usage endpoint reports only a
percentage of plan consumed — the hook payload is the only place those
token counts ever exist. `coach-hook` records each one to
`~/.tokenops/cursor-turns/`, and the daemon ingests it, so Cursor joins
every other client in `spend`, burn rate, forecast and top consumers.
Without the hook, Cursor stays quota-only.

The schema is **flat**, unlike Claude Code's and Codex's — an event maps
straight to entries carrying `command`, with a top-level `"version": 1`
and a lower-camel event name. `--read-guard --client cursor` is
**refused**: `beforeReadFile` is observe-only, so a read cannot be
declined there.

### `tokenops hooks install --read-guard --client opencode`

opencode is the **only client besides Claude Code where read-guard can
actually refuse a read**. Codex has no file-read tool at all; Cursor's
`beforeReadFile` is observe-only. opencode has a `read` tool and a
`tool.execute.before` hook that blocks when it throws.

What it does not have is a config file to merge into — its extension
point is a JavaScript module. So tokenops **generates** one at
`~/.config/opencode/plugins/tokenops-read-guard.ts`:

```bash
tokenops hooks install --read-guard --client opencode
# restart opencode to load it
```

Generated rather than shipped, deliberately. A published package would
mean a second artifact, a second release path, and version skew between
the plugin and the binary it calls. The generated file has none of that:
it is written by the binary, names the version that wrote it, hardcodes
the absolute path to that same binary, and is rewritten in place by the
next install. It is a config file that happens to be JavaScript — the
other three clients get one too, theirs just happen to be JSON.

Delete the file to remove it.

**One file serves opencode 1.x and opencode 2.** Its default export has
a `server` function, which 1.x calls for its hooks, and a `setup`
function, which opencode 2 calls to register them: read-guard on
`tool.execute.before` (2.x calls the file `path`) and route-guard on the
session's prompt hook. Each version uses the half it knows. Coaching
nudges are toasts in opencode's TUI, which a 2.x plugin cannot reach, so
`--coach` reaches 1.x only. TokenOps reads opencode 2's sessions from the
same database, in both versions' tables.

The daemon brings the file up to date when it starts, so an upgrade
repairs it without a reinstall. It keeps the hooks, binary and budget
the file already had, folds in the separate `tokenops-v2.js` older
versions wrote, and never touches a plugin TokenOps did not write.

**It fails open.** A guard sitting in front of every file read must never
be the reason a read cannot happen. A missing binary, a spawn failure,
unparseable output — all allow the read. Only an explicit deny throws,
and the throw happens outside the `try` so a bug in the error handling
cannot swallow a real refusal.

**It respects `coaching.delivery`** like every other read-guard surface:
`intervene` refuses, anything below records what it would have refused.

`--coach --client opencode` works too, and takes a different route from
every other client. opencode has no end-of-turn hook that accepts a
return value, so the nudge is delivered as a **TUI toast** — the one
channel a plugin has to reach you. The trigger is `session.idle`.

It is also the only client that already knows what its turns cost:
opencode records a per-message `cost`, and the coach uses that in
preference to inferring one from a rate card. That is how a session on a
model no rate card carries still produces a number.

A zero cost is usually *correct* — a Copilot turn or one through
opencode's own gateway is included in a subscription. But the budget is
denominated in **API-equivalent** spend, the counterfactual, so a zero
falls back to the rate card exactly as it does for Claude Code on a
subscription. When neither can answer, the model is reported as unpriced
rather than counted as free.

The whole session is recomputed on each idle rather than accumulated.
`session.idle` fires every time you stop typing, and a hook that added on
every fire would inflate a session without bound.

#### Codex sessions may report $0

The shipped rate card has no entry for `gpt-5.5` or `gpt-5.6-luna`, which
is what Codex runs today. Those turns cannot be priced, and a session
reported as free when nobody could cost it is exactly the failure this
tool exists to find — so `coach stats` names them instead of staying
quiet:

```
  not priced — no rate card for these models:
    gpt-5.5                14 turn(s)
    their spend reads as $0 above. Add rates via `pricing.path` to count them.
```

and it will not tell you your spend is lean on the strength of numbers it
could not compute.

## Routing and policy

### `tokenops mode [passive|active]`

What TokenOps may do on its own. With no argument it prints the mode and,
per subsystem (coaching, smart routing, the read guard, compaction,
routing approval), the configured and effective authority and the
setting that controls it.

- `passive` — collect and analyse on demand (the default).
- `active` — passive, plus live interventions: routing rules applied to
  proxied traffic, and a watcher evaluating [budgets](#tokenops-budget-list-set-unset).

```bash
tokenops mode                    # print the mode
tokenops mode --json             # the daemon API's GET /api/mode
tokenops mode active
```

Active mode does its work inside the daemon, so it is a no-op without one
running. See [Configuration](./configuration.md) for what each mode
does and how it differs from the coach's autonomy.

### `tokenops routing {proposals|decide|rule}`

Model-routing rules and the proposals they raise.

```bash
tokenops routing rule list --json
tokenops routing rule set anthropic claude-opus-5 claude-sonnet-5 --quality 0.9
tokenops routing rule set anthropic 'claude-fable-5*' claude-sonnet-5 --quality 0.9
tokenops routing rule unset anthropic claude-opus-5
tokenops routing proposals --json
tokenops routing decide "anthropic|claude-sonnet-5|claude-opus-5" approve
```

`rule set` adds or updates a rule, upserted by provider and from-model; a
trailing `*` in from-model is a prefix match. `--quality` is the
confidence (0–1] that the target preserves task quality, and `--fallback`
(repeatable) names a model to use if the target is unavailable. In
passive mode a rule only shows its would-be savings on recorded history;
with `mode active` the proxy rewrites matching live requests.

`proposals` lists model upgrades a rule wanted but your
[preferred model](#tokenops-preferred-model-list-set-unset) refused. Each is
a real choice waiting on you — take the proposed model, or stay on the
preferred one — and nothing applies until you answer. `decide` answers one
by its key: `approve` routes matching requests to the proposed model from
the next request, `deny` keeps the requested one. The menu bar and
`POST /api/routing/decisions` answer the same way; an agent's
`tokenops_routing` `decide` applies only once you confirm it at your MCP
client. With a [presence key](#tokenops-boundaries-presence-enroll-enforce-status-rotate)
enrolled, `decide … approve` shows the Touch ID dialog and signs the
approval; with `boundary_approval: presence` an upgrade approval counts only
when signed, and an agent cannot approve one at all.

### `tokenops boundaries {status|promote|demote|headroom-pause|loop-breaker}`

[Action boundaries](https://team.klarlabs.de/guide/boundaries) are limits you set in
`config.yaml` before work starts: past $20 in a session, ask me; no
subagents above Sonnet without an admin; no new work once the week's plan
is under 5%; hold routes to Opus; ask before figures go to the team.

```bash
tokenops boundaries status          # each rule, how often it matched, where it binds
tokenops boundaries status --json   # the daemon API's GET /api/boundaries
tokenops boundaries promote session-spend
tokenops boundaries demote session-spend
tokenops boundaries headroom-pause  # add the headroom pause to an older config, in shadow
tokenops boundaries loop-breaker    # add the loop breaker to an older config, in shadow
```

Every rule starts in **shadow**: it records what it would have done and
changes nothing. `status` shows how often each rule matched — for a
shadow rule, how often it would have fired — and, per client, whether it
is enforced, in shadow, advice only (Codex and Cursor, whose hooks cannot
refuse a call), or waiting on its hook; for `model_route` and `share`
rules, whether the router, the team upload and the OTel export enforce
them or are idle. `promote` writes `stage: enforce`
to the rule in `config.yaml`; `demote` puts it back in shadow. Neither
has an MCP equivalent: an agent must not change the limits set on it.

**The headroom pause** is the boundary `tokenops init` writes on a new
config: once the tightest plan window TokenOps knows is down to 10% left, the
agent's next tool call asks first (`action: continue`, `when: { plan_left_pct: "<= 10" }`, `require: ask`). It starts in shadow, and the
glance's findings say how many sessions it would have paused this week;
`tokenops boundaries promote headroom-pause` makes it pause. A config
written before init did this is not changed: the findings suggest
`tokenops boundaries headroom-pause`, which adds the rule in shadow and
installs the pause point in Claude Code and opencode. A config that already
has a `continue` rule on `plan_left_pct` is left as it is. Like `promote`,
it has no MCP equivalent.

**The loop breaker** is the other boundary init writes: once the agent's
latest tool calls go round a loop with no file changed (the same call four
times, or the same two or three calls three times), the next call asks
first (`when: { loop_calls: ">= 4" }`, `require: ask`). It starts in
shadow; `tokenops boundaries loop-breaker` adds it to an older config, and
`tokenops boundaries promote loop-breaker` makes it pause. It keeps tool
names and input hashes only, and is evaluated in Claude Code only.

**Where and when.** Two conditions limit a rule to a project folder and a
time of day; a rule with them applies only when they hold, together with its
other conditions, and a rule without them applies everywhere and always:

```yaml
boundaries:
  - name: acme-office-hours
    action: continue
    when:
      folder: "~/clients/acme, ~/clients/globex/*"
      hours: "mon-fri 09:00-18:00, 22:00-06:00"
      session_cost_usd: ">= 10"
    require: ask
```

`folder` is one or more absolute folders, separated by commas (`~` is your
home): the session's working directory matches when it is one of them or
inside one. Like an allocation rule's `dir`, a folder may be a glob
(`~/clients/*`); case matters and symlinks are not resolved. Claude Code's
hook and route-guard send the working directory, and opencode's plugin its
own; Codex and Cursor do not, so `status` shows a folder rule as not
evaluated there, and `model_route` and `share` rules do not take `folder`.
`hours` is one or more windows, separated by commas, on a 24-hour clock in
this machine's time zone: `09:00-18:00` (the end is not included),
`22:00-06:00` across midnight, an optional weekday or range first
(`mon-fri 09:00-18:00`, `sat`, `fri-mon`, `mon+wed 08:00-09:00`), or
weekdays alone for whole days (`sat-sun`). A window across midnight
belongs to the day it starts on: `fri 22:00-06:00` runs into Saturday
morning. `status` lists each rule's conditions and says whether its hours
hold now; a refusal and `tokenops explain` say which folder and time
matched. The working directory is compared and dropped, never recorded.
Organisation rules take both too: a condition only narrows the rule it is
on, so it never loosens a local rule, and an approval sent to the team
plane carries neither.

### `tokenops boundaries presence {enroll|enforce|status|rotate}`

[Presence-checked approval](https://team.klarlabs.de/guide/boundaries#presence-checked-approval) on
a Mac with a Secure Enclave: an approval signed by a key that asks for
Touch ID, a paired Apple Watch or your password every time.

```bash
tokenops boundaries presence enroll             # create and enrol the key: one dialog
tokenops boundaries presence enroll --replace   # new Mac or lost key: approvals held 15 minutes
tokenops boundaries presence enforce            # boundary_approval: presence
tokenops boundaries presence enforce --strict   # presence-strict: Claude Code's prompt no longer approves
tokenops boundaries presence enforce --off      # back to channel
tokenops boundaries presence status [--json]
tokenops boundaries presence rotate             # new key, signed by the old one: two dialogs, no hold
```

`enroll` writes the key to `~/.tokenops/presence` (the public key, the
Secure Enclave's wrapped private key, which only this Mac can use, and the
key's signature over its enrolment). `enforce` refuses while no key is
enrolled. `status` says what is set and enrolled, how many approvals were
not honoured, and whether the `tokenops-presence` helper was found. The
helper ships beside `tokenops` in the macOS release and inside
`TokenOps.app`. None of these has an MCP equivalent: enrolling raises the
dialog, and requiring it is the operator's act, as `promote` is.

### `tokenops approve <id>`

Answer a boundary that asked or refused. The id is the `bnd-…` one in the
refusal, or listed under "Waiting on you" in `boundaries status`.

```bash
tokenops approve bnd-1a2b3c4d5e6f            # the agent's next step
tokenops approve bnd-1a2b3c4d5e6f --session  # the rest of the session
tokenops approve bnd-1a2b3c4d5e6f --confirm  # show what waits, then ask y/N
tokenops deny bnd-1a2b3c4d5e6f --session
```

`--confirm` shows the rule, what was crossed and where, and answers only
on `y`; the menu bar's *Approve…* opens a terminal running it. For a
route or a share, `--session` covers the next 24 hours.

Without `--session` the answer covers one step, and the boundary asks
again after it. The answer is recorded on the decision with your user
name, so `tokenops explain bnd-…` shows the rule, what was crossed, and
who answered how. `approve` runs only at a terminal: the agent a boundary
stopped can run commands too. The MCP tool `tokenops_boundaries`
(`action=decide`) can only deny; `POST /api/boundaries/decisions` approves
only with a `presence_proof`.

With a presence key enrolled, `approve` shows the Touch ID dialog, which
names the rule, the action and the session, and signs the approval; the
answer is recorded with the key that signed it. With `boundary_approval:
presence` an approval that cannot be signed (no helper, nobody at the
screen, a locked screen, a cancelled dialog) is refused, and so is the
same command run through `script` or another pseudo-terminal without you at
the sensor. Under `channel` it goes on unsigned with a note, unless you
cancelled the dialog. `deny` is never signed.

Decisions an organisation rule sent to a role cannot be approved here;
`approve` says who answers them. `deny` still works.

### `tokenops deny <id>`

Refuse what a boundary asked about: the agent's next step, or with
`--session` the rest of the session. It is recorded like an approval.

### `tokenops preferred-model {list|set|unset}`

The most expensive model a provider may be routed to. The preferred model
is a ceiling, not a default: a routing rule that would move you to a
pricier model is refused and referred to you as a proposal; routes to
cheaper models still apply on their own.

```bash
tokenops preferred-model list
tokenops preferred-model set anthropic claude-sonnet-5
tokenops preferred-model unset anthropic
```

### `tokenops experiment {start|status|stop}`

Bounded, explicitly enrolled trials of a routing change: a proxy-backed
paired comparison of a baseline model against a variant, for at most 10
matched pairs (`--pairs`) and 14 days (`--days`).

```bash
tokenops experiment start anthropic claude-opus-5 claude-sonnet-5 \
  --objective tokens --min-improvement-pct 10 --guardrail quality
tokenops experiment status experiment:6f1c…
tokenops experiment stop experiment:6f1c… --reason "enough evidence"
```

`--objective` is the primary metric — `tokens`, `plan_quota_tokens`,
`metered_cost_usd`, `latency_ms` or `attention_minutes` — and
`--min-improvement-pct` the per-pair improvement the variant must show.
`--guardrail` (repeatable) is `quality` or `metric:max-regression-pct`;
include `quality`. `status` and `stop` print the trial's persisted state
and what TokenOps has learned about the route, as JSON. Stopping keeps the
evidence. [`tokenops verify`](#tokenops-verify) compares the arms.

## Rules

### `tokenops rules {analyze|conflicts|compress|inject|bench}`

Rule Intelligence subsystem — analyses ClaudeMD / agent-instruction
files, detects conflicts, compresses, injects, benchmarks.

## Command-output compression (`fmt`)

Deterministic per-command output compression — shrink a command's stdout
before it enters an agent's context, keeping every critical line (errors,
failures, changed state) and dropping noise. 51 formatters ship built in;
loss level and extra formatters are configured under
[`optimizer.command_fmt`](./configuration.md#command-output-compression-command_fmt).

### `tokenops fmt -- <command> [args...]`

Runs the wrapped command, compresses its stdout, passes stderr through,
and **propagates the exit code** so agents still see failures. The full
raw output is written to `~/.tokenops/recovery/` so nothing is lost.

```bash
tokenops fmt -- git status
tokenops fmt --level aggressive -- npm install
tokenops fmt --quiet -- go test ./...
```

Flags: `--level` (override loss level for this run), `--raw-on-error`
(forward raw stdout on non-zero exit, default on), `--emit` (record an
OptimizationEvent), `--no-recover`, `--quiet`.

### `tokenops fmt eval [--corpus <dir>] [--json]`

Judges fmt by what an agent can still find in the compact output, in
three parts.

**What fmt never drops.** Every line a formatter marks critical (errors,
failures, changed state) survives verbatim at every loss level. The
engine checks this on every run, for built-in and config formatters
alike: a formatter that would drop a critical line forwards the raw
output instead.

**Evidence recall.** A corpus of 64 command outputs, at least one for
every built-in formatter and three (`rg`, `find`, `ls`) for the generic
scrub, has the evidence an agent needs from each labeled by hand:
failing tests, file:line of errors, error messages, changed paths, exit
status, failed resources, counts. Recall is the share of those items
still found in the compact output, verbatim or after collapsing
whitespace. The labels are independent of the formatters, so they also
catch a line an agent needs that a formatter never called critical.
Losing any critical item fails the command; counts, matches and
installed-package lists may be summarized, and the report lists each one
a level gives up.

**Savings.** Bytes and estimated tokens saved at each level.

On the bundled corpus every one of the 157 critical items is recovered at
every level; overall recall is 100% / 97% / 94% (conservative /
balanced / aggressive) for 0% / 50% / 59% of the bytes saved.

```bash
tokenops fmt eval                    # the bundled corpus
tokenops fmt eval --json             # every case, item and miss
tokenops fmt eval --corpus ./labeled # your own captures
```

`--corpus` reads `<id>.txt` outputs, each beside an `<id>.json` label:

```json
{"evidence": [
  {"kind": "failing_test", "text": "TestLogin"},
  {"kind": "error_location", "text": "login_test.go:9"},
  {"kind": "count", "text": "1 failed"}
]}
```

The id's first dot-separated token picks the formatter (`go.fail.txt` →
`go`); `"command"` in the label overrides it. Kinds `failing_test`,
`error_location`, `error_message`, `changed_path`, `exit_status` and
`failed_resource` are critical; `count`, `match` and `installed` are not,
and `"critical": true|false` overrides an item. A label whose text is not
in its own output is rejected. The exit status is non-zero when a
critical item is lost, so the command can gate your captures in CI. Only
the built-in formatters are judged, not those in your config.

Recall says what the agent can still find, not whether its work went
better; `tokenops fmt experiment` (below)
compares sessions with and without fmt on outcomes.

### `tokenops fmt bench --corpus <dir>`

Measures per-file and aggregate byte / estimated-token savings at each
loss level over a directory of captured command outputs
(`<command>.<label>.txt`). Use it to quote real numbers for your own
command mix. The bundled captures live in
`internal/contexts/optimization/eval/testdata/fmtrecall`.

### `tokenops fmt hook [--shell zsh|bash]`

Emits shell wrapper functions for the formatter-backed commands, gated on
`TOKENOPS_FMT=1` so compression only activates where you want it (e.g. an
agent session) and interactive use is untouched.

```bash
eval "$(tokenops fmt hook)"      # add to ~/.zshrc
export TOKENOPS_FMT=1            # the agent sets this to activate
```

### `tokenops fmt recover <id>`

Prints the full stored output for a prior run and records the re-access —
the signal `fmt learn` uses to detect over-compression.

### `tokenops fmt learn [--apply]`

Mines the recovery index (compression + re-access records) and reports
which commands need a formatter (falling back to the generic scrub) and
which are over-compressing. `--apply` writes the safe loss-level tuning
back to config locally; new-formatter candidates are printed as a
paste-ready stub. The `tokenops_fmt (view=learn)` MCP tool returns the same
report to agents. The formatters stay deterministic — learning proposes,
it never mutates runtime behaviour.

### `tokenops fmt experiment [off|on|<percent>]`

Judges fmt on outcomes rather than bytes saved. With the experiment on, a
share of sessions, drawn at random from the session id, get their command
output raw: fmt forwards stdout unchanged, prints no stats line, and
records what it would have saved. The rest get fmt's output as usual. The
session id is read from `TOKENOPS_SESSION_ID`, else Claude Code's
`CLAUDE_CODE_SESSION_ID`; a command run without one is compressed and left
out. Each run's arm and the share it was drawn with are recorded in the
recovery index.

```bash
tokenops fmt experiment on        # half the sessions get raw output
tokenops fmt experiment 25        # a quarter
tokenops fmt experiment           # the setting and the comparison
tokenops fmt experiment off
```

With no argument it compares the arms over the last `--days` (30) on what
the sessions' transcripts show: first-try instructions, reworked
instructions, repeated tool calls (a call the session had already made
with the same arguments, such as a command run again), tokens per
instruction, and outcomes recorded with `tokenops outcome` under the
session id as the execution id. Each measure is computed per session and
compared over sessions, since the session is what was randomized: counts
per arm, the difference, and a rough 95% interval. Until each arm has 4
sessions and 20 instructions it says "not enough sessions yet" and
compares nothing; an interval that crosses zero reads "no difference
shown", and one that excludes zero is reported as a lead to check, not a
finding, since several measures are compared at once. `--json` emits the
report. Off by default; the setting is `optimizer.command_fmt.experiment`.
Agents read the report with `tokenops_fmt (view=experiment)`, and
`tokenops_experiment (action=fmt)` changes the share only after the
operator confirms it.

## Outcomes and verification

### `tokenops outcome {record|detect|check-json}`

Records whether a piece of work succeeded, against an execution ID, so
[`verify`](#tokenops-verify) can weigh savings against quality. Each form
can name the `--decision-id` the outcome evaluates.

```bash
tokenops outcome record exec-42 --result achieved            # achieved | partial | not_achieved
tokenops outcome record exec-42 --result partial --caveat "needed a manual fix" --attention-minutes 15
tokenops outcome detect exec-42 --session-id 9420028c
tokenops outcome check-json exec-42 --file response.json --pointer /status --equals ok
```

- `record` is the operator's assessment; `--attention-minutes` is the
  active human effort the execution took.
- `detect` reads a Claude Code session and records the result of the last
  recognised verifier (a test run, say) after the final edit. With no
  verifier after the last edit it fails and asks you to run tests or
  record a human outcome.
- `check-json` verifies one scalar in a local JSON response, selected by
  an RFC 6901 JSON Pointer.

### `tokenops verify`

Compares resource use and outcomes with and without optimizations. It
attributes recorded events to reconstructed attempts (a pause longer than
`--idle-gap`, default 10m, starts a new one). By default it compares
attempts an optimization touched against those it did not; that view is
observational and does not establish causality.

```bash
tokenops verify --days 30 --each          # list every attempt, not only the comparison
tokenops verify --experiment-id experiment:6f1c… --json
```

When execution-linked randomized assignments exist, it compares a complete
[experiment](#tokenops-experiment-start-status-stop)'s paired arms instead.
With several in the window it compares the only one that can be compared,
or the most recent of several, and says which ("compared experiment:b, the
most recent of 2 comparable experiments; select experiment:a with
--experiment-id"); `--experiment-id` picks another. A randomized
comparison needs complete pairs, measured tokens and explicit outcomes;
otherwise `verify` falls back to the observational view and says why.

Explicit human and verifier outcomes count toward each cohort's assessed
success rate, so a quality drop can flag harm even when the token count
fell; missing outcomes stay unknown. Randomized results are evidence about
that experiment, not a guarantee for other work.

It also lists the [coach holdout](#tokenops-coach-holdout-off-on-percent):
per kind of tip, how many were shown and held back, the expected saving, and
the realized one or why it is not established yet. `--json` gives it as
`holdout`.

### `tokenops optimizations`

Optimizations TokenOps recommended or applied, from the event store.

```bash
tokenops optimizations --since 24h --json
tokenops optimizations --workflow-id research-summariser --limit 200
```

`--since` takes an RFC 3339 time or a duration (default `7d`);
`--workflow-id` and `--agent-id` filter, and `--limit` caps the rows
(default 50).

## Inspection

### `tokenops config show`

Dumps the resolved configuration as YAML (or JSON with `--json`).

### `tokenops audit`

Queries the audit log (`~/.tokenops/events.db`).

### `tokenops events`

Per-kind domain-event counts (workflow.started, optimization.applied,
rule_corpus.reloaded, etc.).

### `tokenops version`

Prints the binary version + commit + build date.
