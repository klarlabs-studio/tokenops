# MCP server

`tokenops serve` starts a Model Context Protocol server that exposes
TokenOps queries as tools. Register it in any MCP client (Claude
Desktop, Cursor, opencode, your own agent) to let an LLM ask "how much
did we spend last week?" or "show me the top wasteful workflows" —
answered from the local event store.

## Tools

Sixteen tools, each answering one question. Most pick their slice with a
`view` (or an `action` or `setting` where they change something), so an
agent chooses between sixteen clear options instead of fifty near
neighbours, and reads far fewer tokens of tool definitions before its
first call. Every tool has a title, annotations a client can base approval
on (`readOnlyHint`, `destructiveHint`, `idempotentHint`), described
parameters, and an output schema; a parameter that does not apply to the
chosen view is refused with a message saying which ones do.

| Tool | Answers | Views, actions or settings |
|---|---|---|
| `tokenops_glance` | Every plan's windows with pace, spend against limits, the session budget, the coach's findings | `all`, `headroom`, `session_budget`, `findings` |
| `tokenops_spend` | What was spent and where | `summary`, `top`, `burn`, `forecast` |
| `tokenops_sessions` | How agent sessions go | `dx`, `story`, `prompts` |
| `tokenops_status` | Whether TokenOps is working and how it is set up | `status`, `mode`, `config`, `data_sources`, `vendor_usage`, `version` |
| `tokenops_explain` | What a figure means, or why a decision was made | `term` or `decision_id` |
| `tokenops_records` | What TokenOps recorded | `optimizations`, `audit`, `events`, `workflow`, `scorecard`, `verify` |
| `tokenops_rules` | The repository's agent rules files | `analyze`, `conflicts`, `compress`, `inject` |
| `tokenops_fmt` | Command-output compression | `learn`, `analyze` |
| `tokenops_pricing` | Model prices from the rate card | — |
| `tokenops_prepare_work` | Headroom and a model recommendation before a task | — |
| `tokenops_review_work` | One workflow reviewed after a task | — |
| `tokenops_outcome` | How a piece of work turned out | `record`, `detect` |
| `tokenops_routing` | Which model a turn should run on, and routing governance | `advise`, `proposals`, `decide`, `set_rule` |
| `tokenops_configure` | Change a setting | `plan`, `budget`, `mode`, `preferred_model`, `usage_meter` |
| `tokenops_coach` | How much the coach says and does | — |
| `tokenops_experiment` | A bounded routing trial | `start`, `status`, `stop` (as `action`) |

The read tools are marked read-only; `tokenops_prepare_work` and
`tokenops_outcome` record evidence without changing settings;
`tokenops_routing`, `tokenops_configure`, `tokenops_coach` and
`tokenops_experiment` change settings and are marked so a client asks
first. The developer tools (`eval`, `replay`, `coverage-debt`, `rules
bench`) are CLI commands only.

## Claude Desktop setup

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "tokenops": {
      "command": "tokenops",
      "args": ["serve"],
      "env": {
        "TOKENOPS_STORAGE_PATH": "/Users/<you>/.tokenops/events.db"
      }
    }
  }
}
```

Restart Claude Desktop. The TokenOps tools surface in the tools list.

## Cursor / other clients

Any client speaking MCP over stdio works. Run `tokenops serve` as the
command; pass `TOKENOPS_STORAGE_PATH` to point at the events DB.

## Logs

Diagnostic output goes to stderr (stdout is reserved for the JSON-RPC
channel). Tail it via `tokenops serve 2>/tmp/tokenops-serve.log`.
