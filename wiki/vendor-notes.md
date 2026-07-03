---
updated: 2026-07-03
tags: [vendors, reference]
---
# Vendor / external notes

## rtk-ai/rtk (comparison target)
Rust CLI proxy that compresses shell command output before it hits an LLM context. 100+ commands. Claims 60-90% token reduction. Auto-rewrite bash hook. Single binary, no deps.
- **Lacks vs tokenops fmt**: no critical-line survival guarantee, no per-command loss config, no proxy plane, no cost/dashboard integration, no learning loop. Cloud coverage is AWS-only.
- **Covers that tokenops does too**: git, go test, cargo, npm/pnpm, pytest, jest/vitest/playwright/rspec, eslint/tsc/ruff/golangci-lint/rubocop/prettier/biome, docker/kubectl, pip/bundle, pulumi, aws.
- **tokenops-only (RTK gaps)**: make, mvn, gradle, sbt, mix, dotnet, cmake, ninja, bazel, apt/dnf/brew, terraform, ansible, helm, gcloud/az, flyway/alembic, curl, uv, composer.

## Plan catalog (rate-limit prediction)
13 plans w/ dated vendor source URLs pinned in code: Claude Max 5x/20x/Pro, Claude Code, ChatGPT Plus/Pro/Team, Copilot Individual/Business, Cursor Pro/Business, Mistral Le Chat Pro, Codex Plus. Proxy providers: OpenAI, Anthropic, Gemini, Mistral.

## Claude Code hooks (integration facts)
- PreToolUse hook stdin: {session_id, tool_name, tool_input:{file_path,offset,limit}, cwd, ...}. Deny via stdout {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}; exit 0 + no stdout = allow/no-op. PreToolUse can only allow/deny — it CANNOT modify tool_input.
- settings.json hook changes are picked up MID-SESSION via a file watcher — no restart needed (I wrongly assumed a startup snapshot; corrected 2026-07-03). `/hooks` is a view-only browser (confirms loaded hooks; can't toggle).
- Wire a hook: settings.json hooks.PreToolUse[].{matcher:"Read", hooks:[{type:"command", command:"<abs path>", args:[...], timeout:10}]}. args:[] execs directly (no shell).
