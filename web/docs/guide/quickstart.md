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
`/api/spend/*`, mounts `/dashboard`, and writes its listen URL to
`~/.tokenops/daemon.url` so the MCP server can hand it to your agent.

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

```text
http://127.0.0.1:7878/dashboard
```

Vue + D3, auto-refresh every 15s. Cost-over-time line, tokens-per-bucket
stacked bar, KPI tiles. Same data the MCP tools and the CLI use — one
local source of truth.

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
tokenops demo                       # seed 7d synthetic events
```

## Next steps

- [SDK shim reference](/integrations/sdk-overview) — Python + Node + curl
- [CLI reference](/guide/cli)
- [Architecture](/architecture/overview) — how the pieces fit
