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
│ Codex [local] PLAN Pro Standard ($100) │  │ Claude [meter]            PLAN Max 20x │
│ ────────────────────────────────────── │  │ ────────────────────────────────────── │
│ Weekly                        91% used │  │ Session                        4% used │
│ [ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━─── ] │  │ [ ━───────────────────────────────── ] │
│ Resets in 30m                          │  │ Resets in 4h 37m                       │
│ Pace: ahead (+37%) · out in 23h 47m    │  │ Pace: behind (-3%) · lasts to reset    │
│                                        │  │                                        │
│ Overage risk:                     HIGH │  │ Weekly · Fable                 0% used │
│ Today:              $28.00+ · 198M tok │  │ [ ────────────────────────────────── ] │
│ 30 days:             $745+ · 2.24B tok │  │ Resets in 5d 17h                       │
│ At API prices; the plan covers it.     │  │                                        │
│ 15% of requests have no price yet.     │  │ Today:                       1.00B tok │
│                                        │  │ 30 days:            $3,214 · 6.10B tok │
│                                        │  │ At API prices; the plan covers it.     │
╰────────────────────────────────────────╯  ╰────────────────────────────────────────╯
```

**Pace** compares the share used with the share of the window gone by:
behind lasts to the reset; ahead says when the window runs out at the
rate so far. The daemon API, the menu bar and MCP report the same pace.

**Cost** is at API list prices where a plan covers the usage. Requests on
models without a list price yet are left out of the money: a `+` marks a
figure that leaves some out, and where most have no price only tokens are
shown, since a total that looks complete but is not misleads.

**Coach** findings follow the cards, most urgent first: what the coach and
the session analysis observed, the figures behind it, and what to do.

```
Coach · 5 findings
  ▲ Codex's weekly window runs out in 23h 32m at this pace
    56% used, 38 points ahead of an even pace; it resets in 5d 18h
    → Put the work that can move on Claude (25% used) until it resets.
  ● Agents re-read 14 unchanged files in full
    about 225k tokens of context spent on files already read, across 63 sessions
    → tokenops coach set waste autonomous lets the read guard refuse them.
  · The coach is observing and says nothing
    it would have given 560 tips so far
    → tokenops coach preset advise lets it speak, once per kind of thing.
```

They cover a quota window that runs out before it resets (and a plan with
room to take the work), files the agent re-reads unchanged, sessions past
their budget, the one change that would most improve how sessions go,
session sources that cannot be read, kinds of advice the coach stopped
giving because they were ignored, and what the coach would have said while
it only observes. The session analysis reads a week of transcripts, so the
daemon runs it in the background every three hours; the section says how
old it is. `tokenops glance --findings` lists every finding, and
`GET /api/findings` serves the same list. Findings carry figures only,
never prompt or file text.

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
PLAN                       WINDOW            USED  RESETS    PACE
Codex Pro Standard ($100)  Weekly             91%  30m       +37% · out in 23h 47m
Claude Max 20x             Session             4%  4h 37m    -3% · lasts
                           Weekly · Fable      0%  5d 17h
```

Colour follows the terminal and `NO_COLOR`; `--no-color` (or `--color
never`) prints plain text. `$COLUMNS` sets the width.

### `tokenops menubar`

On macOS, installs the menu bar app that ships with the Homebrew install
to `~/Applications` and opens it: the busiest plan window next to the icon,
and a panel with every plan's windows, pace and cost and the coach's
findings. Its menu has Launch at Login; upgrades refresh the installed copy.

## Setup

### `tokenops init`

Scaffolds the config (sqlite + rules on, idempotent) **and wires this
machine**: it registers the MCP server with every installed host and
installs the Claude Code hooks. `--detect` adds a report of the clients it
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
says which client reported it. A plan you bound yourself is never
changed: if a client reports a different one, `init` points it out.

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

### `tokenops plan {list|set|unset|catalog|headroom}`

```bash
tokenops plan list              # configured bindings + headroom
tokenops plan catalog           # all 30 plans and their list prices
tokenops plan set anthropic claude-max-20x
tokenops plan unset cursor
tokenops plan headroom          # live consumption + overage risk
tokenops plan history           # every recorded switch, oldest first
```

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
tokenops plan set anthropic claude-max-20x --price 214.60 --currency EUR
```

A spend limit is recorded with the date it took effect, so an earlier
month keeps its own; `plan history` shows it. A provider billed per token
with an account cap binds to `pay-as-you-go`:

```bash
tokenops plan set anthropic claude-enterprise --spend-limit 1500 --since 2026-09-01
tokenops plan set anthropic claude-enterprise --spend-limit 500 --since 2026-10-01
tokenops plan set fireworks pay-as-you-go --spend-limit 200
```

See [Switching plans](/guide/configuration#switching-plans) and ADR 0008.

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

### `tokenops start`

Starts the daemon in the foreground. Listens on `127.0.0.1:7878` by
default, also advertises `tokenops.local` over mDNS. Stop with
SIGINT / SIGTERM (Ctrl-C).

### `tokenops daemon {install|uninstall|status}`

Supervises `tokenops start` so ingestion survives reboot. macOS writes
a LaunchAgent; Linux writes a systemd user unit. Placeholders in
`deploy/launchd` / `deploy/systemd` are filled with this binary's path
and `$HOME`.

```bash
tokenops daemon install            # write + load
tokenops daemon install --no-load  # write only
tokenops daemon status
tokenops daemon uninstall
```

Install and upgrade wait until the supervised process answers `/healthz`
before reporting success. Supervisor acceptance alone is not treated as a
running daemon; a failed startup exits non-zero with the status and log path
to inspect.

`tokenops serve` is the MCP server and does not ingest. A reboot that
kills an unsupervised `start` while the client respawns `serve` is how
a 27-day outage stayed invisible.

### `tokenops serve`

MCP server over stdio. Wire into Claude Desktop / Code / Cursor /
aider via your client's `mcpServers` config.

### `tokenops status`

Daemon health + `blockers[]` / `next_actions[]`. Falls back to a
self-report when the daemon isn't running so MCP-only deployments
don't hit a dead end.

## Spend + plan headroom

### `tokenops spend`

```bash
tokenops spend                                   # last 7 days, top 5 by model
tokenops spend --by provider --top 3 --since 24h
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

From an agent, `tokenops_configure (setting=usage_meter)` does the same: it reads the
browser session (you allow the keychain prompt) and never asks for the key
in the conversation.

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

- **The key** is `FIREWORKS_API_KEY`, or FireConnect's own key: TokenOps
  runs Claude Code's `apiKeyHelper` only when it is FireConnect's
  `fireconnect key export`. Without FireConnect, a Fireworks key that
  opencode or Codex uses is taken instead. The key is fetched for each reading and is
  never stored or logged.
- **The figures** on a company account are your own spend against your
  per-user cap (`/users/{you}/usageLimits`). On your own account they are
  the month's spend (`/billing/summary`) against the `monthly-spend-usd`
  quota. Your user and account come from FireConnect's
  `~/.fireconnect/minted-key.json`, or from the key itself.
- **Calls** go to `api.fireworks.ai` only. Turn them off with
  `tokenops vendor-usage enable fireworks --disable`.

### Other gateways and API vendors

Every provider you pay per token for and used this month shows in
`plan headroom` as `pay-as-you-go`, with spend estimated from usage.
Where the vendor documents an account endpoint, TokenOps also reads the
vendor's own figures every 15 minutes:

| Vendor | Endpoint | Shows |
|---|---|---|
| OpenRouter | `GET /api/v1/key` | the key's spend, against its credit cap when it has one |
| DeepSeek | `GET /user/balance` | prepaid USD balance left |
| Moonshot (Kimi API) | `GET /v1/users/me/balance` | prepaid USD balance left |
| DeepInfra | `GET /payment/checklist` | spend since the last invoice, the limit, prepaid credit |
| Vercel AI Gateway | `GET /v1/credits` | the team's credit balance |

Coding plans report their usage windows instead. A provider whose reader
reports one, with no plan bound, shows as `subscription` with the
vendor's windows, busiest first:

| Vendor | Endpoint | Shows |
|---|---|---|
| z.ai GLM Coding Plan | `GET /api/monitor/usage/quota/limit` | 5-hour and weekly token windows |
| Kimi Code | `GET /coding/v1/usages` | 5-hour, weekly and monthly windows |
| MiniMax Token Plan | `GET /v1/token_plan/remains` | the interval and weekly windows |
| Synthetic | `GET /v2/quotas` | the subscription's request quota |
| Chutes | `GET /users/me/subscription_usage` | the 4-hour and monthly caps |

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

| Gateway | Recognised by | Shows |
|---|---|---|
| LiteLLM proxy | `GET /health/liveliness` | `GET /key/info`: the key's spend against its budget, and when it resets |
| Bifrost | `GET /health` | `GET /api/governance/virtual-keys/quota`: each budget's share used |
| ClawRouter | its host, or `GET /v1/health` | `GET /v1/usage`: the policy's spend against its monthly budget |

Portkey and Cloudflare AI Gateway are not asked: neither lets the key in
use read its own spend.

- **The key** is the one your harnesses already send that vendor: the key
  next to a gateway in Claude Code's settings, Codex's
  `[model_providers]` (`experimental_bearer_token` or `env_key`),
  opencode's `auth.json` and `provider.*.options.apiKey`, or
  `OPENROUTER_API_KEY`, `DEEPSEEK_API_KEY`, `MOONSHOT_API_KEY`,
  `DEEPINFRA_API_KEY`, `CHUTES_API_KEY`, `SYNTHETIC_API_KEY` and
  `AI_GATEWAY_API_KEY`. A key is
  sent only to the vendor whose endpoint it was found for. It is never
  stored or logged.
- **Calls** go only to vendors whose key is on the machine. Turn them off
  with `tokenops vendor-usage enable vendor-accounts --disable`.

### `tokenops vendor-usage enable <source>`

Writes a vendor-usage source's config block to the active config
file so operators don't hand-edit YAML. Nine sources covered:
`claude-subscription`, `cursor`, `github-copilot`, `codex-jsonl`,
`claude-code-jsonl`, `opencode`, `anthropic-admin`, `fireworks`,
`vendor-accounts`. Secrets accept env-var fallback so they don't leak
through shell history.

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

## Explain

### `tokenops explain [term | decision-id]`

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
- the prompt cache hit ratio;
- the session's cost in your currency — marked "value" when a plan
  covers it, since then it is what the work was worth, not what you paid;
- the coach's open tip, unless the coach is quiet or off.

It reads only what Claude Code hands it and files TokenOps already keeps,
never the event store or the network, and takes about 12 ms. Subagent
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
Approval requests are always shown. A [`model_policy`](/guide/configuration#model-policy-model-policy)
applies whatever the coach is set to.

### `tokenops coach stats`

What the coach saw and did, from its two ledgers: each session's spend
against budget fractions, the quota and compact tips it gave and what it
held back; and files read again, which re-reads were waste, and which the
read guard refused. Both ledgers fill while the coach only observes, so
this shows what you would hear before you let it speak. `--json` gives
`budget` and `read_guard`.

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
[Configuration](/guide/configuration) for how it differs from `mode`.

### `tokenops coach prompts`

Heuristic prompt-quality feedback. Walks
`~/.claude/projects/**/*.jsonl` AND `~/.codex/sessions/**/*.jsonl`,
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
```

Output leads with a **BIGGEST WIN** panel — the highest-impact
recommendation with evidence quotes pulled from your data, plus
projected savings (turns / tokens / dollars / hours) computed
from your per-turn averages.

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
tokenops dx --json
```

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
the number that reads as $94k when cache-aware pricing says $10k. It is
there for comparing tasks against each other, never as spend.

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

### `tokenops daemon restart`

Restarts the supervised unit so it re-reads config. The daemon loads its
configuration once, at boot, so a config write that is not followed by a
restart has not taken effect yet. The command waits up to 20 seconds for the
new process to answer `/healthz`; success means the restart is live rather
than merely accepted by launchd or systemd.

```bash
tokenops daemon restart
```

The commands that write config — `plan set/unset`, `provider set/unset`,
`vendor-usage enable` — do this for you. Pass
`--no-restart` when writing several keys in a row, then restart once.

Unsupervised (no unit installed), there is nothing to bounce: those
commands tell you to Ctrl-C and re-run `tokenops start` instead of
claiming a restart that did not happen.

## Hooks

TokenOps wires three hooks into a client, each independent:

| Hook | Fires | What it does |
|---|---|---|
| `--coach` | end of turn | the coaching nudge, against a per-session budget |
| `--read-guard` | before a file read | refuses a redundant re-read of an unchanged file |
| `--route-guard` | when you submit a prompt | states the case for a cheaper model when the one in use is over-spec |

`--client` selects which client to act on, and **install, status and
uninstall all take it**:

| `--client` | Where the hooks live |
|---|---|
| `claude-code` *(default)* | `~/.claude/settings.json` |
| `codex` | `~/.codex/hooks.json` |
| `cursor` | `~/.cursor/hooks.json` |
| `opencode` | a generated plugin in `~/.config/opencode/plugins` |

```bash
tokenops hooks install --route-guard        # wire the model-fit guard
tokenops hooks status                       # what's wired for Claude Code
tokenops hooks status --client cursor       # ... and for Cursor
tokenops hooks uninstall --route-guard      # remove one hook
tokenops hooks uninstall                    # remove every tokenops hook
```

`uninstall` with no hook named removes all three. Naming one removes only
that one, on every client — including opencode, where the generated plugin
is rewritten with the remaining halves rather than deleted, and deleted
only once nothing is left in it.

`status` reports the hook, its event, and the binary each entry calls, and
flags an entry pointing at a different binary than the one you ran — which
is what a stale `hooks install` from an older install looks like.

### `tokenops hooks install --client codex`

Codex keeps hooks in `~/.codex/hooks.json`, in the same nested shape
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

**opencode 2** does not run 1.x plugins, so for it tokenops writes a
second file, `tokenops-v2.js`, in the same folder: read-guard on
`tool.execute.before` (2.x calls the file `path`) and route-guard on the
session's prompt hook. The installer writes the 1.x file when opencode 1
is installed, the 2.x file when opencode 2 is (`opencode2` on your PATH,
or `opencode --version` reporting 2), and both during a transition; each
version skips the other's file. Coaching nudges are toasts in opencode's
TUI, which a 2.x server plugin cannot reach, so `--coach` applies to 1.x
only for now. TokenOps reads opencode 2's sessions from the same
database, in both versions' tables.

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

## Replay

### `tokenops replay [SESSION_ID]`

Replays past prompts through the optimizer pipeline.

```bash
tokenops replay sess-abc123 --json
tokenops replay --workflow-id research-summariser --since 24h
tokenops replay --agent-id planner --since 7d --limit 200
```

Add `--workflow-id ID` to also run the waste detector against the
reconstructed workflow trace.

## Rules + governance

### `tokenops rules {analyze|conflicts|compress|inject|bench}`

Rule Intelligence subsystem — analyses ClaudeMD / agent-instruction
files, detects conflicts, compresses, injects, benchmarks.

### `tokenops eval`

Optimizer eval harness — gates new model/prompt configurations
against a baseline.

### `tokenops coverage-debt`

Risk-weighted coverage debt report.

## Command-output compression (`fmt`)

Deterministic per-command output compression — shrink a command's stdout
before it enters an agent's context, keeping every critical line (errors,
failures, changed state) and dropping noise. 46 commands ship built in;
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

### `tokenops fmt bench --corpus <dir>`

Measures per-file and aggregate byte / estimated-token savings at each
loss level over a directory of captured command outputs
(`<command>.<label>.txt`). Use it to quote real numbers for your own
command mix.

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
