// TokenOps VS Code extension. Exposes three commands + a sidebar view
// summarising daemon health and recent spend. The dashboard's HTML is
// rendered inside a webview to reuse the existing chart components
// without duplicating them as VS Code-native UI.

import * as vscode from "vscode";
import { TokenOpsClient } from "./client";
import { renderBurnGraphHtml, renderOptimizationsHtml } from "./webviews";
import { SummaryProvider } from "./summaryView";

export function activate(context: vscode.ExtensionContext): void {
  const client = new TokenOpsClient();

  const summary = new SummaryProvider(client);
  context.subscriptions.push(
    vscode.window.registerTreeDataProvider("tokenops.summary", summary),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("tokenops.showBurnGraph", async () => {
      const panel = vscode.window.createWebviewPanel(
        "tokenopsBurnGraph",
        "TokenOps · Burn graph",
        vscode.ViewColumn.Active,
        { enableScripts: true },
      );
      try {
        const series = await client.spendSeries({ since: "24h", bucket: "hour" });
        panel.webview.html = renderBurnGraphHtml(series);
      } catch (err) {
        panel.webview.html = errorHtml(err);
      }
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("tokenops.showOptimizations", async () => {
      const panel = vscode.window.createWebviewPanel(
        "tokenopsOptimizations",
        "TokenOps · Optimizations",
        vscode.ViewColumn.Active,
        { enableScripts: true },
      );
      try {
        const opts = await client.optimizations({ since: "7d" });
        panel.webview.html = renderOptimizationsHtml(opts);
      } catch (err) {
        panel.webview.html = errorHtml(err);
      }
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("tokenops.openReplay", async () => {
      const id = await vscode.window.showInputBox({
        prompt: "Workflow ID to replay",
        placeHolder: "e.g. research-summariser",
      });
      if (!id) return;
      // The CLI is the canonical replay surface. Open an integrated
      // terminal and run it there so output streams in front of the
      // user without bouncing through a webview.
      const terminal = vscode.window.createTerminal({ name: `TokenOps replay ${id}` });
      terminal.sendText(`tokenops replay --workflow ${shellEscape(id)} --json | less`);
      terminal.show();
    }),
  );

  // Refresh the sidebar summary every 60s so the user does not have to
  // poke it manually after running a few prompts.
  const interval = setInterval(() => summary.refresh(), 60_000);
  context.subscriptions.push({ dispose: () => clearInterval(interval) });
}

export function deactivate(): void {
  // No persistent state to clean up; subscriptions are auto-disposed.
}

function errorHtml(err: unknown): string {
  const msg = err instanceof Error ? err.message : String(err);
  return `<!DOCTYPE html><html><body style="font-family:sans-serif;padding:24px;color:#f85149">
    <h2>Couldn't reach the TokenOps daemon.</h2>
    <pre style="white-space:pre-wrap">${escapeHtml(msg)}</pre>
    <p>Check that <code>tokenops start</code> is running and that
    <code>tokenops.daemonUrl</code> in settings points at it.</p>
  </body></html>`;
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c] ?? c),
  );
}

function shellEscape(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`;
}
