# TokenOps for VS Code

Surface the local TokenOps daemon's burn graph, optimization
suggestions, and replay flow inside VS Code.

## Install (dev)

```bash
cd integrations/vscode
npm install
npm run compile
```

Open this folder in VS Code and press **F5** to launch a development
host. The "TokenOps" sidebar panel and the three commands appear:

- `TokenOps: Show burn graph`
- `TokenOps: Show optimization suggestions`
- `TokenOps: Open replay for workflow…`

## Configure

| Setting                | Default                       | Purpose                              |
|------------------------|-------------------------------|--------------------------------------|
| `tokenops.daemonUrl`   | `""`                          | Daemon URL; empty uses the running daemon's `~/.tokenops/daemon.url`. |
| `tokenops.bearerToken` | `""`                          | API token; empty uses the running daemon's. |

## Build

```bash
npm run build      # tsc → out/
```

The extension is single-source TypeScript with no bundler — `out/`
contains plain CommonJS that VS Code loads directly.

## What it surfaces

- **Sidebar summary**: 7-day spend / tokens / request count, refreshed
  every 60s.
- **Burn graph webview**: SVG line chart of hourly cost over the last
  24 hours.
- **Optimizations webview**: table of the last 7 days of
  `OptimizationEvent`s with savings + reasons.
