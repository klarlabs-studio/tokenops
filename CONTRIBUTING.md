# Contributing to TokenOps

Thanks for your interest in TokenOps. This project follows spec-driven
planning, atomic commits, and TDD where practical.

## Quick start

```bash
git clone https://github.com/felixgeelhaar/tokenops.git
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

Email `security@tokenops.dev` (placeholder). Do not file public issues for
security-sensitive reports.

## License

By contributing you agree that your contributions are licensed under the
Apache License 2.0 (see `LICENSE`).
