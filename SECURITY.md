# Security Policy

## Supported versions

TokenOps follows semantic versioning. Security fixes land on the latest
minor; older minors receive only critical patches at maintainer discretion.

| Version | Supported |
|---------|-----------|
| 0.11.x  | ✅        |
| < 0.11  | ❌        |

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

- **Dashboard auth.** `/dashboard` and `/api/*` require a shared-secret token
  (since v0.10.3). Health probes (`/healthz`, `/readyz`, `/version`) stay
  public. Constant-time token comparison. The token is persisted at
  `~/.tokenops/dashboard.token` with `0600` permissions on POSIX.
- **mDNS advertise** (v0.10.1+). Advertised IPs match the bind address —
  loopback-only listener publishes `127.0.0.1`; a wildcard / LAN-bound
  listener publishes every non-loopback interface and is reachable from the
  LAN. Operators binding beyond loopback should rotate the dashboard token
  (`tokenops dashboard rotate-token`) before sharing the host.
- **Vendor admin credentials** (v0.10.2+). `vendor_usage.anthropic.admin_key`
  carries a `sk-ant-admin-*` key. Stored in plain text in `config.yaml`;
  protect the config file with filesystem permissions or environment
  substitution.
- **Event store** (`~/.tokenops/events.db`). SQLite, no encryption at rest.
  Contains prompt hashes (not raw prompts) by default, plus token counts,
  model names, and timestamps. Treat as you would any other local telemetry
  database.

## Out of scope

- Denial-of-service against the local daemon by the local user
- Cross-site script injection in the local dashboard (the operator is the
  only viewer; CSP is not currently enforced)
- Supply-chain attacks on third-party Go modules (we run `nox scan` in CI)
