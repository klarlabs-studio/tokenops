// Minimal HTTP client for the TokenOps daemon. Mirrors the dashboard
// surface but lives in CommonJS so the extension does not need a
// bundler. Uses Node's global ``fetch`` (Node 18+) which is also what
// VS Code ships.

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import * as vscode from "vscode";

// DaemonHint is what the daemon writes where it binds: its address and
// the bearer token every API route requires.
interface DaemonHint {
  url?: string;
  local_url?: string;
  dashboard_token?: string;
}

// hintPath mirrors the daemon's own rule: $XDG_DATA_HOME/tokenops/daemon.url,
// else ~/.tokenops/daemon.url.
function hintPath(): string {
  const xdg = process.env.XDG_DATA_HOME;
  return xdg ? path.join(xdg, "tokenops", "daemon.url") : path.join(os.homedir(), ".tokenops", "daemon.url");
}

// readHint reads the running daemon's address and token; undefined when
// no daemon has written one or it cannot be read.
function readHint(): DaemonHint | undefined {
  try {
    return JSON.parse(fs.readFileSync(hintPath(), "utf8")) as DaemonHint;
  } catch {
    return undefined;
  }
}

// configured is a setting's value only when the user set it, so the
// daemon's own address and token apply otherwise.
function configured(key: string): string | undefined {
  const i = vscode.workspace.getConfiguration("tokenops").inspect<string>(key);
  const v = i?.workspaceFolderValue ?? i?.workspaceValue ?? i?.globalValue;
  return v && v.length > 0 ? v : undefined;
}

export interface SeriesRow {
  BucketStart: string;
  GroupKey: string;
  Requests: number;
  TotalTokens: number;
  CostUSD: number;
}

export interface SeriesResponse {
  bucket: string;
  group: string;
  rows: SeriesRow[];
  currency: string;
}

export interface SpendSummary {
  Requests: number;
  InputTokens: number;
  OutputTokens: number;
  TotalTokens: number;
  CostUSD: number;
}

export interface SpendSummaryResponse {
  summary: SpendSummary;
  currency: string;
}

export interface OptimizationEntry {
  timestamp: string;
  kind: string;
  mode: string;
  decision: string;
  estimated_savings_tokens: number;
  estimated_savings_usd: number;
  quality_score: number;
  reason: string;
  workflow_id?: string;
  agent_id?: string;
}

export interface OptimizationListResponse {
  optimizations: OptimizationEntry[];
  currency: string;
}

export class TokenOpsClient {
  // base is the daemonUrl setting when set, else the address the running
  // daemon wrote, else the default.
  private base(): string {
    const hint = readHint();
    const url = configured("daemonUrl") ?? hint?.local_url ?? hint?.url ?? "http://127.0.0.1:7878";
    return url.replace(/\/$/, "");
  }
  // bearer is the bearerToken setting when set, else the token the
  // running daemon wrote: every API route requires one.
  private bearer(): string | undefined {
    const v = configured("bearerToken") ?? readHint()?.dashboard_token;
    return v && v.length > 0 ? v : undefined;
  }
  private async request<T>(path: string): Promise<T> {
    const headers: Record<string, string> = { Accept: "application/json" };
    const tk = this.bearer();
    if (tk) headers["Authorization"] = `Bearer ${tk}`;
    const resp = await fetch(this.base() + path, { headers });
    if (!resp.ok) {
      const body = await resp.text().catch(() => "");
      throw new Error(`${resp.status} ${path}: ${body.slice(0, 200)}`);
    }
    return (await resp.json()) as T;
  }
  spendSummary(params: { since?: string }): Promise<SpendSummaryResponse> {
    return this.request<SpendSummaryResponse>("/api/spend/summary" + qs(params));
  }
  spendSeries(params: { since?: string; bucket?: string; group?: string }): Promise<SeriesResponse> {
    return this.request<SeriesResponse>("/api/spend/series" + qs(params));
  }
  optimizations(params: { since?: string }): Promise<OptimizationListResponse> {
    return this.request<OptimizationListResponse>("/api/optimizations" + qs(params));
  }
}

function qs(params: Record<string, string | number | undefined>): string {
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") usp.set(k, String(v));
  }
  const s = usp.toString();
  return s ? "?" + s : "";
}
