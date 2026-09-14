# Contributing to TokenOps

Thanks for your interest in TokenOps. This project follows spec-driven
planning, atomic commits, and TDD where practical.

## Quick start

```bash
git clone https://github.com/klarlabs-studio/tokenops.git
cd tokenops
make tools     # install dev tooling (golangci-lint, etc.)
make build     # build all Go binaries
make test      # run Go test suite
```

## Development workflow

1. Check the active plan in `.roady/` (`mcp__roady__roady_get_plan`) for
   ready tasks. Pick one whose dependencies are satisfied.
2. Branch from `main`: `git checkout -b feat/<short-name>`.
3. Follow TDD: write a failing test, make it pass, refactor.
4. Keep commits atomic. Use [Conventional Commits](https://www.conventionalcommits.org/):
   - `feat: ...`, `fix: ...`, `docs: ...`, `refactor: ...`, `test: ...`,
     `chore: ...`, `perf: ...`.
5. Run `make verify` before pushing (`fmt`, `lint`, `test`).
6. Open a pull request describing the change and linking the relevant Roady
   task ID.

## Branch protection policy

- `main` is a protected branch and should be updated **only via pull request**.
- Do not push directly to `main`/`master` during normal development.
- Use short-lived feature branches (`feat/...`, `fix/...`, `chore/...`) and
  merge through GitHub PR checks.

Install local guardrails once per clone:

```bash
make install-hooks
```

Git hooks live in `.git/hooks`, which is not cloned — so this is genuinely
per clone, and skipping it leaves you with no local gate even though
`.warden.yaml` says `enabled: true`.

What it arms:

| Hook | Runs |
|---|---|
| `pre-commit` | `fmt`, `vet` — kept cheap enough not to resent |
| `pre-push` | the protected-branch guard, then `tidy`, `fmt`, `vet`, `lint`, `test`, `sec-gate`, `vulns`, `proto-verify` |

The pre-push gate is the same set of checks CI runs, moved in front of the
push instead of after it, so a lint error is yours to fix rather than a red
build for everyone else. Without `warden` on your PATH the target installs
the protected-branch guard on its own, so that one never depends on warden
being present.

The protected-branch guard blocks direct pushes from `main`/`master`.
Emergency override exists for explicit incident workflows:

```bash
ALLOW_PROTECTED_PUSH=1 git push origin main
```

## Code standards

- **Go**: `gofmt`, `goimports`, `golangci-lint run` must be clean. Public APIs
  need GoDoc comments.
- **TypeScript / Vue (in `web/`)**: ESLint + Prettier, TypeScript strict mode.
- **Tests**: unit + integration. Critical paths require coverage. Avoid
  mocking the database for migration-touching code.
- **Security**: never log prompt bodies or secrets. Run new outbound emission
  paths through the redaction layer.
- **Performance**: proxy code paths must respect the latency budget
  (see `internal/proxy`).

## Reporting issues

Use GitHub issues. Include reproduction steps, expected vs. actual behaviour,
TokenOps version, and your platform.

## Security disclosures

Email `felix.geelhaar@gmail.com`. Do not file public issues for
security-sensitive reports.

## License

By contributing you agree that your contributions are licensed under the
Apache License 2.0 (see `LICENSE`).
