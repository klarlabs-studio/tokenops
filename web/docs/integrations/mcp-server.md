# MCP server

`tokenops-mcp` is a Model Context Protocol server that exposes
TokenOps queries as tools. Register it in any MCP client (Claude
Desktop, Cursor, your own agent) to let an LLM ask "how much did we
spend last week?" or "show me the top wasteful workflows" — answered
from the local event store.

## Tools

| Tool                       | Purpose                                                                |
|----------------------------|------------------------------------------------------------------------|
| `tokenops_spend_summary`   | Total requests / tokens / cost over a time window                      |
| `tokenops_top_consumers`   | Top N spenders grouped by model / provider / workflow / agent          |
| `tokenops_burn_rate`       | Spend over the last N hours (default 24)                               |
| `tokenops_forecast`        | Holt-forecasted daily spend for the next horizon_days                  |
| `tokenops_workflow_trace`  | Reconstructed workflow trace + waste-detector findings                 |

## Claude Desktop setup

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "tokenops": {
      "command": "tokenops-mcp",
      "env": {
        "TOKENOPS_STORAGE_PATH": "/Users/<you>/.tokenops/events.db"
      }
    }
  }
}
```

Restart Claude Desktop. The TokenOps tools surface in the tools list.

## Cursor / other clients

Any client speaking MCP over stdio works. Run `tokenops-mcp` as the
command; pass `TOKENOPS_STORAGE_PATH` to point at the events DB.

## Logs

Diagnostic output goes to stderr (stdout is reserved for the JSON-RPC
channel). Tail it via `tokenops-mcp 2>/tmp/tokenops-mcp.log`.
