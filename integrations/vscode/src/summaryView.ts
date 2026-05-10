// Sidebar summary view. Renders a tiny tree with three headline
// metrics (cost, tokens, requests) over the last 7 days. Clicking a
// node opens the burn graph.

import * as vscode from "vscode";
import { TokenOpsClient } from "./client";

export class SummaryProvider implements vscode.TreeDataProvider<SummaryItem> {
  private _onDidChange = new vscode.EventEmitter<SummaryItem | undefined>();
  readonly onDidChangeTreeData = this._onDidChange.event;

  constructor(private client: TokenOpsClient) {}

  getTreeItem(item: SummaryItem): vscode.TreeItem {
    return item;
  }

  async getChildren(): Promise<SummaryItem[]> {
    try {
      const res = await this.client.spendSummary({ since: "7d" });
      return [
        new SummaryItem(`Spend (7d): ${res.summary.CostUSD.toFixed(4)} ${res.currency}`),
        new SummaryItem(`Tokens: ${res.summary.TotalTokens.toLocaleString()}`),
        new SummaryItem(`Requests: ${res.summary.Requests.toLocaleString()}`),
        new SummaryItem("Open burn graph", {
          command: "tokenops.showBurnGraph",
          title: "Open",
        }),
        new SummaryItem("Open optimizations", {
          command: "tokenops.showOptimizations",
          title: "Open",
        }),
      ];
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      return [new SummaryItem(`error: ${msg}`)];
    }
  }

  refresh(): void {
    this._onDidChange.fire(undefined);
  }
}

class SummaryItem extends vscode.TreeItem {
  constructor(label: string, command?: vscode.Command) {
    super(label, vscode.TreeItemCollapsibleState.None);
    if (command) this.command = command;
  }
}
