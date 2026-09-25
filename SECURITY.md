# Security Policy

## Supported versions

TokenOps follows semantic versioning. Security fixes land on the latest
minor; older minors receive only critical patches at maintainer discretion.

| Version | Supported |
|---------|-----------|
| 0.70.x  | ✅        |
| < 0.70  | ❌        |

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security reports.

Email `felix.geelhaar@gmail.com` with:

- A short description of the issue and its impact
- Steps to reproduce (or a minimal proof of concept)
- Affected version (`tokenops version` output)
- Whether you'd like credit in the release notes

Acknowledgement within 72 hours. Coordinated disclosure: a fix lands on a
private branch, a release is cut, and the advisory publishes alongside.

## Threat model

TokenOps is a **local-first** daemon. Default install binds `127.0.0.1`
and assumes the host is trusted. Notable surfaces:

- **Local API auth.** `/api/*` requires a shared-secret token
  (since v0.10.3 — see the correction below). Health probes (`/healthz`,
  `/readyz`, `/version`) stay public. Constant-time token comparison. The
  token is persisted at `~/.tokenops/dashboard.token` with `0600`
  permissions on POSIX. The setting and file retain their historical
  `dashboard` names for compatibility; no browser UI is served.

  **Correction (0.69.0).** From v0.10.3 until 0.69.0 that sentence was true
  of most of `/api/*` but not all of it. `/api/audit`,
  `/api/rules/{analyze,conflicts,compress,inject}` and `/api/domain-events`
  were mounted beside the authenticated routes rather than inside them, and
  Go's router prefers an exact path over the `/api/` pattern the middleware
  wrapped — so those six answered without a credential. A loopback-bound
  daemon, the default, was reachable only from the host; a daemon bound to a
  LAN address served its own audit log to the network. Fixed in 0.69.0:
  every `/api` route now registers on the mux the middleware wraps.
- **mDNS advertise** (v0.10.1+). Advertised IPs match the bind address —
  loopback-only listener publishes `127.0.0.1`; a wildcard / LAN-bound
  listener publishes every non-loopback interface and is reachable from the
  LAN. Operators binding beyond loopback should configure a strong
  `TOKENOPS_DASHBOARD_ADMIN_TOKEN` before sharing the host.
- **Vendor admin credentials** (v0.10.2+). `vendor_usage.anthropic.admin_key`
  carries a `sk-ant-admin-*` key, and four other fields carry vendor
  session credentials. When written to `config.yaml` they are stored in
  plain text; the file and its directory are kept at `0600`/`0700`, and
  since 0.70.0 every write repairs the mode of a file it finds rather than
  only the ones it creates.

  To keep a credential out of the file entirely, set it in the
  environment. These win over the file and are never written back:

  | Variable | Credential |
  |----------|------------|
  | `TOKENOPS_DASHBOARD_ADMIN_TOKEN` | local API admin token (legacy variable name) |
  | `TOKENOPS_ANTHROPIC_ADMIN_KEY` | Anthropic admin key (`sk-ant-admin-*`) |
  | `TOKENOPS_CLAUDE_USAGE_METER_SESSION_KEY` | claude.ai session cookie |
  | `TOKENOPS_CURSOR_COOKIE` | cursor.com session cookie |
  | `TOKENOPS_COPILOT_OAUTH_TOKEN` | GitHub Copilot OAuth token |

  **Correction (0.70.0).** Before 0.70.0 this entry advised "environment
  substitution", which no code implemented: `config.Load` expanded no
  `${VAR}`, and its environment overrides covered listen addresses and
  endpoints but not one credential. The variables above are the promise,
  made real. Note that `tokenops vendor-usage enable` reads similarly
  named variables and *persists* what it finds into `config.yaml`; the
  daemon-side reads above do not.
- **Event store** (`~/.tokenops/events.db`). SQLite, no encryption at rest.
  Contains prompt hashes (not raw prompts) by default, plus token counts,
  model names, and timestamps. Treat as you would any other local telemetry
  database.

## Out of scope

- Denial-of-service against the local daemon by the local user
- Supply-chain attacks on third-party Go modules (we run `nox scan` in CI)
