# Daemon API

The daemon serves everything a surface other than an agent needs: a
menu bar, a script, a dashboard you build yourself. It answers from the
same code as the MCP tools and the CLI (ADR 0010), so all three give the
same figures.

The full contract is the generated OpenAPI 3.1 document,
[`docs/api/openapi.json`](https://github.com/klarlabs-studio/tokenops/blob/main/docs/api/openapi.json).
This page is the overview.

## Calling it

The daemon listens on `127.0.0.1:7878`. Every `/api/*` route needs the
bearer token the daemon writes to `~/.tokenops/daemon.url` (mode `0600`):

```bash
TOKEN=$(python3 -c "import json,os;print(json.load(open(os.path.expanduser('~/.tokenops/daemon.url')))['dashboard_token'])")
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7878/api/glance
```

`/healthz`, `/readyz` and `/version` need no token, so a supervisor can
probe them.

The daemon answers only requests addressed to this machine: a loopback
`Host` (`127.0.0.0/8`, `::1`, `localhost`), the listen address, or a name
it was told about (`tls.hostnames`, the advertised `tokenops.local`,
[`allowed_hosts`](./configuration.md#network-exposure)). Browser requests
from another site — an `Origin` that is not local, or
`Sec-Fetch-Site: cross-site` — are refused with `403`. CLI tools and SDKs
send neither header and are unaffected; the probes answer whatever the
`Host`.

## What it never serves

Derived figures only: windows, spend, grades, counts, tips. Never
prompt text, file contents or transcripts. Where a question is built on
the operator's own words, the API answers without them:

- `/api/story` returns each task's shape (turns, tool calls, timing, how
  many files it edited, and each friction by kind: `interrupted`,
  `rejected`, `rework`, `delegated`) with its title and file paths
  withheld, and says `titles_withheld: true` and `paths_withheld: true`.
- `/api/story/runtime` identifies tasks by hashed keys and carries no
  instruction text, file paths or session ids.
- `/api/coach/prompts` returns counts and recommendations, with every
  quoted instruction left out.

The MCP tool `tokenops_sessions` answers the same way by default. Only an
explicit `quote: true` on its `story` or `prompts` view returns the
operator's instruction text and local file paths.

## Reading

| Route | Answers |
|---|---|
| `GET /api/glance` | Session budgets, plan headroom and one ranked insight; `boundaries` lists the [boundaries](https://team.klarlabs.de/guide/boundaries) that hold now, as advice; `plan_use` gives each provider's plan utilization and coverage over the last 7 days; `early_resets` lists windows that reset early within the last day; a window with `not_started: true` has no `resets_at` yet; `prompt_cache` is the countdown to the current Claude Code session's prompt cache going cold (`last_request_at`, `ttl_seconds`, `expires_at`, `remaining_seconds`, and `state`: `warm`, `expiring` with under two minutes left, or `cold`), absent when no session's cache went cold within the last hour |
| `GET /api/findings` | What the coach and the session analysis observed, ranked, with what to do. Each finding has a stable `key` (its kind and subject, such as `quota:openai:week`); a quota finding also names its `provider` and `window` as the plan headroom names them, so it can be shown beside that window. Where an operation exists for it, it has `actions` to run with `POST /api/findings/act`. `hidden` counts the findings you dismissed that are still hidden |
| `GET /api/plans/headroom` | Each plan: usage, the vendor's windows, spend against the limit, risk; `allowances` is what the vendor reports banked that expires on its own (Codex's reset credits), with each one's `expires_at` |
| `GET /api/plans/session-budget` | Each plan window: share used, pace, what to do |
| `GET /api/plans/advice` | Plan right-sizing over `days` (30 by default, up to 90): each plan's windows replayed against its other tiers, metered overflow and the API at list price. Advice only. `observed` lists each limit taken from this machine's overlay of observed limits instead of the catalog, with `catalog_factor` and `observed_factor` side by side |
| `GET /api/status` | Readiness, blockers, warnings, next actions |
| `GET /api/mode` | Operating mode and what each subsystem may do |
| `GET /api/coach` | The coach's dials and each power's autonomy; `activity` is what it did on its own `today` (since local midnight) and over the last `7d`: subagents moved to a cheaper model, new sessions started on one, re-reads the read guard refused, with a one-line `summary` that is empty when it did nothing; `presets` names each preset with what it does; `prompt_cache` is the current Claude Code session's cache countdown, as on the glance |
| `GET /api/config` | The active configuration, secrets redacted |
| `GET /api/data-sources` | Events per source and each reader's health |
| `GET /api/vendor-usage` | Which usage sources are on and producing |
| `GET /api/sources` | Each reader's ingestion health |
| `POST /api/sources/refresh` | Ask the usage readers to poll now, at most every 30 seconds: `202` with how many were asked, or `429` with `Retry-After` |
| `GET /api/dx` | Agent DX, graded, with the single change worth making; `source`, `model` and `role` (`primary`, `auto_review`, `subagent`) choose the instructions |
| `GET /api/story` | Recent work, task by task, titles withheld; takes the same `source`, `model` and `role` |
| `GET /api/story/runtime` | Where tasks' time went as far as the transcripts record it: status, model request, tool, approval wait, idle and unattributed time with origin and coverage, and nearest-rank median and p90 per harness, model and role. Unknown is `null`, never `0`. Same filters, plus `limit` |
| `GET /api/coach/prompts` | Prompt scoring, quotes withheld |
| `GET /api/coach/lint-context` | What in every session's context changes between sessions and breaks the prompt cache (see [`tokenops coach lint-context`](./cli.md#tokenops-coach-lint-context)): the user's CLAUDE.md, AGENTS.md and hooks, the project named by `root`, and the hook output recent transcripts recorded. Each finding carries the matched token only |
| `GET /api/spend/summary` | Spend and tokens over a window |
| `GET /api/spend/series` | Spend and tokens over time |
| `GET /api/spend/forecast` | Daily spend projected forward |
| `GET /api/spend/cache_stats` | Prompt cache hits and savings |
| `GET /api/spend/top` | Top consumers, ranked on the API equivalent |
| `GET /api/spend/commits` | What each commit cost: the agent work that led to it, priced; subjects withheld |
| `GET /api/spend/claude-code` | The organisation's Claude Code use as Anthropic reports it, per UTC day (`?days=`, 1 to 90; 7 when omitted), from every configured key: a Claude Console admin key (with Anthropic's estimated cost) and a Claude Enterprise Analytics API key (claude.ai sign-ins, about a day behind; unpublished days under `pending`). Summed over users, no names. 409 when neither key is configured |
| `GET /api/spend/units` | Cost per commit, merged pull request, recorded and achieved outcome and change class against the 4 weeks before (`?since=`; the last 7 days when omitted), with plan-window shares on a flat-rate plan and the work no unit took |
| `GET /api/spend/waste` | Avoidable usage by waste class, each token in one class, in API-equivalent dollars and plan-window share (`?since=`; the last 7 days when omitted), what was realized beside it, and the guardrail to switch on next |
| `GET /api/spend/burn-rate` | Usage over the last hours |
| `GET /api/spend/tags` | Spend per project, client or cost centre, tagged on the daemon by your allocation rules (`?by=project\|client\|cost_center&since=&until=`; project over the last 30 days when omitted), with the untagged work kept apart |
| `GET /api/spend/export` | Spend and usage as FOCUS 1.4 CSV for FinOps tools (`?since=&until=`; the month so far when omitted; `?version=1.2` for FOCUS 1.2) |
| `GET /api/scorecard` | The wedge KPI scorecard |
| `GET /api/pricing` | The rate card TokenOps costs with |
| `GET /api/workflows` | Recorded workflows |
| `GET /api/workflows/{id}` | One workflow's trace and its waste |
| `GET /api/optimizations` | Optimization recommendations |
| `GET /api/routing/proposals` | Model upgrades waiting on you |
| `GET /api/decisions/{id}` | Why a decision was made |
| `GET /api/boundaries` | Each [action boundary](https://team.klarlabs.de/guide/boundaries), how often it matched, where it is enforced, and what waits on you; `when` lists a rule's conditions, with `holds_now` on each `hours` window; `extras` are the extra allowances in force (`tokenops boundaries extra`): `rules` lifted until `until`, with `granter` and `why` |
| `GET /api/rules/analyze` | Rule files analysed |
| `GET /api/rules/compress` | Rule files compressed |
| `GET /api/rules/conflicts` | Conflicts between rules |
| `GET /api/rules/inject` | Rules to inject for a task |
| `GET /api/audit` | The audit log, newest first |
| `GET /api/domain-events` | Domain-event counts |

A plan that is not set up answers `{"error": "plans_unconfigured",
"hint": …}`, the same as the MCP tools, so a surface can show the hint
instead of an empty screen.

## Changing

| Route | Does |
|---|---|
| `POST /api/mode` | Sets the operating mode |
| `POST /api/budgets` | Creates, updates or deletes a budget; `provider` and `tag` scope it, as on the CLI |
| `POST /api/routing/rules` | Creates, updates or deletes a routing rule |
| `POST /api/plans` | Binds a provider to a plan, or clears it |
| `POST /api/preferred-models` | Sets or clears a provider's preferred model |
| `POST /api/routing/decisions` | Answers a routing proposal; with `boundary_approval: presence` an approval needs `presence_proof`, as below |
| `POST /api/boundaries/decisions` | Denies what a boundary asked about (`{"id", "decision": "deny", "session"}`). It approves only with `presence_proof`, the signature `tokenops approve` makes with this Mac's [presence key](https://team.klarlabs.de/guide/boundaries#presence-checked-approval) (the statement's fields, and `sig` in base64); without one an approval comes from `tokenops approve` at a terminal or Claude Code's prompt |
| `POST /api/boundaries/statements` | The statement that approves a waiting boundary decision (`{"id", "session"}`), for a surface that signs it with the presence helper: `proof` (the statement's fields, unsigned; post it back as `presence_proof` with `sig` added), `statement` (the bytes to sign, base64), `reason` (the dialog's text, naming the rule, action and session) and `blob` (the key the helper signs with). `409` with `"code": "presence_unavailable"` when no key can sign here (none enrolled, a held key, no key blob), so the surface falls back to the terminal. The menu bar's inline *Approve* uses it |
| `POST /api/routing/statements` | The same for a routing upgrade (`{"key"}`) |
| `POST /api/outcomes` | Records your judgement of an execution |
| `POST /api/coach` | Applies a coach preset, or changes its dials |
| `POST /api/findings/dismiss` | Hides a finding (`{"key", "for": "day"}` or `"week"`) until the time passes or it becomes more urgent than when you hid it: a notice you hid shows again as a warning. `{"for": "none"}` shows every hidden finding again, or one with its `key`. Answers with the findings after the change |
| `POST /api/findings/act` | Runs one of the `actions` a finding offers now (`{"key", "action"}`), through the same code as the CLI: `boundary.promote` (`tokenops boundaries promote`) and `tip.ack`, `tip.expected` or `tip.snooze` (`tokenops coach tip`). An action's `confirm` says what it changes, for the surface to show first. `409` when the finding is gone or no longer offers the action; `400` for an action that is not on the list. A promote takes effect on the next tool call, with no restart. Demoting stays on the CLI, so nothing on the API loosens a boundary |

The write routes are guarded more tightly than the reads:

- They exist only behind the token.
- They take only an `application/json` body. A browser cannot send that
  to another origin without a preflight the daemon never answers, so a
  web page cannot change your setup.
- An unknown field is refused, not ignored, so a typo cannot pass as
  accepted.
- Every accepted change is written to the audit log as `config_change`
  with actor `api` (`GET /api/audit?actor=api`).
- A change to config answers first, then restarts a supervised daemon so
  it takes effect. Unsupervised, the answer says how to restart it.

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"provider":"anthropic","plan":"claude-ultra"}' http://127.0.0.1:7878/api/plans
```

Connecting a vendor's login (`tokenops vendor-usage setup`) is not on
the API. It goes through your browser and the OS's consent prompt, which
belongs in a terminal or an agent conversation, not a background daemon.
