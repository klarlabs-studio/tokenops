#!/usr/bin/env bash
set -euo pipefail

# Guardrail: default-deny direct pushes from protected branches.
# Override only for explicit emergency workflows:
#   ALLOW_PROTECTED_PUSH=1 git push origin main

branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"

if [[ "${ALLOW_PROTECTED_PUSH:-}" == "1" ]]; then
  exit 0
fi

case "$branch" in
  main|master)
    cat >&2 <<'EOF'
Push blocked by repository policy.

Protected branches (main/master) must be updated through a pull request.

Recommended flow:
  git checkout -b <type>/<short-name>
  git push -u origin <type>/<short-name>
  gh pr create

If this is an explicit emergency push, re-run with:
  ALLOW_PROTECTED_PUSH=1 git push origin <branch>
EOF
    exit 1
    ;;
esac

exit 0
