# Releasing TokenOps

TokenOps uses Relicta for release planning, version selection, notes, and
approval. Relicta 4.2.0 does not reliably honor `gitsign: true` or
`autocommitchangelog: false`, so it must not create or push the release tag.

Prepare the release normally from a clean, up-to-date `main`:

```bash
relicta plan --no-ai --skip-cognitive
relicta bump
relicta notes
relicta approve
```

Publish with the repository guard:

```bash
scripts/relicta-publish-signed.sh
```

The guard requires an approved Relicta run, a clean `main` aligned with
`origin/main`, and a new version. It calls `relicta publish` with tag creation
and pushing disabled, removes only the known forbidden `CHANGELOG.md` mutation,
refuses any other working-tree change, creates an SSH-signed tag with Git,
verifies its signature and target, and only then pushes the tag that triggers
the GoReleaser workflow.

After publishing, verify the GitHub release assets, Homebrew update, installed
CLI/daemon version and commit, and daemon health/readiness.
