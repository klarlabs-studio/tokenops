# Competitive landscape, gaps, and plan

Checked 2026-11-02 against each product's own site, repository, changelog or
release notes (previous full check 2026-10-22, partial 2026-11-01). What moved
since 10-22 is logged under "Update 2026-11-01" and "Update 2026-11-02". Items
marked **[U]** came from a third-party page or search snippet and are
unverified. Re-check before quoting any of it externally; this field moves
weekly now, not monthly. The positioning direction of 2026-10-25 is recorded
under "Direction (2026-10-25)" below.

## Summary

The field still splits in two, and in the eleven days since 10-22 the vendors
moved more than anyone:

- **The vendors now enforce budgets inside the harness.** Codex 0.166 (10-29)
  enforces a budget per rollout by default; Claude Code's apps gateway added
  pooled group budgets (2.1.316, 10-31) beside per-user caps, and Anthropic's
  Spend Limits API puts a member's request for more usage into an admin
  approval queue; Copilot caps AI credits per automation run, day, week and
  month (10-28). Anthropic also reshaped its plans on 10-30: Max 5x is **Max**,
  Max 20x is **Ultra**, and a verified Ultra account has no 5-hour limit, only
  a weekly one at ten times Pro's and a separate weekly limit for Fable.
- **Individual tools** (ccusage, tokscale, CodexBar, agentsview, the menu-bar
  long tail) still **report**. CodexBar (110+ providers) added a daily
  allowance and reminders before unused quota lapses; agentsview 0.46 rolls
  subagent cost up to the pull request and grades whether each tool call
  helped. Neither acts, but CodexBar's hooks now feed small tools that do (ccx
  hands a session from Claude Code to Codex at 90%).
- **The acting tier grew up.** Peras 0.5 (10-30) pauses Claude Code and Codex
  by pace, burn rate, per prompt, per folder, per hour and per subagent count,
  and refuses a `codex exec` from Claude Code while Codex is paused. limitcast,
  agent-usage-guard and spare10 do parts of the same. All of them stop at
  Claude Code and Codex.
- **Team and enterprise tools** sell to leadership. New since 10-22: Opviera, a
  gateway with hard per-user caps for four agents, and Copilot's usage metrics
  API, which now reports Claude Code, Codex and OpenCode traffic that runs
  through Copilot. Quesma is still pre-launch, and its new architecture page
  says Quesma decrypts session data in its hosted tiers.
- **The token savers** (RTK, Caveman, Headroom, each 78k–117k★) are moving
  from compression into routing and cache keeping. See "Update 2026-11-01".

The five positions this doc called unoccupied on 2026-10-03, as of 11-02:

1. **Headroom on subscription plans, acted on.** *Contested, now across two
   harnesses.* Peras' cross-agent guard and ccx's hand-off act on headroom
   across Claude Code and Codex. Nobody else acts on it across Cursor,
   opencode, Copilot or Gemini as well, or across accounts and plans together.
2. **Action inside the live session, per developer.** *Contested, and the
   vendors moved in.* Codex is building a rollout budget (behind a feature
   flag still under development, off by default); Claude Code caps
   auto-continue at a usage limit at three (2.1.314). Nobody else combines the
   read guard, compression, budgets, the loop breaker and boundaries in one
   place.
3. **Local-only, no proxy.** *Holds, and the contrast sharpened.* Quesma's
   hosted tiers decrypt sessions; the Claude apps gateway stores identities
   and groups; Datadog's Trajectory collector ships traces. TokenOps' core
   needs no upload, and the team plane receives derived figures only.
4. **Which account paid, through routers.** *Still unoccupied.*
5. **Agent DX graded for the developer.** *Contested in part.* agentsview 0.46
   grades tool calls (helped, did not help) and reports repeat-call and
   recovery rates across 50+ agents, as analytics after the fact. Coaching at
   the moment of the call, across harnesses, is still ours.

The October positioning, **guardrails for coding agents** ("Know what's left.
Set what's allowed. Let your agents work."), lands in a field where each
vendor now enforces its own budgets and approvals inside its own harness.
TokenOps' defensible claim is unchanged but has to be said more precisely:
**one set of limits on plan headroom, budget and session cost, across every
harness and plan you use, set by the developer or signed down by a team,
enforced locally, and explained.** A vendor will not enforce a rule about its
rival's plan.

## The field

### Individual and local

| Tool | What it does | Acts? | Harnesses | Maturity (2026-11-02) | Beats TokenOps on |
|---|---|---|---|---|---|
| [ccusage](https://github.com/ccusage/ccusage) | Tokens and cost by day/session/5h block, statusline | No | 18 sources | 19.3k★, v20.0.47 (11-01), patch releases only | Mindshare (`npx ccusage`), zero setup |
| [CodexBar](https://github.com/steipete/CodexBar) | Menu bar: windows, credits, spend, provider spend budgets, daily allowance target (0.80+), reminders before unused weekly quota or a banked reset lapses, per-session request search, LiteLLM team budgets, threshold shell hooks that now carry the plan | Small: shell hooks, Stay Awake; others act on its output (ccx) | 110+ providers | 23.0k★, v0.83.0 (10-31), four releases since 10-22 | **Closest headroom rival.** Ambient GUI, provider breadth, release pace |
| [tokscale](https://github.com/junhoyeo/tokscale) | Token and cost CLI, leaderboard, groups with members, invitations and calendar periods | No | 50+ incl. Claude Desktop | 5.8k★, v4.21.0 (11-02) | Breadth, virality, a social team angle |
| [agentsview](https://github.com/kenn-io/agentsview) | Session search, cost dashboards, pull requests linked to sessions with subagent cost rolled up to them (0.46), tool-effectiveness grading and repeat-call and recovery rates, memory plugins, read-only MCP tools; hosted and SSH sync | No | 50+ | 6.3k★, v0.46.1 (10-23) | Session archive; cost per PR; a DX report across many agents |
| [Claude-Code-Usage-Monitor](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor) | Live TUI, burn rate, P90 limit forecast | No | Claude Code | 8.8k★, v4.0.0 (06-27), dormant since 07-05 | Pace forecasting UX |
| [governor](https://github.com/0xhimanshu/governor) | Compacts tool output, compresses memory files, scope-drift contracts | **Yes** | CC (hooks) + rules files | 131★, v0.2.5 (09-06), no pushes since | Rules-file fallback everywhere |
| [ccflare](https://github.com/snipeship/ccflare) | Proxy that load-balances accounts | **Yes** | Anthropic, OpenAI APIs | 1.05k★, stalled since 2026-04 | Routes around limits |
| [Pushary](https://pushary.com/) | Approvals routed to phone, Mac or Slack with an audit trail; "budgets" on the $19.99 tier, their effect not stated; the kill-switch page from 10-04 now returns 404 [U] | **Yes** (approvals); budgets [U] | 8 harnesses (CC, Codex, Cursor, Gemini CLI, OpenCode, Windsurf, Antigravity, Hermes) plus MCP clients | $9.99/month, Agent Pro $19.99 (5 seats); local setup, encrypted cloud sync | Phone approvals across many harnesses |
| [Peras](https://peras.vertias.eu/) | Rules that warn, ask (approvable from a phone) or pause, by pace, burn rate, share of the week per prompt, subagents per prompt, per folder, by hour, or a daily allowance with a reserve; refuses `codex exec` from Claude Code while Codex is paused, and the reverse (0.5.0); alerts before unused quota expires | **Yes** | CC, Codex | v0.5.0 (10-30); free and MIT, €4.99 Mac menu bar app; 4★, Product Hunt #18 [U] | **Nearest functional rival to boundaries for a solo user**: richer pause rules, a cross-agent guard, phone approvals |
| [Tokenade](https://tokenade.net/en/solutions/software-teams/engineering-managers) | Compresses command, MCP, search and stale-context output on each machine; claims 39% lower cost on its own benchmark | **Yes** (compression) | 22 harnesses | v2.0.3 (10-20), 27★, no release since; local, MIT/Apache | Direct overlap with fmt |
| [ccx](https://github.com/aaronwong1999/ccx) (new) | Reads `codexbar usage`; past 90% of either side's 5h window, offers to hand the newest session to the other agent with its context | Semi (one-click hand-off) | CC, Codex | 9★, v0.0.41 (10-24); linked from CodexBar's README | "Room elsewhere", acted on |
| [ccm](https://github.com/revfactory/ccm) (new) | Claude Code usage per account; switches to the account with the most headroom | **Yes** (account switch) | CC | 9★, created 10-25 | Acts on headroom across accounts |
| Hook-sized enforcers (cc-cost, agentwatchdog, terse, claudepace, coding-agent-guardrails) | Per-project budgets with warn/block hooks, budget circuit breakers, destructive-command blocks | **Yes** | Mostly CC | 0–2★ each, 09–10 | Signal, not threat: the idea is obvious |
| Menu-bar and widget long tail (ai-usagebar, TokenBar, codex-usage-badge, Developer-Island, Clawdmeter, CodeZeno's Windows monitor, OrbMet, Paceline and others) | 5h and weekly bars, per-account notch, Windows taskbar, GNOME/KDE, an ESP32 desk display | No | 1–4 vendors | ai-usagebar 739★ (10-30); TokenBar 200★ (11-02); Clawdmeter 2.3k★ v2.2.5 (11-02); CodeZeno 675★ v2.22.4 | Linux, Windows and hardware presence we lack |
| Claude Code `/usage`, `/insights`, `/cost` | Plan bars; waste warnings with one-key fixes (2.1.299, 2.1.305), large cache misses (2.1.311); resume warning on 5h share (2.1.304); auto-continue at a usage limit capped at three (2.1.314); idle compaction; gateway spend limit in dollars with warnings at 75% and 95% | **Yes** (fixes on request; auto mode default) | Claude Code | First party. No pace signal in the release notes from 2.1.257 to 2.1.316 | Authoritative, built in, *acts* on Claude waste |
| OpenAI Codex usage | Personal dashboard (Enterprise/Edu): history, top consuming chats, per-chat cost; dollar usage per thread in `/status` and the status line for ENT26 plans (0.166); widgets; banked resets; **rollout budgets behind the `rollout_budget` feature, under development and off by default; once on, enforced unless `enforce_limit = false`** (0.166, 10-29); `PreModelRequest`/`PostModelRequest` hooks that report each request's tokens (0.164, 10-23) | **Yes** (apply a saved reset; rollout budgets once the feature ships) | Codex, ChatGPT | First party | Session budget inside the harness; per-chat cost for Codex users |
| Cursor dashboard | Allowance per pool | No | Cursor | First party | Authoritative for its own plan. [U] New weekly pool shared with Grok (10-12) |

### Team and enterprise

| Product | Buyer | Acts? | Data | Beats TokenOps on |
|---|---|---|---|---|
| Anthropic analytics, OTel, managed settings, apps gateway | Admins | **Yes**: spend caps per user, IdP group or org with a 429 at the gateway, **pooled group budgets** (2.1.316), increase requests approved by an admin (Spend Limits API, Enterprise), `deniedModels`/`allowedProviders`, approval `ceiling`, `blockWritesOutsideWorkingDirectories` (2.1.314), server-managed settings applied without a prompt (2.1.316); Usage and Cost Analytics API beta (10-29). `safety_event` OTel added in 2.1.307 and dropped in 2.1.312 | Anthropic | **Budget with approval for Claude-only teams**, at the gateway; identities and groups stored |
| OpenAI Business and Enterprise | Admins | **Yes**: monthly credit limits per seat type, per-user overrides (help page updated 10-19) | OpenAI | Per-seat limits on the plan itself |
| GitHub Copilot | Admins | **Yes**: budgets, model-policy REST API (10-19), enforced managed settings (10-13), assisted permissions GA (an approval classifier, 10-26), AI-credit caps per automation run, day, week and month (10-28), Fusion model routing GA (10-28). The usage metrics API reports Claude Code, Codex, OpenCode and Zed traffic through Copilot by client (10-23) | GitHub | Fleet policy on the biggest seat base; a cross-client usage view for traffic it carries |
| Datadog Agent Console (preview) | Eng leadership | **Yes**: Fix Library ships PreToolUse hooks as PRs | SaaS | Fleet-wide fixes. Now lists Codex beside Claude Code, Cursor and Copilot, and its open-source Trajectory collector brings in 30+ other clients |
| Faros AI Token Engineering | VP Eng, CFO | **Yes**: quota, routing, model policy at the gateway | SaaS | Task → PR → CI → outcome per dollar; now writing about on-device classification |
| Harness AI Budgets (10-20) | FinOps, platform | **Yes** [U]: budgets per team, developer, model or project; warn at 90%; tiered approval for raises; weekly reset. Behind a feature flag; press disagrees on whether a hit cap blocks | SaaS | **Budget with approval, the team half of our boundaries**, inside an existing FinOps buyer |
| GitLab Duo Agent Platform (10-13) | Admins | **Yes**: per-user credit caps that auto-pause, alerts at 50/80/100% | GitLab | Same, for GitLab shops |
| Milestone Budget Alerts (new, 10-20) | Eng management | Alerts only | SaaS | One per-developer budget across Cursor, Claude, Copilot with cost per merged PR |
| DarkControl (new) | Security | **Yes**: allow/ask/deny policy console with audit, now records tokens per decision (10-06) | SaaS | Policy plus spend on one decision record |
| Braintrust Coding Agent Insights (new, preview 10-07) | Platform | Suggests fixes | SaaS | Team traces from CC, Codex, OpenCode |
| PointFive TokenShift (new) | FinOps | No | Local analysis, derived metadata uploaded | Our "derived only" pitch, sold to FinOps |
| Quesma (early access "soon") | Leadership, finance | No ("No proxy") | Open-source collector (quesma-shipper 0.0.3-90, 10-23); the `/architecture/` page lists five modes, and in the hosted Professional and Business tiers the customer's key is shared "with Quesma to decrypt"; BYOC and open-source modes keep it out | Seat cut/upgrade advice; now lists Copilot |
| Jellyfish, Swarmia, DX, LinearB | Eng management | No | SaaS | Jellyfish "AI spend-to-work attribution" (10-07); Swarmia token use by investment (10-09), Claude Code backfill (10-14) |
| LiteLLM, Portkey, Cloudflare AI Gateway, OpenRouter | Platform teams | **Yes**: dollar caps, allow-lists; LiteLLM `agent_access_default_deny` (rc, 10-18); Portkey MCP guardrails | Self-host or SaaS | Central enforcement, now extending to agent and MCP access |
| Langfuse, CloudWatch Coding Agent Insights | Platform, cloud | No | Cloud or self-host / AWS | ClickHouse acquired Langfuse (2026-01-16) [U]; MIT self-hosting reported unchanged |
| [Opviera](https://opviera.ai/blog/ai-spend-control) (new) | Platform, FinOps | **Yes**: hard daily and weekly caps per user or project in dollars, tokens or sessions, answered with 403 so agents do not retry; model policy; no approvals | Gateway, SaaS or self-hosted (Enterprise) | Hard caps across CC, Codex, opencode and Cursor at $149–$3,799/month |
| Promptster
| Codensics | Spend governance | **Yes**: credit-pool budgets [U] | [U] | No product change found |

Helicone's homepage now says it "is no longer part of Mintlify. Details to
come soon" (seen 2026-11-01); the maintenance-mode line from 10-22 no longer
holds.

## Gaps

Ranked by how much each costs TokenOps with its own audience, solo developers
first, then the team they will one day bring. Status as of 2026-11-02.

| # | Gap | Who has it | Severity | Status |
|---|---|---|---|---|
| G1 | **Harness breadth.** CodexBar at 110+ providers; tokscale and agentsview at 50+ | Individual tools | High | About 90 providers read for windows; Copilot CLI sign-in, Junie quota, opencode 2 and the current Cursor plans read (0.114); transcript readers still fewer |
| G2 | **Setup friction** | ccusage, CodexBar | High | `npx tokenops checkup` (0.101), `init` binding plans from evidence, headroom pause written by `init` (0.108); wizard open |
| G20 | **Vendor budgets inside the harness.** Codex has a rollout budget behind a feature flag (`Stage::UnderDevelopment`, `default_enabled: false` in 0.166's source); Claude's gateway enforces user, group and pooled caps; Copilot caps automations | OpenAI, Anthropic, GitHub | Low until Codex ships it (lowered 2026-11-03) | A TokenOps session budget on Codex would neither read nor defer to a rollout budget, but no Codex user has one without opting into an unfinished feature. Revisit when it leaves development. Vendor org caps are read for admins (#857, #858) |
| G18 | **Headroom pause, and the rules around it.** Peras 0.5 pauses on burn rate, per prompt, per subagent count, folder and hour, and guards across agents | Peras, limitcast, agent-usage-guard, spare10, Pushary | Medium-high | Pause on by default in shadow (0.108), loop breaker (0.113), folder and hour scope and a temporary extra allowance under presence approval (0.117). Unreleased: a cross-agent guard (#853), a pause on pace, `plan_runs_out_early_min` (#854), subagent fan-out per prompt and per session (#856). Open: phone approvals |
| G17 | **Vendor approvals.** Auto mode is the default; Codex Guardian V3; Copilot assisted permissions GA (10-26) | Anthropic, OpenAI, GitHub | High | Claude Code's verdicts are read from the transcript and never taken as the person's yes (0.108). The OTel route is moot: `safety_event` was dropped in 2.1.312 |
| G19 | **New window shapes.** Ultra with no 5-hour limit and a separate Fable weekly limit; banked resets (OpenAI); early resets; per-model quotas (Antigravity); shared pools (Cursor with Grok [U]) | Vendors | Medium-high (raised) | Max/Ultra and Ultra's week-only measure (0.116.5); early resets (0.112, 0.114); Antigravity quota groups (0.114); Codex reset credits read with their expiry, and a finding before one lapses, and before a week resets mostly unused (0.117); observed limits overlay the catalog (0.117). Unreleased: a model's own weekly window, Ultra's Fable week among them, read as its own window (#852). Open: shared pools |
| G16 | **Vendor coach overlap on Claude** | Anthropic | Medium (lowered) | Coach stands down where the vendor speaks and measures it (0.110). Claude Code still shows no pace signal, so the run-out warning stays |
| G4 | Outcome linkage: cost per commit, PR and task | Faros, Jellyfish, Milestone, agentsview (per PR with subagents, 0.46) | Medium | Shipped: `spend --by commit` (0.99), `spend units` (0.105), cost per task (0.115); subagent transcripts, `--by subagent`, and work a revert undid (0.117) |
| G7 | Team view with budgets and approvals | Anthropic (group and pooled caps, approval queue), Harness, GitLab, Copilot, Opviera | High for teams | Team plane, org boundaries and role approval (0.102–0.103), FOCUS 1.4 and schema 3 (0.115–0.116). Unreleased: `team admin caps` reads each member's Claude Enterprise spend limit and the increase-request queue (#857) and an organisation's Copilot budgets (#858) on the admin's machine. OpenAI's per-seat limits have no API. Open: answering the Claude queue from TokenOps |
| G9 | Positioning and name | Promptster (receding), Pushary, Harness; [theagentplane/tokenops](https://github.com/theagentplane/tokenops) ("run-aware token governance for multi-agent systems", 85★, v2.0.1 10-15); Finout and CloudNuro use "TokenOps" as a category word | Medium (raised) | Guardrails positioning shipped (10-13); the name is now a category word others write about |
| G8 | Ambient GUI on Linux and Windows | ai-usagebar, CodeZeno, Developer-Island, CodexBar Linux | Low-medium | macOS menu bar only |
| G5 | In-session effect on harnesses without hooks | governor, Tokenade | Medium | `rules` inject exists; boundaries are enforced in Codex through its `PreToolUse` hook (unreleased, #855), and stay advice in Cursor |
| G10–G12 | Session search, failover, benchmarks and seats | agentsview, ccflare, Jellyfish/DX | Low | Non-goals (ADR 0004, ADR 0009) |

## Plan

The 2026-10-03 plan is done: gateway caps (#507), router governance (#518,
#521), cost per commit (#550, 0.105), OTel export (#551), the menu bar and
alerts, the team plane (0.102) and action boundaries (0.103). The October
plan below is half done; its status as of 2026-11-02 is in the last column.
The research roadmap (`docs/research-roadmap-2026-11.md`) shipped in
0.117; the new steps here come after it.

**Direction priorities (2026-10-25)** sit above this table and, where they
overlap, win: see "Direction (2026-10-25)" below.

**Build, in order:**

| Step | Work | Closes | Status (2026-11-02) |
|---|---|---|---|
| 1 | **Headroom pause as a default**, in shadow on every new install | G18 | **Done** (0.108): `init` writes `headroom-pause`; findings say what it would have done |
| 2 | **Read the vendor's verdicts** instead of asking twice | G17 | **Done for Claude Code** by the transcript route (0.108). The OTel route is gone with `safety_event` (2.1.312). Codex rollouts still record no approval |
| 3 | **Coach stands down where the vendor coaches** | G16 | **Done** (0.110), with tip volume and the unseen rate in `coach stats` |
| 4 | **New window shapes** in the plan catalog | G19 | **Mostly done**: Ultra, early resets, Antigravity groups, Codex reset credits and their expiry (0.117). Open: Ultra's Fable weekly window, shared pools |
| 5 | **Org caps as vendor readings**: OpenAI per-seat limits, Copilot budgets, the Claude gateway's user, group and pooled caps, so a team rule sits under the vendor's cap | G7 | Claude Enterprise spend limits and Copilot budgets read on the admin's machine (#857, #858); OpenAI has no API; the gateway's pooled caps are not readable yet |
| 6 | **Transcript readers, wave 2** (Claude Desktop, Junie, Droid, Qwen Code) | G1 | Junie quota read (0.114); transcripts open |
| 7 (new) | **Defer to a harness's own budget.** Read Codex's rollout budget (enforced or advisory) and the Claude gateway's cap as the vendor's; a TokenOps budget on the same session says it sits under it, as the coach does with `vendor_speaks` | G20 | Deferred (2026-11-03): Codex's rollout budget is off by default and under development; revisit when it ships. The gateway's pooled caps have no reader yet |
| 8 (new) | **Read Codex's per-request usage hooks.** `PostModelRequest` reports each request's tokens; take it as evidence with its source (direction priority 5) where the rollout files are late or missing | G1, G19 | Codex spend matches the hook figure, stored as Codex's. The hooks are observation-only, not feature-gated, and shipped in 0.164 |
| 9 (new) | **A cross-agent guard.** While a boundary pauses one harness, a call from another harness into it (`codex exec` from Claude Code, `claude -p` from Codex) is held by the same boundary | G18 | **Done** (#853, unreleased): a shell call that starts another agent's CLI reads that agent's plan too |

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

**Step 3 shipped (2026-10-25).** The used-up tip stands down on Claude Code
unless another plan has room, since Claude Code's own limit message says the
rest; each stand-down is recorded as `vendor_speaks`. The run-out warning is
the pace banner's counterpart, and the banner is unverified in Claude Code, so
it stands nothing down; it interrupts once a day where the last week's
warnings were mostly unseen. `tokenops coach stats` reports tips per day and
the unseen rate by kind, which is how the cut is judged.

**Stop claiming** (landing page and README):

- "Each vendor's own meter: 🚫 acts inside the session" and "🚫 limits you set".
  Claude Code acts on waste and on resume, and every vendor now sells admin
  limits. Reword to "one harness, its own plan".
- That nothing else stays on the laptop. Say the core needs no upload at all,
  and that team figures are derived only (ADR 0012).
- That only meters show subscription windows. Every vendor does now.
- That approvals need TokenOps. Auto mode and Guardian decide per tool call;
  TokenOps decides on money, headroom and team rules around them.
- That nobody else acts on headroom across harnesses (2026-11-02). Peras and
  ccx act across Claude Code and Codex. Say "across every harness and plan you
  use", and name the ones the pause guards do not reach.
- That a session budget is ours alone. Codex is building its own rollout
  budget (off by default for now); say a TokenOps budget spans harnesses and
  plans.

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
- **The vendors enforce budgets inside their own harness now** (Codex rollout
  budgets, Claude gateway pooled caps, Copilot automation caps). Each stops
  at its own product. TokenOps' case is the rule that spans them, and it has
  to read their caps rather than stack a second one beside them (step 7).
- **Peras out-specifies the boundaries for a Claude Code and Codex user**, for
  free. Folder and hour shipped in 0.117; close the rest (burn rate, subagent
  count, cross-agent) and lead with what Peras cannot do: Cursor, opencode, Copilot,
  money across plans, a team, an explained record.

Deliberately not planned: session search and memory (agentsview), failover
routing, benchmarks or seat management, Linux and Windows GUIs (until the
macOS menu bar has users), and matching provider counts one by one.

## Direction (2026-10-25)

Decision: **TokenOps stops competing on single-vendor usage display and
competes on deciding across vendors, plans and harnesses, under the owner's
rules, acting, and verifying the outcome.**

Why now. Showing one vendor's usage is commoditised. Claude Code now shows a
weekly pace banner ("on pace to hit your weekly limit early", seen in the
Claude desktop app on 2026-10-24), `/usage` waste warnings with one-key fixes
(2.1.299, 2.1.305) and a resume warning against the 5-hour limit (2.1.304).
OpenAI ships Codex usage dashboards, widgets and banked resets (see the
2026-10-22 update). A tip that says what the vendor's own surface already says
is noise, and a percentage per window is what every meter sells.

**Priorities, in order:**

| # | Priority | Done when |
|---|---|---|
| 1 | **Stand down where the vendor already speaks.** No quota tip where Claude Code shows its pace banner; same rule as `/usage` classes (Plan, step 3). Measure tip volume and the unseen rate, so the cut is judged by data, not taste | Tip volume and unseen rate are reported per tip class; every class the vendor covers is dropped or links to the vendor's surface |
| 2 | **Act instead of inform.** Pressure-aware routing: subagents a tier down under plan pressure, the next session's default model, the headroom pause (Plan, step 1). Every move is recorded, reversible and explained | `explain` shows each move with its alternatives, the rule that allowed it and the way to undo it; each capability has its own shadow, recommend, execute stage |
| 3 | **Verify the outcome of autonomous moves.** After work moves to a cheaper model, watch rework, failure and interrupt signals, and retract the move automatically when quality drops. Outcome-based, retractable learning, as AGENTS.md requires | A downgrade that raised rework or interrupts is rolled back without the person asking, and the record shows why |
| 4 | **One daily allowance across every plan**, instead of another per-window percentage. Plans, windows and banked resets collapse into what the owner may spend today | `glance` leads with one allowance across all bound plans, and the per-window figures sit behind it |
| 5 | **Ingest vendor signals as evidence instead of recomputing them.** Claude `/usage` findings, `safety_event`, Codex's own usage numbers. Keep provenance and freshness; the vendor's number wins where it is the source | A figure the vendor reports is stored as the vendor's, with its source, and is not re-derived |

**What stays distinctly TokenOps:**

- **Rules across tools**: boundaries, budgets and presence-checked approval,
  the same in every harness. No single vendor will write rules for its rivals.
- **The team plane**: limits a team signs down to its developers, derived
  figures only (ADR 0012).
- **Cross-vendor decisions**: which plan, model or harness should take the
  next piece of work, when an operator holds two plans or more.

**Checked 2026-11-02:** no Claude Code release from 2.1.257 to 2.1.316 names
a pace or weekly-pace banner, and the status line's `rate_limits` still carries
only `five_hour` and `seven_day` use and reset times. The banner remains a
desktop-app sighting.

**Not yet verified.** That the pace banner is shown in Claude Code itself, and
not only in the Claude desktop app, is [U]: it was seen once in the desktop app
and has no changelog entry. Whether it covers every plan is [U]. Whether any
vendor exposes the pace as an API or hook that TokenOps could read (priority
5) is [U].

**Risks.** Acting raises the cost of a wrong move, so priorities 2 and 3 ship
together: no autonomous move reaches execution before its retraction path
exists. Priority 1 gives up reach on purpose; the unseen-rate measure is how we
find out whether that was right.

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

## Update 2026-11-01

From the November research review (papers, competitors and practitioner
reports); the roadmap it led to is `docs/research-roadmap-2026-11.md`.

**A tier this document did not track: token savers.** Three open-source
tools, far larger than any usage meter, grew in October from output
compressors into cost engines that also route models and keep the cache:
[RTK](https://github.com/rtk-ai/rtk) (about 85k★; compresses every tool's
output, sends large reads to a cheaper model, shared rule files),
[Caveman](https://github.com/JuliusBrussee/caveman) (about 117k★ in seven
months; 4.0 adds a local proxy, automatic model and effort picking, cache
keep-warm and a hosted cloud) and
[Headroom](https://github.com/headroomlabs-ai/headroom) (78.5k★ by the GitHub
API, 24.9k on its own site; MCP server, reversible compression, a warning
when content would break the prompt cache, a daily budget). All Apache-2.0.
They overlap fmt, routing and the prefix lint; none refuses a redundant
re-read or measures outcomes. The star counts are odd enough to re-check.

**Pause guards are now a crowd.** Peras (row updated above) and
[limitcast](https://limitcast.dev/guide/) (forecasts each window's run-out,
blocks new subagents near the limit and moves them to Sonnet, advises a
smaller plan, names the harness with room; its pricing page and homepage
disagree) lead agent-usage-guard, spare10 and agent-quota-guard. All cover
Claude Code and Codex only. A daily allowance now ships in Peras, limitcast
and CodexBar.

**CodexBar** v0.80–v0.83 (110 providers): reminders before an unused weekly
quota or banked reset expires, session search, LiteLLM team budgets. Still no
budgets that block.

**Quesma** has an installable collector,
[quesma-shipper](https://github.com/QuesmaOrg/quesma-shipper) (Apache-2.0); its
founder says it is free for individuals; company early access is still not
open.

**Claude Code** 2.1.308–2.1.316: idle compaction, `/usage` shows large cache
misses, "session limit" renamed "5-hour limit", pooled group budgets on the
gateway. **Codex**: Pro and Business limits doubled on 10-29, and those plans
now include monthly API credits; headroom there is volatile, not scarce.

**Vendor team analytics reach further, for Enterprise only.** Anthropic's
[Analytics API](https://platform.claude.com/docs/en/manage-claude/analytics-api)
returns named users with cost and seat use (Enterprise, not Team or Pro), and
the Claude Code Value view shows cost per commit; OpenAI Codex Enterprise has
a dashboard and an Analytics API; Cursor's per-member spend limits and
Analytics API are Enterprise-only [U]. Every one of them returns named people.

**A competitor class for seat right-sizing:** SaaS managers (Torii,
CloudEagle, Spendflo, Corma) reclaim and downgrade AI seats from logins or
admin APIs, with full identities; Spendflo markets an "AI seat rightsizing
agent" [U]. None reads plan-window use.

**AI FinOps:** Vantage ingests Anthropic, OpenAI and Cursor; CloudZero splits
Anthropic spend by developer; Revenium prices outcomes and joined the FinOps
Foundation [U]. FOCUS 1.5 is reported for December 2026 with AI token
tracking, and a Tokenomics Foundation was announced on 06-03 [U]; neither was
checked against its primary source.

**What still sets TokenOps apart:** plan windows across Claude Code, Codex,
Cursor and opencode; one explainable policy that moves per capability from
shadow to acting and now verifies its moves against a randomized holdout;
the read guard; the prefix lint; and a team plane that uploads derived
figures only and shows no one by name.

## Update 2026-11-02

**Since (unreleased, 2026-11-03):** a model's own weekly window, Ultra's
Fable week among them, read on its own (#852); the cross-agent guard (#853);
a pause on pace (#854); boundaries enforced in Codex (#855); subagent fan-out
rules (#856); Claude Enterprise spend limits and Copilot budgets in
`team admin caps` (#857, #858).

What moved since 10-22 that "Update 2026-11-01" does not already carry, with
dates and sources. The tables above carry these.

**Vendors (primary sources).**

- **Anthropic plans** ([pricing](https://claude.com/pricing),
  [Ultra help page](https://support.claude.com/en/articles/17728952-what-is-the-ultra-plan)):
  on 10-30 [U for the date] Max 5x became Max and Max 20x became Ultra, at
  $100 and $200. A verified Ultra account has no 5-hour limit, a weekly limit
  at ten times Pro's, and a separate weekly limit for Fable; an unverified
  one keeps a temporary 5-hour limit. TokenOps took the rename and Ultra's
  week-only measure in 0.116.5; the Fable window is not modelled yet.
- **Claude Code** ([releases](https://github.com/anthropics/claude-code/releases)):
  - 2.1.311 (10-26): `/usage` shows large prompt-cache misses; the 1M-context
    credit stop auto-compacts instead.
  - 2.1.312 (10-27): "session limit" renamed "5-hour limit"; hooks capped at
    24 concurrent per session; the `claude_code.safety_event` OTel event was
    dropped.
  - 2.1.314 (10-30): `permissions.blockWritesOutsideWorkingDirectories`;
    "continue automatically at usage limit" capped at three; blocked
    auto-mode calls explain themselves.
  - 2.1.316 (10-31): pooled group budgets on the apps gateway
    (`pooled_budget`), `managed.policy_mode: layered`, server-managed settings
    for Team and Enterprise applied without a prompt.
- **Anthropic platform**: Usage and Cost Analytics API beta (10-29); the
  [Spend Limits API](https://platform.claude.com/docs/en/manage-claude/spend-limits-api)
  (Enterprise) turns a member's "request more usage" into an increase request
  an admin approves.
- **OpenAI Codex** ([releases](https://github.com/openai/codex/releases)):
  0.164 (10-23) `PreModelRequest`/`PostModelRequest` hooks reporting tokens
  per request; 0.165 (10-27) Guardian V3 config; 0.166 (10-29)
  `features.rollout_budget.enforce_limit` (the feature is under development
  and off by default; once enabled the limit is enforced unless set false)
  and dollar thread usage for ENT26 plans. Not verified: whether the usage
  dashboard reached Plus and Pro, and whether banked resets apply on their
  own (help.openai.com refused the fetch).
- **GitHub Copilot** ([changelog](https://github.blog/changelog/)): usage
  metrics by client, including Claude Code, Codex, OpenCode and Zed (10-23);
  assisted permissions GA (10-26); Automations cost controls and Fusion
  routing GA (10-28).
- **No usage changes**: Cursor (last entry 10-20), Gemini CLI 0.66, Windsurf
  (now Devin Desktop), Kiro (credit multipliers only). Junie and Factory
  Droid: no primary source found.

**Individual tools (GitHub, 11-02).**

- **Peras** 0.5.0 (10-30): burn-rate and per-prompt lines, subagent caps,
  folder and hour scope, the cross-agent guard. Its README says it is "not a
  security boundary" and that Codex hooks fail open.
- **CodexBar** 0.80–0.83: daily allowance target, reset-expiry reminders, plan
  ID on `usage_updated` hooks, a README link to ccx.
- **agentsview** 0.46: PRs linked to sessions with subagent cost rolled up,
  tool-effectiveness grading, eight read-only MCP tools, Codex cloud import.
- **New**: ccx and ccm (above). Clawdmeter (2.3k★) and CodeZeno's Windows
  monitor (675★) were missing from the long tail.
- **Pushary**: harness list now eight, prices public; its budget behaviour and
  kill switch are unverified.
- **Unchanged**: ccusage (patches), tokscale (4.21), Tokenade, the dormant
  three.

**Team tools.**

- **Opviera** (new): gateway caps for four agents, $149–$3,799 a month.
- **Quesma**: still pre-launch; `/architecture/` says its hosted tiers decrypt.
- **Datadog**: Codex added; Trajectory collector for 30+ clients.
- **Faros**: blog only ("Time Machine" for testing model routes, 10-26).
- **No change found**: Harness, GitLab, Braintrust, PointFive, Jellyfish,
  Swarmia, DX, LinearB, Portkey, Cloudflare, Vercel. LiteLLM shipped patch
  releases not checked for governance items. DarkControl could not be found
  again [U].
- **Name**: theagentplane/tokenops (85★) and the category use by Finout and
  CloudNuro (G9).
- No funding round in this space found since mid-October.

**Claims in this doc that no longer hold.**

| Claim (from) | Now |
|---|---|
| "Still nobody else acts on headroom across harnesses" (10-22) | Peras and ccx do, across Claude Code and Codex |
| Peras pauses "at 90% of the weekly limit" [U] (10-22) | Verified, and far richer: pace, burn rate, per prompt, subagents, folder, hour, cross-agent |
| Pushary covers CC, Qwen Code, Droid, Devin CLI (10-22) | Its site lists eight others; the cost-guard page is gone |
| `safety_event` as a source of vendor verdicts (10-22, plan step 2) | Dropped in Claude Code 2.1.312 |
| Agent DX graded "still unoccupied" (10-22) | agentsview grades tool calls across 50+ agents, after the fact |

## Update 2026-10-25

Positioning direction recorded (see "Direction (2026-10-25)"): single-vendor
display is dropped as a contest, the plan's priorities now lead with standing
down where the vendor speaks, acting, and verifying outcomes. One new vendor
item: Claude's weekly pace banner, seen in the Claude desktop app on
2026-10-24 and not yet checked in Claude Code or its changelog [U].

## Watch list

- **Vendor budgets inside the harness:** Codex rollout budgets (when does
  `rollout_budget` leave `UnderDevelopment`, and does the ChatGPT admin push
  it centrally?), the Claude apps gateway, Copilot
  Automations. Each release decides what our budgets defer to.
- **Claude Code `/usage` and the plans:** Ultra's weekly-only shape spreading
  to other tiers; a pace signal arriving in Claude Code itself.
- **Peras and limitcast:** the pause guards; watch for Cursor, opencode or a
  team tier. Peras is the feature benchmark for boundaries.
- **CodexBar as a platform:** its hooks and HTTP server feed tools that act
  (ccx). If it adds blocking budgets or per-task cost, it becomes the main
  rival. It ships every few days.
- **agentsview:** per-PR cost and tool grading; if it adds live nudges, it
  meets the coach.
- **Copilot's usage metrics by client:** if it grows past Copilot-routed
  traffic, it is a cross-harness team view for free.
- **Harness, GitLab and Opviera:** budget-with-approval and hard caps for
  teams, sold to buyers who already pay.
- **Datadog Agent Console and Faros:** the org-level version of our thesis.
- **Quesma:** when early access opens and what it charges.
- **RTK, Caveman, Headroom, Tokenade:** the token savers adding routing and
  cache control; if one adds outcome checks or plan windows, it is a direct
  rival.
- **The name:** theagentplane/tokenops and "TokenOps" as a FinOps category
  word.
