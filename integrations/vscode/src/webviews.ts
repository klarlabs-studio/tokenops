// Webview HTML generators. Standalone strings (no React, no bundling)
// so the extension stays a single transpilation step.

import type { OptimizationListResponse, SeriesResponse } from "./client";

export function renderBurnGraphHtml(series: SeriesResponse): string {
  const rows = series.rows ?? [];
  const max = rows.reduce((m, r) => (r.CostUSD > m ? r.CostUSD : m), 0) || 1;
  const W = 720;
  const H = 240;
  const PAD_L = 48;
  const PAD_R = 12;
  const PAD_T = 16;
  const PAD_B = 28;
  const innerW = W - PAD_L - PAD_R;
  const innerH = H - PAD_T - PAD_B;
  const points = rows.map((r, i) => {
    const x = rows.length > 1 ? PAD_L + (i / (rows.length - 1)) * innerW : PAD_L;
    const y = PAD_T + innerH - (r.CostUSD / max) * innerH;
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  });
  const path = points.length > 0 ? "M" + points.join(" L") : "";
  const total = rows.reduce((s, r) => s + r.CostUSD, 0);
  return baseHtml(
    "Burn graph (24h)",
    `
    <h1>Burn graph · last 24h</h1>
    <div class="kv">
      <span>Total</span>
      <strong>${total.toFixed(4)} ${escapeHtml(series.currency)}</strong>
    </div>
    <svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="xMidYMid meet">
      <line x1="${PAD_L}" x2="${W - PAD_R}" y1="${PAD_T + innerH}" y2="${PAD_T + innerH}" stroke="#3a414c"/>
      <path d="${path}" fill="none" stroke="#6b8aff" stroke-width="2"/>
    </svg>
    <p class="hint">${rows.length} hourly buckets · max ${max.toFixed(4)} ${escapeHtml(series.currency)}</p>
    `,
  );
}

export function renderOptimizationsHtml(opts: OptimizationListResponse): string {
  const items = opts.optimizations ?? [];
  const rows = items
    .map(
      (it) => `
      <tr>
        <td>${escapeHtml(new Date(it.timestamp).toLocaleString())}</td>
        <td>${escapeHtml(it.kind)}</td>
        <td>${escapeHtml(it.decision)}</td>
        <td>${it.estimated_savings_tokens.toLocaleString()}</td>
        <td>${it.estimated_savings_usd.toFixed(4)}</td>
        <td>${escapeHtml(it.reason)}</td>
      </tr>`,
    )
    .join("");
  return baseHtml(
    "Optimizations",
    `
    <h1>Optimization suggestions · last 7d</h1>
    ${
      items.length === 0
        ? '<p class="hint">No optimization events yet. Run <code>tokenops replay &lt;workflow&gt;</code>.</p>'
        : `<table>
            <thead>
              <tr>
                <th>Time</th><th>Kind</th><th>Decision</th>
                <th>Save tok</th><th>Save $</th><th>Reason</th>
              </tr>
            </thead>
            <tbody>${rows}</tbody>
          </table>`
    }
    `,
  );
}

function baseHtml(title: string, body: string): string {
  return `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8" />
<title>${escapeHtml(title)}</title>
<style>
  body { font-family: ui-sans-serif, -apple-system, "Segoe UI", sans-serif; padding: 24px; color: var(--vscode-foreground); }
  h1 { font-size: 18px; margin: 0 0 16px; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { padding: 6px 8px; border-bottom: 1px solid var(--vscode-panel-border, #3a414c); text-align: left; }
  th { color: var(--vscode-descriptionForeground); font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; }
  .kv { display: flex; gap: 12px; align-items: baseline; margin-bottom: 16px; }
  .kv span { color: var(--vscode-descriptionForeground); }
  .hint { color: var(--vscode-descriptionForeground); font-size: 12px; }
  svg { width: 100%; height: auto; max-height: 280px; }
  code { font-family: ui-monospace, monospace; }
</style>
</head>
<body>
${body}
</body>
</html>`;
}

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c] ?? c),
  );
}
