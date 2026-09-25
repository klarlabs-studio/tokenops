# Repository Guidelines

## Product and Architecture

TokenOps is a local-first adaptive control plane for AI-assisted work. It observes work and heterogeneous resources, applies policy, recommends or performs authorized interventions, verifies outcomes, and learns. Optimize for total utility—quality, time, cost, capacity, attention, privacy, and preference—not minimum usage.

The harness owns task decomposition and execution; TokenOps governs resources around it. Do not turn TokenOps into an agent framework or project manager. Keep evidence, beliefs, policies, decisions, interventions, and outcomes distinct. Evidence retains provenance, freshness, confidence, and scope; decisions record alternatives and remain explainable. Learning is outcome-based and retractable. Autonomy progresses per capability from shadow to recommendation to execution.

The local daemon is canonical. Keep entitlements outside core domains. Model insights structurally before adapting them to each surface; prefer ambient, actionable UX over interruptions.

## Project Structure

- `cmd/tokenops`, `cmd/tokenopsd`: CLI and daemon entry points.
- `internal/contexts`: DDD domains for control-plane capabilities.
- `internal/{infra,storage,events,proxy,mcp}`: adapters and runtime infrastructure.
- `pkg/eventschema`: public event contracts and protobuf definitions.
- `web/docs`: VitePress documentation site. The daemon exposes a local HTTP API; there is no bundled browser dashboard.
- `integrations`: VS Code and Python adapters; `docs/adr`: architecture decisions.

## Build and Verification

```bash
make build        # build bin/tokenops and bin/tokenopsd
make test         # race-enabled Go tests with coverage
make verify       # format, vet, lint, test, eval, security, and proto gates
make install-hooks
```

Before pushing, explicitly run `gofmt -l .`, `golangci-lint run ./...`, and `go test ./...`. After adding dependencies, confirm `go.mod` remains on Go 1.25.

## Code and Test Conventions

Use idiomatic Go, `gofmt`, and GoDoc for exported APIs. Keep TypeScript strict and follow repository ESLint/Prettier settings. Tests live beside code as `*_test.go`; prefer table-driven tests and real migration paths. Add optimizer fixtures under `internal/contexts/optimization/eval/testdata`.

Register new `internal/contexts` domains in `internal/archlint`'s `domainPackages`. Formatter safety belongs only in the engine's `enforceCritical` guard; shared engine changes require golden survival and monotonic tests. Never log secrets or prompt bodies; redact outbound data.

## Commits and Pull Requests

Branch from fresh `origin/main` using `feat/`, `fix/`, or `chore/`. Use atomic Conventional Commits, e.g. `feat(work): associate attempts with outcomes`. PRs explain intent, risks, verification, linked issue/task, and include UI screenshots. Never push directly to `main`. Merge with `gh pr merge <n> --squash --admin --delete-branch`.

## Agent Workflow

Recommend a clear default; ask only about consequential forks. Complete obvious verification without waiting. Verify surprising pricing changes against the authoritative vendor source. Read `memory/status.md` for current context and `memory/decisions.md` for durable decisions.
