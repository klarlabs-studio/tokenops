# Releasing TokenOps

TokenOps uses Relicta for release planning, version selection, notes, and
approval. Relicta 4.2.0 does not reliably honor `gitsign: true` or
`autocommitchangelog: false`; it also ignored `--skip-tag` and `--skip-push`
when the corresponding configuration remained enabled. The canonical config
therefore disables Relicta tag creation, push, and signing. Git owns those
steps through the repository guard.

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
the GoReleaser workflow. As defense in depth, it refuses an unexpected remote
tag and removes an unexpected local-only tag before signing.

After publishing, verify the GitHub release assets, Homebrew update, installed
CLI/daemon version and commit, and daemon health/readiness.
