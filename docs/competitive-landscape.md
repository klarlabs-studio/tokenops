# Competitive landscape, gaps, and plan

Checked 2026-10-22 against each product's own site, repository, changelog or
release notes (previous full check 2026-10-03, partial 2026-10-06). Items
marked **[U]** came from a third-party page or search snippet and are
unverified. Re-check before quoting any of it externally; this field moves
weekly now, not monthly.

## Summary

The field still splits in two, but both halves moved toward TokenOps in
October:

- **Individual tools** (ccusage, tokscale, CodexBar, Claude-Code-Usage-Monitor,
  agentsview) read local transcripts or vendor meters and **report**. They win
  on harness breadth and zero setup. CodexBar now displays provider-reported
  spend budgets and per-session performance; agentsview sets commits and PRs
  beside a date range. None of them acts.
- **Team and enterprise tools** (Anthropic's own analytics, Datadog, Faros, DX,
  Jellyfish, Swarmia, Quesma, gateways like LiteLLM, Portkey, Cloudflare) sell
  to leadership. In October, **hard budgets with approval flows** arrived for
  teams (Harness AI Budgets, GitLab per-user credit caps, OpenAI Business
  per-seat limits), and several leaders now say "local, derived only" (Quesma,
  PointFive, Faros).
- **The vendors themselves** are the fastest-moving rival. Claude Code's
  `/usage` now names waste and applies the fix, warns before a resume eats the
  5-hour window, and auto mode is the default permission mode on every plan.
  OpenAI shipped a personal Codex usage dashboard with per-chat cost, headroom
  widgets and banked resets.
- **A small acting tier appeared**: Pushary stops a session at a daily budget
  and routes approvals to a phone; Peras [U] pauses Claude Code and Codex at
  90% of the weekly limit; a dozen tiny hook repos warn or block on budget.

Of the five positions this doc called unoccupied on 2026-10-03, two still are,
two are contested, and one is contested only in wording:

1. **Headroom on subscription plans, acted on.** *Contested.* Showing windows
   is now table stakes: CodexBar (106 providers), each vendor's own meter, and
   OpenAI's widgets. Acting on them is contested by Claude Code (resume
   warning, retry watchdog, Claude only) and Peras [U] (pause at 90% weekly).
   Still nobody else acts on headroom across harnesses.
2. **Action inside the live session, per developer.** *Contested.* Claude
   Code's `/usage` fixes (2.1.305) and Pushary's cost guard now act for one
   developer; Tokenade compresses agent input on the laptop across 10+ agents,
   which overlaps fmt. Nobody else combines read-guard, compression, budget
   advice and boundaries in one place.
3. **Local-only, no proxy.** *Contested in wording.* Quesma now says
   "Encrypted on the laptop", PointFive TokenShift analyses locally and ships
   derived metadata, Faros writes about on-device classification. All three
   still feed a hosted product; TokenOps' claim that the core works with no
   upload at all still holds, but "nothing else stays on the laptop" no longer
   reads as true.
4. **Which account paid, through routers.** *Still unoccupied.* CodexBar reads
   the Vercel AI Gateway's budget (0.78) but does not attribute a request to
   the account that billed it.
5. **Agent DX graded for the developer.** *Still unoccupied across harnesses*,
   but narrower: Claude Code's `/usage` warnings and OpenAI's "Top consuming
   chats" each cover their own harness; Braintrust Coding Agent Insights does
   it for teams.

The October positioning, **guardrails for coding agents** ("Know what's left.
Set what's allowed. Let your agents work."), lands in a field that got busy the
same month: vendor classifiers (Claude Code auto mode, Codex Guardian) now own
the default per-tool approval, admins get model and budget policy from each
vendor, and team buyers get approval flows from Harness and GitLab. TokenOps'
defensible claim is narrower and still real: **limits on plan headroom, budget
and session cost, set by the developer or signed down by a team, enforced
locally and the same way across harnesses.**

## The field

### Individual and local

| Tool | What it does | Acts? | Harnesses | Maturity (2026-10-22) | Beats TokenOps on |
|---|---|---|---|---|---|
| [ccusage](https://github.com/ccusage/ccusage) | Tokens and cost by day/session/5h block, statusline | No | 18 sources | 19.1k★, v20.0.40 (10-21), patch releases only | Mindshare (`npx ccusage`), zero setup |
| [CodexBar](https://github.com/steipete/CodexBar) | Menu bar: windows, credits, spend, provider spend budgets, session throughput and tool errors, threshold shell hooks | Small: shell hooks, Stay Awake | 106 providers | 22.7k★, v0.79.0 (10-20), eight releases since 10-04 | **Closest headroom rival.** Ambient GUI, provider breadth, release pace |
| [tokscale](https://github.com/junhoyeo/tokscale) | Token and cost CLI, leaderboard, groups with members and calendar periods | No | 50+ incl. Claude Desktop | 5.7k★, v4.20.0 (10-17) | Breadth, virality, a social team angle |
| [agentsview](https://github.com/kenn-io/agentsview) | Session search, cost dashboards, commits and PRs per date range, tool-sequence panel, conversation memory plugins; hosted sync | No | 50+ | 6.2k★, v0.45.0 (10-09) | Session archive; memory; a cheap team view |
| [Claude-Code-Usage-Monitor](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor) | Live TUI, burn rate, P90 limit forecast | No | Claude Code | 8.8k★, v4.0.0 (06-27), dormant since 07-05 | Pace forecasting UX |
| [governor](https://github.com/0xhimanshu/governor) | Compacts tool output, compresses memory files, scope-drift contracts | **Yes** | CC (hooks) + rules files | 131★, v0.2.5 (09-06), no pushes since | Rules-file fallback everywhere |
| [ccflare](https://github.com/snipeship/ccflare) | Proxy that load-balances accounts | **Yes** | Anthropic, OpenAI APIs | 1.05k★, stalled since 2026-04 | Routes around limits |
| [Pushary](https://pushary.com/blog/limit-ai-agent-token-usage) (new here) | Cost guard reads session cost from the transcript and kills the session at a daily budget; approval gate routed to phone or Mac, audit log | **Yes** | Claude Code, Qwen Code, Droid, Devin CLI; says Codex, Gemini CLI, Cursor "work the same way" | Paid; post updated 10-04; local vs hosted not stated | **Nearest to "set what's allowed"** for a solo developer; phone approvals |
| Peras (new) [U] | Pauses Claude Code and Codex at 90% of the weekly limit, notifies at 75% | **Yes** [U] | CC, Codex [U] | Launched 2026-10-21 [U] | Headroom-triggered pause, one feature, one line |
| [Tokenade](https://tokenade.net/en/solutions/software-teams/engineering-managers) (new) | Compresses agent input on each machine; claims 39% lower cost on its own benchmark | **Yes** | 10+ agents | Team page 10-15; no team console | Direct overlap with fmt |
| Hook-sized enforcers (cc-cost, agentwatchdog, terse, claudepace, coding-agent-guardrails) | Per-project budgets with warn/block hooks, budget circuit breakers, destructive-command blocks | **Yes** | Mostly CC | 0–2★ each, 09–10 | Signal, not threat: the idea is obvious |
| Menu-bar long tail (ai-usagebar, brow, codex-usage-badge, Developer-Island, Usagebar, TokenBar [U]) | 5h and weekly bars, per-account notch, Windows and Linux widgets | No | 1–4 vendors | ai-usagebar 715★ v1.41.0 (10-18); brow 57★ v0.5.0 (10-21) | Linux and Windows presence we lack |
| Claude Code `/usage`, `/insights`, `/cost` | Plan bars; waste warnings with one-key fixes (2.1.299, 2.1.305); resume warning on 5h share (2.1.304); retry watchdog on limits; gateway spend limit in dollars | **Yes** (fixes on request; auto mode default) | Claude Code | First party | Authoritative, built in, now *acts* on Claude waste |
| OpenAI Codex usage | Personal dashboard (Enterprise/Edu, 10-04): 7D/30D history, top consuming chats, per-chat credits and estimated cost; widgets (10-02); banked resets (10-21) | Partly (apply a saved reset) | Codex, ChatGPT | First party | Per-chat cost and headroom for Codex users, no setup |
| Cursor dashboard | Allowance per pool | No | Cursor | First party | Authoritative for its own plan. [U] New weekly pool shared with Grok (10-12) |

### Team and enterprise

| Product | Buyer | Acts? | Data | Beats TokenOps on |
|---|---|---|---|---|
| Anthropic analytics, OTel, managed settings | Admins | **Yes**: spend caps, alerts, `deniedModels`/`allowedProviders` (2.1.283/285), approval `ceiling` (2.1.290), `safety_event` OTel (2.1.307) | Anthropic | Enforced caps and model policy; every stopped tool call as telemetry |
| OpenAI Business and Enterprise | Admins | **Yes**: monthly credit limits per seat type, per-user overrides (help page updated 10-19) | OpenAI | Per-seat limits on the plan itself |
| GitHub Copilot | Admins | **Yes**: budgets, model-policy REST API (10-19), enforced managed settings (10-13), review billed to a cost center (10-08) | GitHub | Fleet policy on the biggest seat base |
| Datadog Agent Console (preview) | Eng leadership | **Yes**: Fix Library ships PreToolUse hooks as PRs | SaaS | Fleet-wide fixes. *Correction:* the docs list three coding agents (Claude Code, Cursor, Copilot), not five |
| Faros AI Token Engineering | VP Eng, CFO | **Yes**: quota, routing, model policy at the gateway | SaaS | Task → PR → CI → outcome per dollar; now writing about on-device classification |
| Harness AI Budgets (new, 10-20) | FinOps, platform | **Yes**: budgets per team, developer, model or project; warn at 90%, block past 100%, tiered approval for raises; weekly reset | SaaS | **Budget with approval, the team half of our boundaries**, inside an existing FinOps buyer |
| GitLab Duo Agent Platform (10-13) | Admins | **Yes**: per-user credit caps that auto-pause, alerts at 50/80/100% | GitLab | Same, for GitLab shops |
| Milestone Budget Alerts (new, 10-20) | Eng management | Alerts only | SaaS | One per-developer budget across Cursor, Claude, Copilot with cost per merged PR |
| DarkControl (new) | Security | **Yes**: allow/ask/deny policy console with audit, now records tokens per decision (10-06) | SaaS | Policy plus spend on one decision record |
| Braintrust Coding Agent Insights (new, preview 10-07) | Platform | Suggests fixes | SaaS | Team traces from CC, Codex, OpenCode |
| PointFive TokenShift (new) | FinOps | No | Local analysis, derived metadata uploaded | Our "derived only" pitch, sold to FinOps |
| Quesma (early access "soon") | Leadership, finance | No ("No proxy") | Encrypted on the laptop, stored "in your cloud" | Seat cut/upgrade advice; now lists Copilot |
| Jellyfish, Swarmia, DX, LinearB | Eng management | No | SaaS | Jellyfish "AI spend-to-work attribution" (10-07); Swarmia token use by investment (10-09), Claude Code backfill (10-14) |
| LiteLLM, Portkey, Cloudflare AI Gateway, OpenRouter | Platform teams | **Yes**: dollar caps, allow-lists; LiteLLM `agent_access_default_deny` (rc, 10-18); Portkey MCP guardrails | Self-host or SaaS | Central enforcement, now extending to agent and MCP access |
| Langfuse, CloudWatch Coding Agent Insights | Platform, cloud | No | Cloud or self-host / AWS | No change since 10-03 |
| Promptster | Hiring, then AI enablement | Coaches | Its cloud | **Changed:** homepage leads with hiring again; "the TokenOps platform" wording is gone from homepage and /teams; blog still tags posts TokenOps; `cc-audit` last released 0.9.0 (08-18) |
| Codensics | Spend governance | **Yes**: credit-pool budgets [U] | [U] | No product change found |

Helicone is in maintenance mode after Mintlify acquired it (2026-03).

## Gaps

Ranked by how much each costs TokenOps with its own audience, solo developers
first, then the team they will one day bring. Status as of 2026-10-22.

| # | Gap | Who has it | Severity | Status |
|---|---|---|---|---|
| G1 | **Harness breadth.** CodexBar at 106 providers; tokscale and agentsview at 50+ | Individual tools | High | About 90 providers read for windows (0.102); transcript readers still fewer |
| G2 | **Setup friction** | ccusage, CodexBar | High | `npx tokenops checkup` (0.101) and `init` binding plans from evidence; wizard open |
| G16 | **Vendor coach overlap on Claude.** `/usage` names waste and applies the fix; our Claude-only tips now duplicate it | Anthropic | **High, new** | Coach must defer to, or read, the vendor's own warnings |
| G17 | **Vendor approvals.** Auto mode is the default; Codex has Guardian; both emit decision telemetry | Anthropic, OpenAI | **High, new** | Boundaries pause *around* the classifier; nothing reads its verdicts yet |
| G18 | **Headroom pause as a one-liner.** Peras [U] and Pushary sell "stop before the limit" as the whole product | Peras, Pushary | Medium-high | Shipped as a boundary (0.103) but not a default or a headline |
| G19 | **New window shapes.** Banked resets (OpenAI), per-model weekly quotas (Antigravity [U]), shared pools (Cursor with Grok [U]) | Vendors | Medium | Forecasts assume one window that resets on schedule |
| G4 | Outcome linkage: cost per commit and PR | Faros, Jellyfish, Milestone, agentsview (by date) | Medium | Shipped: `spend --by commit` (0.99), `spend units` (0.105) |
| G7 | Team view with budgets and approvals | Harness, GitLab, Copilot, Anthropic | High for teams | Team plane, org boundaries and role approval shipped (0.102–0.103); needs to speak to vendor org caps |
| G8 | Ambient GUI on Linux and Windows | ai-usagebar, codex-usage-badge, Developer-Island, CodexBar Linux | Low-medium | macOS menu bar only |
| G9 | Positioning and name | Promptster (receding), Pushary, Harness | Medium (lowered) | Guardrails positioning shipped (10-13); see "stop claiming" below |
| G5 | In-session effect on harnesses without hooks | governor, Tokenade | Medium | `rules` inject exists; boundaries give Codex and Cursor advice only |
| G10–G12 | Session search, failover, benchmarks and seats | agentsview, ccflare, Jellyfish/DX | Low | Non-goals (ADR 0004, ADR 0009) |

## Plan

The 2026-10-03 plan is mostly done: gateway caps (#507), router governance
(#518, #521), cost per commit (#550, 0.105), OTel export (#551), the menu bar
and alerts, the team plane (0.102) and action boundaries (0.103) all shipped,
and the landing page now leads with guardrails. What the October field changes
is what TokenOps should build next, and what it should stop saying.

**Build, in order:**

| Step | Work | Closes | Done when |
|---|---|---|---|
| 1 | **Headroom pause as a default.** Ship a `continue` boundary at a share of the tightest window (say 90% used) in shadow on every new install, promoted to recommend after a week; one line in the README and quickstart | G18 | A fresh `init` records what the pause would have done, on every harness with a pause point |
| 2 | **Read the vendor's verdicts.** Ingest Claude Code's `safety_event` and `tool_decision` (`classifier_verdict`, `permission_mode`) and Codex approval outcomes as evidence; record them on the boundary decision instead of asking twice | G17 | `explain` shows the vendor's verdict beside ours; a tool call is never put to the person twice |
| 3 | **Coach stands down where the vendor coaches.** As with routers (ADR 0009): when Claude Code's `/usage` already raises a warning class, drop or link our tip; keep cross-harness and cross-session findings | G16 | No Claude tip duplicates a `/usage` warning; the realized-savings ledger counts only ours |
| 4 | **New window shapes.** Model banked resets, per-model quotas and shared pools in the plan catalog; forecasts treat an unused banked reset as headroom | G19 | `glance` shows a banked reset and a per-model quota correctly |
| 5 | **Org caps as vendor readings.** Read OpenAI Business per-seat limits, Copilot budgets and the Claude apps gateway cap into boundaries, so a team rule can sit under the vendor's cap, not beside it | G7 | A boundary can name the vendor's own org limit as its ceiling |
| 6 | **Transcript readers, wave 2**, in order of reach (Claude Desktop, Junie, Droid, Qwen Code), each verified against a real transcript | G1 | Coverage matrix updated |

**Step 2 and 3 audit (2026-10-24, against Claude Code 2.1.307's own
code).** Step 2, transcript route: the pause point now reads a call that
Claude Code's auto mode classifier, a permission rule, a plugin or a
PreToolUse hook stopped as Claude Code's verdict. It is recorded as
evidence and never as the person's yes. The `safety_event` and
`tool_decision` OTel events would need an OTLP receiver, and TokenOps has
none yet. Codex rollouts record no approval request or answer, and Codex
boundaries are advice only, so nothing there asks twice. Step 3: `/usage`
in 2.1.307 has these warning classes: `max_effort`, `workflow`,
`always_on_context` and `large_tool_results` (2.1.299), then `cache_miss`,
`long_context`, `subagent_heavy`, `high_parallel` and `cron` (version not
in the changelog). The resume warning came in 2.1.304. None of the
Claude-session tips (budget and window tiers, run-out, spend budgets, the
compact tip, the read-guard case) shares a class with the five named in
the changelog. The nearest pairs are the compact tip with `long_context`
and `cache_miss`, and subagent model advice with `subagent_heavy`. Ours
fire live in the session, at a context line or a specific Agent call,
while `/usage` warns after the fact when it is opened. Nothing was stood
down; revisit if `/usage` starts warning live.

**Stop claiming** (landing page and README):

- "Each vendor's own meter: 🚫 acts inside the session" and "🚫 limits you set".
  Claude Code acts on waste and on resume, and every vendor now sells admin
  limits. Reword to "one harness, its own plan".
- That nothing else stays on the laptop. Say the core needs no upload at all,
  and that team figures are derived only (ADR 0012).
- That only meters show subscription windows. Every vendor does now.
- That approvals need TokenOps. Auto mode and Guardian decide per tool call;
  TokenOps decides on money, headroom and team rules around them.

**Positioning risks:**

- **Anthropic absorbs the Claude-only solo case.** A Claude-only user now gets
  headroom, waste fixes and approvals without us. Lead with cross-harness and
  with limits that span accounts.
- **Harness and GitLab own "budget with approval" for teams** inside budgets
  FinOps already pays for. The team plane should integrate (OTel, FOCUS
  export) rather than compete as a FinOps tool.
- **"Guardrails" is crowded** (DarkControl, Pushary, Portkey MCP guardrails,
  hook repos), mostly meaning "block dangerous commands". Ours are about
  money, headroom and authority; say so in the first line.

Deliberately not planned: session search and memory (agentsview), failover
routing, benchmarks or seat management, Linux and Windows GUIs (until the
macOS menu bar has users), and matching provider counts one by one.

## Update 2026-10-06

Re-checked three days on. Sources: each product's releases, changelog or
site; **[U]** as above.

**Plan progress.** Steps 1 (#507), 5 (#518, #521), 6 (#550) and 7 (#551)
shipped; step 3 in part (`init` binds plans from vendor evidence, #520);
step 4 began (Gemini CLI #523, coding-plan windows for seven more
providers #517). G8 closed: the menu bar app ships in the cask, with
alerts as of #556. Steps 2 (landing page) and 8 (team plane) are open.

**What moved in the field.**

- **CodexBar** v0.72.0 (10-04), 90 providers. Beyond the windows it now
  *acts* in small ways: shell hooks on quota thresholds and status changes,
  Stay Awake while an agent session runs. It lists live agent sessions,
  locally and on remote hosts over SSH, serves a dashboard API, has a Linux
  Qt app, iCloud sync, widgets, reset notifications (10-02) and a quota
  burndown chart (09-30). Still no per-task or per-commit cost.
- **Promptster** is no longer only a name collision. It sells "Cut AI Coding
  Cost Per Merged PR" to teams (Claude Code, Codex, Cursor, Copilot) and a
  free solo `npx @promptster/cc-audit`: weekly cost, standing context,
  redundant reads, oversized models, then your own agent writes three fix
  plans. Our solo pitch, as one command. When it pivoted is unverified.
- **Claude Code** 2.1.281–2.1.291: `/insights` recommends auto mode,
  `/cost` names cache-miss causes, `/rate-limit-options`, and the statusline
  `rate_limits.spend_limit` gained `used_usd`, `limit_usd`, `period` (read
  since #507).
- **tokscale** v4.18.0 added Copilot CLI and Antigravity; ccusage had added
  Copilot session state in 09. Copilot CLI is the next reader by reach.
- **New menu-bar meters** (UsageBar, TouchGrassBar, TokenBar, Notch Crew
  [U], and smaller): the same 5h and weekly bars with 80/95% alerts.
- No change: Quesma (waitlist, no pricing), ccusage releases, governor,
  Datadog Agent Console, Faros.

**Gaps, re-ranked.**

| # | Gap | Severity | Status |
|---|---|---|---|
| G13 | **Alerts.** Every menu-bar rival warns at thresholds and on reset | High, small | Shipped in #556 |
| G9 | **Positioning and name.** Promptster now sells our space under our category name; the landing page still says "Nothing uploads… no telemetry" although OTel export exists (opt-in, figures only), against the 2026-09-15 rule to reword before an upload path ships | **High (raised)** | Step 2, next |
| G14 | **Zero-install first look.** `npx @promptster/cc-audit` answers in one command what `dx` and the coach answer after `init` and a daemon | Medium-high | New: a one-shot `tokenops audit` without the daemon |
| G1 | **Harness breadth.** Copilot CLI added by tokscale and ccusage | High | Step 4 continues with Copilot CLI |
| G15 | **Quota burndown chart.** CodexBar charts window use over time; the panel charts cost per day | Medium, small | The window readings are already stored |
| G7 | Team plane | High for teams | Waiting on hosting region, identity, budget |

Display defects found on the way and fixed in #555: a used-up window read
"out in" an empty duration and "runs out in 0m"; a claude.ai window sent
under an internal codename (from 2026-10-05) was shown by that name.

Still not planned: remote session lists, Stay Awake, iCloud sync, widgets, a
Linux GUI, and matching provider counts one by one.

**Order from here:** landing page and name decision, then the Copilot CLI
reader, then a one-shot `audit`, then the team plane once its three
decisions are made.

## Update 2026-10-22

What moved since 2026-10-03, with the date of each change and its source.
The tables above already carry these; this is the log.

**Vendors (primary sources).**

- **Claude Code**
  ([releases](https://github.com/anthropics/claude-code/releases)):
  - 2.1.283 (09-25): `deniedModels`.
  - 2.1.284 (09-28): sessions start in auto mode "on every plan and provider"
    unless `permissions.defaultMode` is set.
  - 2.1.285 (09-29): `allowedProviders`.
  - 2.1.290 (10-05): an approval `ceiling` on the `tool.check` hook.
  - 2.1.299 (10-14): `/usage` warns on max effort, workflows, always-on
    context and large tool results.
  - 2.1.304 (10-19): a warning before a resume takes "a noticeable share" of
    the 5-hour limit.
  - 2.1.305 (10-20): each `/usage` warning is a one-line fix, and `f` applies
    it.
  - 2.1.307 (10-21): `claude_code.safety_event` OTel for every tool call
    stopped or prompted.
- **OpenAI**:
  - A personal Codex usage dashboard for Enterprise and Edu, with per-chat
    credits and estimated cost (help article published 10-04).
  - Home Screen and Lock Screen widgets for remaining usage and resets (iOS,
    10-02).
  - Banked resets for the 5-hour and weekly windows (help article updated
    10-21). [U] The desktop app may offer one automatically at ≤10% left.
  - Business per-seat monthly credit limits (help article updated 10-19).
  - Codex CLI 0.159–0.163: windows kept in turn analytics, USD in thread
    usage, approvals bound to the turn, managed policy kept on resume.
- **GitHub Copilot**:
  - Model-policy REST API (10-19).
  - Managed settings enforced at startup (10-13).
  - User identity on telemetry, opt-in (10-12).
  - Code review billed to a cost center with a budget (10-08).
- **Google**:
  - Gemini CLI v0.64 (10-13) emits ACP `usage_update`.
  - [U] Antigravity added a separate weekly quota per model (10-13/15).
- **Cursor**:
  - Nothing on usage in its changelog.
  - [U] A weekly pool shared with Grok (10-12).

**Individual tools (GitHub, 10-22).**

- **CodexBar**:
  - Eight releases, 0.73.0 to 0.79.0 (10-07 to 10-20), and 106 providers.
  - Per-session throughput and tool errors for Codex (0.74).
  - An OpenAI live rate-limit window (0.75).
  - Provider spend budgets with display rules (0.77–0.79: Claude Enterprise,
    Vercel AI Gateway, Cursor additional spend).
  - An opt-in HTTP usage server (0.77).
  - The 10-03 note still holds: no per-task cost, no budgets you set
    yourself, no guardrails.
- **agentsview** v0.45.0 (10-09): commits and PRs for the selected dates,
  memory plugins for Claude Code and Codex, hosted sync.
- **tokscale** v4.19–4.20 (10-14 to 10-17): Claude Desktop, groups with
  members and calendar periods.
- **ccusage** v20.0.27–20.0.40: patch releases only.
- **Dormant**: Claude-Code-Usage-Monitor, governor and ccflare had no releases
  or pushes.

**Team tools.**

- **Budgets with approval and alerts**:
  - Harness AI Budgets ([10-20](https://www.harness.io/blog/introducing-ai-budgets)).
  - GitLab per-user credit caps
    ([10-13](https://about.gitlab.com/blog/make-every-ai-credit-count/)).
  - Milestone Budget Alerts
    ([10-20](https://mstone.ai/blog/introducing-budget-alerts/)).
- **Attribution and spend views**:
  - Jellyfish "AI spend-to-work attribution"
    ([10-07](https://jellyfish.co/newsroom/)).
  - Swarmia token use by investment (10-09) and Claude Code backfill (10-14)
    ([changelog](https://www.swarmia.com/changelog/)).
- **Policy and governance**:
  - DarkControl adds token use per policy decision (10-06).
  - LiteLLM v1.106.0 (10-18) disables stdio MCP servers by default;
    v1.107.0-rc adds `agent_access_default_deny`.
- **Team traces**: Braintrust Coding Agent Insights, preview (10-07).
- **Quesma**: still "Early access opens soon", with no pricing. It now lists
  Copilot and says "Encrypted on the laptop".
- **Faros**: blog posts only ("Cost per outcome", 10-21).
- **Datadog**: still in preview. Its docs list three coding agents.
- **Promptster**: its homepage leads with hiring again, and the "TokenOps
  platform" wording is gone. Its blog still tags posts TokenOps (10-06,
  10-07).

**Claims in this doc that no longer hold.**

| Claim (from) | Now |
|---|---|
| "Five positions are unoccupied" (10-03) | Two of the five are still unoccupied, two are contested, and one is contested in wording (Summary). |
| Datadog covers "five harnesses" (10-03) | Its docs list three. |
| Promptster "Markets itself as 'the TokenOps platform'" (10-03) | That wording is gone from its homepage and /teams. |
| Claude Code `/usage` is "plan bars" and reports (10-03) | It applies fixes on request and auto mode is the default. |
| Gap G4, cost per commit (10-03) | Shipped in TokenOps; others now have it by date range or org. |

## Watch list

- **Claude Code `/usage` and auto mode:** the biggest erosion of the
  Claude-only case. Each release decides what our coach should stop doing.
- **OpenAI usage dashboard:** whether per-chat cost reaches Plus and Pro, and
  whether banked resets get applied automatically.
- **CodexBar:** if its spend-budget rules start acting, or it adds per-task
  cost, it becomes the main rival. It ships every few days.
- **Pushary and Peras:** the only solo tools that stop a session on money or
  headroom. Watch their harness reach and whether they run locally.
- **Harness AI Budgets and GitLab:** budget-with-approval for teams, sold to
  buyers who already pay.
- **Datadog Agent Console and Faros:** the org-level version of our thesis;
  Faros is heading on-device.
- **Quesma:** when early access opens and what it charges.
- **Tokenade:** local input compression across 10+ agents, against fmt.
- **Promptster:** check whether the coding-cost line survives the move back
  to hiring.
