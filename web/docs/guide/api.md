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

- `/api/story` returns each task's shape (turns, files, frictions,
  timing) with its title withheld, and says `titles_withheld: true`.
- `/api/coach/prompts` returns counts and recommendations, with every
  quoted instruction left out.

The MCP tools keep the words, because the agent reading them is the
operator's own.

## Reading

| Route | Answers |
|---|---|
| `GET /api/glance` | Session budgets, plan headroom and one ranked insight |
| `GET /api/findings` | What the coach and the session analysis observed, ranked, with what to do |
| `GET /api/plans/headroom` | Each plan: usage, the vendor's windows, spend against the limit, risk |
| `GET /api/plans/session-budget` | Each plan window: share used, pace, what to do |
| `GET /api/status` | Readiness, blockers, warnings, next actions |
| `GET /api/mode` | Operating mode and what each subsystem may do |
| `GET /api/coach` | The coach's dials and each power's autonomy |
| `GET /api/config` | The active configuration, secrets redacted |
| `GET /api/data-sources` | Events per source and each reader's health |
| `GET /api/vendor-usage` | Which usage sources are on and producing |
| `GET /api/sources` | Each reader's ingestion health |
| `POST /api/sources/refresh` | Ask the usage readers to poll now, at most every 30 seconds: `202` with how many were asked, or `429` with `Retry-After` |
| `GET /api/dx` | Agent DX, graded, with the single change worth making |
| `GET /api/story` | Recent work, task by task, titles withheld |
| `GET /api/coach/prompts` | Prompt scoring, quotes withheld |
| `GET /api/spend/summary` | Spend and tokens over a window |
| `GET /api/spend/series` | Spend and tokens over time |
| `GET /api/spend/forecast` | Daily spend projected forward |
| `GET /api/spend/cache_stats` | Prompt cache hits and savings |
| `GET /api/spend/top` | Top consumers, ranked on the API equivalent |
| `GET /api/spend/commits` | What each commit cost: the agent work that led to it, priced; subjects withheld |
| `GET /api/spend/burn-rate` | Usage over the last hours |
| `GET /api/scorecard` | The wedge KPI scorecard |
| `GET /api/pricing` | The rate card TokenOps costs with |
| `GET /api/workflows` | Recorded workflows |
| `GET /api/workflows/{id}` | One workflow's trace and its waste |
| `GET /api/optimizations` | Optimization recommendations |
| `GET /api/routing/proposals` | Model upgrades waiting on you |
| `GET /api/decisions/{id}` | Why a decision was made |
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
| `POST /api/budgets` | Creates, updates or deletes a budget |
| `POST /api/routing/rules` | Creates, updates or deletes a routing rule |
| `POST /api/plans` | Binds a provider to a plan, or clears it |
| `POST /api/preferred-models` | Sets or clears a provider's preferred model |
| `POST /api/routing/decisions` | Answers a routing proposal |
| `POST /api/outcomes` | Records your judgement of an execution |
| `POST /api/coach` | Applies a coach preset, or changes its dials |

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
  -d '{"provider":"anthropic","plan":"claude-max-20x"}' http://127.0.0.1:7878/api/plans
```

Connecting a vendor's login (`tokenops vendor-usage setup`) is not on
the API. It goes through your browser and the OS's consent prompt, which
belongs in a terminal or an agent conversation, not a background daemon.
