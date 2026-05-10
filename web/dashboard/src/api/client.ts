// Lightweight API client for the TokenOps daemon. The daemon listens on
// 127.0.0.1:7878 by default; in development the Vite dev server proxies
// all known paths there, so the dashboard never needs to know the
// daemon URL. In production the dashboard is served from the daemon
// itself, making same-origin fetch sufficient.

export interface Health {
  status: string;
  started_at: string;
  uptime_ns: number;
}

export interface Ready {
  status: "ready" | "not_ready";
}

export interface Version {
  version: string;
  commit: string;
  date: string;
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

export interface SeriesRow {
  BucketStart: string;
  GroupKey: string;
  Requests: number;
  InputTokens: number;
  OutputTokens: number;
  TotalTokens: number;
  CostUSD: number;
}

export interface SeriesResponse {
  bucket: string;
  group: string;
  rows: SeriesRow[];
  currency: string;
}

export interface ForecastPoint {
  At: string;
  Value: number;
  Lower: number;
  Upper: number;
}

export interface ForecastResponse {
  horizon_days: number;
  history: SeriesRow[];
  forecast: ForecastPoint[];
  currency: string;
}

export interface WorkflowSummary {
  workflow_id: string;
  requests: number;
  tokens: number;
  cost_usd: number;
}

export interface WorkflowListResponse {
  workflows: WorkflowSummary[];
  currency: string;
}

export interface WorkflowStep {
  Index: number;
  Prompt: {
    request_model: string;
    input_tokens: number;
    output_tokens: number;
    latency_ns: number;
    workflow_id?: string;
    agent_id?: string;
    cost_usd?: number;
  };
  ContextDelta: number;
  Latency: number;
  StartGap: number;
}

export interface WorkflowTrace {
  WorkflowID: string;
  Steps: WorkflowStep[];
  StartedAt: string;
  EndedAt: string;
  TotalInputTokens: number;
  TotalOutputTokens: number;
  TotalCostUSD: number;
  StepCount: number;
  MaxContextSize: number;
  ContextGrowthTotal: number;
}

export interface CoachingFinding {
  Kind: string;
  Summary: string;
  Details?: string;
  EstimatedSavingsTokens?: number;
}

export interface WorkflowDetailResponse {
  trace: WorkflowTrace;
  findings: CoachingFinding[] | null;
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

export class ApiError extends Error {
  status: number;
  url: string;
  constructor(status: number, url: string, message?: string) {
    super(`${status} ${url}${message ? `: ${message}` : ""}`);
    this.status = status;
    this.url = url;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    ...init,
    headers: { Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new ApiError(resp.status, path, body.slice(0, 200));
  }
  return (await resp.json()) as T;
}

function qs(params: Record<string, string | number | undefined>): string {
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") {
      usp.set(k, String(v));
    }
  }
  const s = usp.toString();
  return s ? "?" + s : "";
}

export const api = {
  health: () => request<Health>("/healthz"),
  ready: () => request<Ready>("/readyz"),
  version: () => request<Version>("/version"),

  spendSummary: (params: { since?: string; until?: string; workflow_id?: string } = {}) =>
    request<SpendSummaryResponse>("/api/spend/summary" + qs(params)),

  spendSeries: (params: {
    since?: string;
    until?: string;
    bucket?: "hour" | "day";
    group?: "model" | "provider" | "workflow" | "agent" | "";
    workflow_id?: string;
  } = {}) => request<SeriesResponse>("/api/spend/series" + qs(params)),

  spendForecast: (params: { horizon_days?: number } = {}) =>
    request<ForecastResponse>("/api/spend/forecast" + qs(params)),

  workflows: (params: { since?: string; until?: string } = {}) =>
    request<WorkflowListResponse>("/api/workflows" + qs(params)),

  workflowDetail: (id: string) =>
    request<WorkflowDetailResponse>(`/api/workflows/${encodeURIComponent(id)}`),

  optimizations: (params: { since?: string; workflow_id?: string } = {}) =>
    request<OptimizationListResponse>("/api/optimizations" + qs(params)),
};
