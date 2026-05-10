# Security

This project uses [nox](https://github.com/felixgeelhaar/nox) for
vulnerability scanning, secret detection, supply-chain checks, and
infrastructure-as-code review. Dependabot is intentionally **disabled**
at the repository level — nox is the single source of security signal.

## Files

| Path                          | Purpose                                                |
|-------------------------------|--------------------------------------------------------|
| `security/vex.json`           | OpenVEX waivers for known false positives              |
| `.github/workflows/security.yml` | Push / PR / nightly nox scan job                    |
| `findings.json` (artifact)    | Latest scan output, uploaded by every CI run          |

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
brew install felixgeelhaar/nox/nox
# or download a release from
# https://github.com/felixgeelhaar/nox/releases
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
