// Minimal HTTP client for the TokenOps daemon. Mirrors the dashboard
// surface but lives in CommonJS so the extension does not need a
// bundler. Uses Node's global ``fetch`` (Node 18+) which is also what
// VS Code ships.

import * as vscode from "vscode";

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
  private base(): string {
    const cfg = vscode.workspace.getConfiguration("tokenops");
    return (cfg.get<string>("daemonUrl") ?? "http://127.0.0.1:7878").replace(/\/$/, "");
  }
  private bearer(): string | undefined {
    const v = vscode.workspace.getConfiguration("tokenops").get<string>("bearerToken");
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
