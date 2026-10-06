# Competitive landscape, gaps, and plan

Checked 2026-10-03 against each product's own site or repository. Items
marked **[U]** came from a third-party page or search snippet and are
unverified. Re-check before quoting any of it externally; this field moves
monthly.

## Summary

The field splits in two, and TokenOps sits in neither half:

- **Individual tools** (ccusage, tokscale, CodexBar, Claude-Code-Usage-Monitor,
  agentsview) read local transcripts or vendor meters and **report**. They win
  on harness breadth and zero setup.
- **Team and enterprise tools** (Anthropic's own analytics, Datadog, Faros, DX,
  Jellyfish, Swarmia, Quesma, gateways like LiteLLM, Portkey, Cloudflare)
  sell to leadership. Some **act**, but centrally: a 429 at a dollar cap, a hook
  rolled out by PR, a routing policy at the gateway.

Five positions are unoccupied, and TokenOps holds all five:

1. **Headroom on subscription plans.** Gateways see dollars through an API; a
   Claude Max or ChatGPT Pro user is invisible to them. Only CodexBar and the
   long tail of menu-bar meters show windows, and they only display them.
2. **Action inside the live session, per developer.** Datadog's fixes are
   static hooks shipped by PR; nobody else adapts budget advice, output
   compression, read-guard or coaching mid-session.
3. **Local-only, no proxy.** The most private alternatives keep data in the
   customer's S3 (Quesma) or server (LiteLLM). Nothing else stays on the
   laptop.
4. **Which account paid, through routers.** No competitor attributes a request
   to the account that billed it (FireRouter, OpenRouter, a direct
   pay-as-you-go key, or the plan) alongside the plan's own windows.
5. **Agent DX graded for the developer.** Everyone else reports to managers.
   Claude Code's `/insights` is the nearest, and it covers one harness.

The risk is not one competitor. It is that each strength is matched by
someone on one axis: CodexBar on headroom, Claude Code itself on Claude
attribution, governor on in-session compression, Datadog and Faros on
"spend to outcomes, then act". TokenOps' defensible claim is the combination,
across harnesses, on the developer's machine.

## The field

### Individual and local

| Tool | What it does | Acts? | Harnesses | Maturity | Beats TokenOps on |
|---|---|---|---|---|---|
| [ccusage](https://github.com/ccusage/ccusage) | Tokens and cost by day/session/5h block, statusline | No | ~18 | 18.9k★, v20.0.26 (2026-09-27) | Mindshare (`npx ccusage`), breadth, zero setup |
| [CodexBar](https://github.com/steipete/CodexBar) | Menu bar: session/weekly/monthly windows, credits, spend, incidents | No | 89 providers | 22.2k★, v0.71.1 (2026-10-03) | **Closest headroom rival.** Ambient GUI, provider breadth |
| [tokscale](https://github.com/junhoyeo/tokscale) | Token and cost CLI, opt-in global leaderboard | No | 50+ incl. Cursor | 5.6k★, v4.17.0 | Breadth, virality |
| [agentsview](https://github.com/kenn-io/agentsview) | Session search (full-text, semantic), cost dashboards; optional Postgres/ClickHouse sync | No | 50+ | 6.0k★, v0.44.0 | Session archive; a cheap self-hosted team view |
| [Claude-Code-Usage-Monitor](https://github.com/Maciek-roboblog/Claude-Code-Usage-Monitor) | Live TUI, burn rate, P90 limit forecast | No | Claude Code | 8.7k★, v4.0.0 (2026-06) | Pace forecasting UX |
| [governor](https://github.com/0xhimanshu/governor) | Compacts tool output, compresses memory files, scope-drift contracts; rules files for non-hook harnesses | **Yes** | CC (hooks) + rules files | 134★, v0.2.5 | Direct overlap with fmt and coaching; rules-file fallback everywhere |
| [ccflare](https://github.com/snipeship/ccflare) | Proxy that load-balances accounts, fails over on rate limits | **Yes** | Anthropic, OpenAI APIs | 1.0k★, stalled since 2026-04 | Routes around limits |
| Claude Code `/usage`, `/insights` | Plan bars; attribution to skills/subagents/MCP; cache-miss cause; friction report | Partly (auto-continue, spend prompts) | Claude Code | First party | Authoritative, built in; erodes our Claude-only story |
| Cursor and Codex dashboards | Allowance per pool, windows, credits | No | Own harness | First party | Authoritative for their own plan. [U] Cursor self-serve shows tokens only since 2026-07-31 |

### Team and enterprise

| Product | Buyer | Acts? | Data | Beats TokenOps on |
|---|---|---|---|---|
| Anthropic analytics, OTel, Analytics API | Admins | **Yes**: spend caps, alerts, per-group model entitlements | Anthropic | PR attribution via GitHub app; enforced caps. Statusline gained gateway `spend_limit` in CC 2.1.284 [U] |
| Datadog Agent Console (preview) | Eng leadership | **Yes**: Fix Library ships PreToolUse hooks as PRs | SaaS | Waste detection plus fleet-wide fixes, five harnesses |
| Faros AI Token Engineering | VP Eng, CFO | **Yes**: quota, routing, model policy at the gateway | SaaS | Task → PR → CI → outcome per dollar; closest to our thesis, org-level |
| Quesma (early access) | Leadership, finance | No ("No proxy, no rerouting") | Customer's S3; Quesma decrypts a copy unless Enterprise BYOC | Team pattern mining; buyer clarity |
| DX, Jellyfish, LinearB, Swarmia | Eng management | No (LinearB automations) | SaaS | Cost per PR, surveys, benchmarks, unused-seat reclamation |
| LiteLLM, Portkey, Cloudflare AI Gateway, OpenRouter | Platform teams | **Yes**: hard dollar caps, model allow-lists, failover; Portkey pushes MCP and skills to every developer | Self-host or SaaS | Central enforcement; fleet config |
| Langfuse | Platform teams | No | Cloud or self-host | Full traces in a shared store, nine harnesses |
| CloudWatch Coding Agent Insights | Cloud teams | No | AWS | Cost-center chargeback |
| Promptster (new) | AI enablement | Coaches, not in-session | Its cloud | **Markets itself as "the TokenOps platform"** |
| Codensics (new) | Spend governance | **Yes**: team budgets in the API path [U] | [U] | Prompt-to-commit provenance |

Helicone is in maintenance mode after Mintlify acquired it (2026-03).

## Gaps

Ranked by how much each costs TokenOps with its own audience, solo developers
first, then the team they will one day bring.

| # | Gap | Who has it | Severity | Fit with TokenOps |
|---|---|---|---|---|
| G1 | **Harness breadth.** Four harnesses plus a handful of pay-as-you-go accounts, against 18 (ccusage), 50+ (tokscale, agentsview), 89 providers (CodexBar) | Individual tools | High: a user whose harness is missing never starts | Core. Passive readers are cheap where a token log exists |
| G2 | **Setup friction.** `npx ccusage` and a menu-bar download need nothing; TokenOps needs `init`, a daemon, choices | ccusage, CodexBar | High: first-run loss | Core. The agreed setup wizard |
| G3 | **Gateway and admin caps in the statusline.** Claude Code now passes `rate_limits.spend_limit` [U]; we read the Enterprise limit from claude.ai instead | Anthropic | Medium, cheap | Core. One more vendor-reported reading |
| G4 | **Outcome linkage: cost per commit and PR** | Anthropic, Faros, DX, Datadog, Codensics | Medium-high: the question finance asks | Core. Work model and outcomes exist; git linkage is local |
| G5 | **In-session effect on harnesses without hooks.** governor ships rules files for Cursor, Codex, Gemini, Windsurf | governor | Medium | Partly there: `tokenops_rules (view=inject)` exists but is not installed by default |
| G6 | **Local per-account budgets and router policy** | LiteLLM, Portkey, Cloudflare, Faros | Medium | Core. ADR 0009 phase 2 |
| G7 | **Team view.** Rollups by team and repo, RBAC, SSO | All team products | High for the team buyer; low for solo users today | Decided: derived metrics only, hosted (2026-09-15). OTel export is a cheaper first bridge into stacks companies already run |
| G8 | **Ambient GUI.** Menu bar, tray | CodexBar, ccseva, long tail | Low-medium: the statusline covers users inside a harness | Optional. The local API makes a thin client cheap |
| G9 | **Positioning and name.** Competitors name a buyer; Promptster uses "TokenOps" | Quesma, Promptster | Medium | Landing page; name protection is a business decision |
| G10 | Session search and archive | agentsview | Low | Non-goal: ADR 0004 rules out becoming a BI product |
| G11 | Multi-account failover | ccflare, Portkey, OpenRouter | Low | Non-goal: the harness or router routes. TokenOps stands down when an external router decides (ADR 0009) |
| G12 | Benchmarks across companies, surveys, seat reclamation | Jellyfish, DX, Swarmia | Low | Non-goal |

## Plan

This extends the order agreed on 2026-10-01 (setup wizard, then router
governance) rather than replacing it. Each step names the gap it closes.

| Step | Work | Closes | Done when |
|---|---|---|---|
| 1 | **Gateway caps.** Verify the `rate_limits.spend_limit` statusline fields against a real payload; if they exist, store them as vendor readings and show them in headroom | G3 | Headroom shows the admin cap from Claude Code's own data |
| 2 | **Landing page.** Lead with the five unoccupied positions, show real headroom output, say "acts, not just reports". Decide on the name question | G9 | Page shipped; decision recorded |
| 3 | **Setup wizard** (agreed). Plans from vendor evidence, Enterprise limit, routers, preset, statusline, and rules files for harnesses without hooks | G2, G5 | A new user reaches correct headroom without editing config |
| 4 | **Harness breadth, wave 1.** Survey which harnesses keep a local token log (Copilot CLI, Gemini CLI, Amp, Goose, Qwen Code, Kimi CLI, Cline/Roo); add readers in order of reach | G1 | Each reader verified against a real transcript; coverage matrix updated |
| 5 | **Router governance** (ADR 0009 phase 2). Budget per billing account, policy on route IDs, quality by served model | G6 | A per-account budget warns before the cap, from local evidence |
| 6 | **Cost per commit and PR.** Link work to commits and branches locally; report cost and DX per merged change | G4 | `story` and `scorecard` show cost per commit |
| 7 | **OTel export of derived metrics.** dx grades, spend by account, headroom; never prompts or transcripts | G7 (first step) | Metrics land in a stock OTel collector |
| 8 | **Team plane proof.** Hosted rollup of the same derived metrics | G7 | Two machines roll up in one view |

Deliberately not planned: session search (G10), failover routing (G11), and
benchmarks or seat management (G12).

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

## Watch list

- **CodexBar:** if it adds attribution or acting, it becomes the main rival.
- **Claude Code `/usage` and `/insights`:** each release narrows what is
  distinctive on Claude alone; our edge must stay cross-harness and acting.
- **Datadog Agent Console and Faros:** the org-level version of our thesis.
- **Quesma:** when early access opens and what it charges.
- **governor:** the only other in-session optimizer.
- **Promptster:** its solo audit is the nearest thing to our solo pitch.
- **CodexBar hooks:** threshold hooks are the first step from reporting to
  acting.
