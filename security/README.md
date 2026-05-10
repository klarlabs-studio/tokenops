# Security

This project uses [nox](https://github.com/nox-hq/nox) for
vulnerability scanning, secret detection, supply-chain checks, and
infrastructure-as-code review. Dependabot is intentionally **disabled**
at the repository level — nox is the single source of security signal.

## Files

| Path                                  | Purpose                                                  |
|---------------------------------------|----------------------------------------------------------|
| `security/vex.json`                   | OpenVEX waivers for known false positives                |
| `.github/workflows/security.yml`      | Push / PR / nightly nox scan job (the build gate)        |
| `.github/workflows/nox-remediate.yml` | Nightly + manual `nox fix` → opens a remediation PR      |
| `findings.json` (artifact)            | Latest scan output, uploaded by every CI run            |

## Gate

The CI gate fails the build on **any critical finding** not waived in
`security/vex.json`:

```bash
nox scan . \
  -severity-threshold critical \
  -vex security/vex.json \
  -fail-on-unwaived
```

Below `critical` is informational — the full `findings.json` is
uploaded as a CI artifact and inspected via the Security tab.

## Run locally

Install nox once:

```bash
brew install nox-hq/nox/nox
# or download a release from
# https://github.com/nox-hq/nox/releases
```

Scan:

```bash
nox scan .
```

Re-run the same gate the CI does:

```bash
nox scan . -severity-threshold critical -vex security/vex.json -fail-on-unwaived
```

Pre-commit hook (optional, blocks `git commit` on critical findings):

```bash
nox install-hook
```

## Remediation

`nox-remediate.yml` runs nightly at 04:00 UTC (and on
`workflow_dispatch`). It wraps the official
[`nox-hq/nox-remediate-action`](https://github.com/Nox-HQ/nox-remediate-action):
scan → `nox fix` upgrade plan → apply → run `go test ./...` to verify
→ open / refresh `chore(deps): nox remediate` on `nox/remediate`.

Trigger it manually:

```bash
gh workflow run nox-remediate.yml \
  -f include_major=false   # set true to allow major-version bumps
```

Apply the same upgrades locally:

```bash
nox scan .
nox fix -dry-run         # preview
nox fix                  # apply
go mod tidy
(cd web/dashboard && npm install --package-lock-only)
(cd web/docs      && npm install --package-lock-only)
```

## Plugins

`.nox.yaml` declares the nox plugins this project requires. CI installs
them automatically via `plugins.required` when nox first scans; locally
run `nox install` to pull them.

Currently enabled:

- **`nox/triage-agent`** — LLM-powered finding prioritisation. Downranks
  likely false positives so the high-noise rules (high-entropy hex,
  AI-semconv key collisions) don't drown the actionable signal.

The remediation plugin (`nox/remediate`, code-level fixes for security
headers and log redaction) ships from
[`Nox-HQ/nox-plugin-remediate`](https://github.com/Nox-HQ/nox-plugin-remediate);
once it cuts a stable release we'll add it under `plugins.required`.
Dependency-side remediation already runs through the **action** above,
which uses the built-in `nox fix` command.

`.nox.yaml` also carries the `scan.exclude` list. We exclude:

- `.roady/` event logs (UUIDs trip the AWS-secret regex)
- npm `package-lock.json` files (transitive hashes are noise; real
  CVEs in those trees surface via VULN-001)
- generated build artefacts (`.vitepress/dist`, `web/dashboard/dist`,
  `integrations/vscode/out`, `findings.json`)
- `internal/redaction/redactor_test.go` (deliberately embeds sample
  AWS keys to exercise the redaction package)

## Triaging a new finding

1. Run `nox scan . -severity critical` (or any severity) to surface it.
2. If it is **real**, fix the underlying code / dependency. For npm
   trees, `npm install <pkg>@<fixed-version>` and rebuild lockfiles
   (overrides in `package.json` work for transitive pins).
3. If it is a **false positive**, add an OpenVEX statement to
   `security/vex.json`:

```json
{
  "vulnerability": "<rule-or-CVE-id>",
  "status": "not_affected",
  "justification": "vulnerable_code_not_present",
  "impact_statement": "Why this finding is a false positive — be specific.",
  "products": ["github.com/felixgeelhaar/tokenops"],
  "_nox_fingerprint": "<full fingerprint from the finding>"
}
```

Use `vulnerable_code_not_present`, `vulnerable_code_not_in_execute_path`,
`vulnerable_code_cannot_be_controlled_by_adversary`, `inline_mitigations_already_exist`,
or `component_not_present` per the OpenVEX spec.

## Why nox over Dependabot

- One tool covers CVEs, secrets, supply-chain typosquatting, IaC
  hardening, and AI-specific risks — Dependabot only handles the
  first.
- Findings stay out of GitHub's "open a PR with a version bump"
  noise loop. Real issues fail the build; false positives waive
  through OpenVEX rather than rotting in alert lists.
- Local + CI use the exact same binary — no "works on Dependabot
  but not on my machine" gap.
