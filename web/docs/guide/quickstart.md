# Quickstart

Ninety seconds from zero to "my agent knows my rate-limit window."

## 1. Install

```bash
brew install felixgeelhaar/tap/tokenops
```

Or via Go:

```bash
go install github.com/felixgeelhaar/tokenops/cmd/tokenops@latest
```

Or grab a prebuilt binary from
[GitHub Releases](https://github.com/felixgeelhaar/tokenops/releases).

## 2. Initialise with auto-detection

```bash
tokenops init --detect
```

`--detect` sniffs Claude Code, Claude Desktop, Cursor, ChatGPT Desktop,
and `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GEMINI_API_KEY`. The
output ends with a paste-ready list of `tokenops plan set …` commands
for everything it found. Pick the ones that fit your plan tiers:

```bash
tokenops plan set anthropic claude-max-20x
tokenops plan set openai gpt-plus
```

## 3. Start the daemon

```bash
tokenops start
```

The daemon binds `127.0.0.1:7878`, opens the SQLite store, mounts
`/api/spend/*` + `/dashboard` (both behind a shared-secret token),
publishes itself as `tokenops.local` over mDNS, and writes its
listen URL + dashboard token to `~/.tokenops/daemon.url` so the MCP
server can hand both to your agent.

## 4. Wire the MCP server into your agent

```json
{
  "mcpServers": {
    "tokenops": {
      "command": "tokenops",
      "args": ["serve"]
    }
  }
}
```

Then ask your agent for any of these:

```text
tokenops_session_budget        # headroom gauge + recommended action
tokenops_burn_rate             # 24h sparkline + cost total
tokenops_dashboard             # clickable URL to the local dashboard
tokenops_plan_headroom         # month-to-date headroom per plan
```

## 5. Open the dashboard

Ask your agent for `tokenops_dashboard` — the response carries a
clickable URL with the one-shot auth token pre-attached:

```text
http://tokenops.local:7878/dashboard?token=<secret>
```

First click sets a 24h session cookie and 303s to a clean URL, so
the token never sticks in browser history. Subsequent refreshes
work cookie-only.

Vue + D3, auto-refresh every 15s. Cost-over-time line,
tokens-per-bucket stacked bar, KPI tiles. Same data the MCP tools
and the CLI use — one local source of truth.

If `.local` resolution isn't available on your machine, the same
URL on `http://127.0.0.1:7878` works (the MCP tool surfaces both).

## (Optional) Upgrade signal quality

Default install reports **low** confidence (MCP pings only). Two
zero-network-config upgrades:

```yaml
# ~/.config/tokenops/config.yaml
vendor_usage:
  claude_code:
    enabled: true              # reads ~/.claude/stats-cache.json
    interval: 60s
  anthropic:
    enabled: true              # calls Anthropic Admin API
    admin_key: sk-ant-admin-…  # mint in claude.com console
    interval: 5m
```

Claude Code stats cache promotes Anthropic confidence to **medium**
(daily granularity, undocumented schema — caveat in every response).
Anthropic Admin API promotes it to **high** for metered API usage
(Claude Max plan window state is not exposed by any documented
endpoint and stays heuristic).

## (Optional) Route SDK calls through the local proxy

Higher-quality signal: TokenOps observes every request instead of just
MCP tool invocations.

```bash
export OPENAI_BASE_URL="http://127.0.0.1:7878/openai/v1"
export ANTHROPIC_BASE_URL="http://127.0.0.1:7878/anthropic"
```

Gemini: pass `httpOptions.baseUrl = "http://127.0.0.1:7878/gemini"`
to `genai.Client()`. For richer attribution, set workflow / agent IDs
as headers:

```http
X-Tokenops-Workflow-Id: research-summariser
X-Tokenops-Agent-Id: planner
```

## CLI quick reference

```bash
tokenops spend --forecast           # spend + 24h burn + 7d forecast
tokenops scorecard                  # operator wedge KPIs
tokenops plan list                  # configured plans + headroom
tokenops vendor-usage status        # show poller state + event counts
tokenops vendor-usage backfill --hours 168   # one-shot Anthropic Admin pull
tokenops dashboard rotate-token     # mint + persist a fresh dashboard secret
tokenops demo                       # seed 7d synthetic events
```

See the [CLI reference](/guide/cli) for the full surface.

## Next steps

- [SDK shim reference](/integrations/sdk-overview) — Python + Node + curl
- [CLI reference](/guide/cli)
- [Architecture](/architecture/overview) — how the pieces fit
