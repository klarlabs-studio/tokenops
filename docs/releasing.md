# Releasing TokenOps

TokenOps uses Relicta for release planning, version selection, notes, and
approval. Relicta 4.2.0's publish action ignores tag, push, and signing controls
in the observed workflow and mutates `CHANGELOG.md` despite
`autocommitchangelog: false`. Do not run `relicta publish`. The canonical
config disables Relicta tag creation, push, and signing as defense in depth;
Git owns publication through the repository guard.

Prepare the release normally from a clean, up-to-date `main`:

```bash
relicta plan --no-ai --skip-cognitive
relicta bump
relicta notes
relicta approve
```

Before publishing, run the daemon under every supported configuration:

```bash
make config-matrix ARGS=--long
```

Each profile runs isolated against a local fake upstream; the target exits
non-zero if a valid profile fails to start, proxy, gate `/api/*`, or stop
cleanly, or if an invalid one is accepted.

## Before a release

The upgrade gate proves that an installed, supervised daemon moves onto the
new version by itself. The release workflow runs it before goreleaser, and
goreleaser does not start until it passes. It runs the latest published release
and a candidate built from the tag: the launchd variant on a macOS runner, then
the Homebrew variant, which installs the published cask and upgrades it to the
candidate. Each variant asserts that the old daemon logs the replacement, the
supervisor starts the new binary, and the API reports the new version within
two minutes.

To run the launchd variant on your Mac:

```bash
make upgrade-check
```

It runs an isolated test job, with its own label, HOME, XDG directories, port
and log. It never touches the real daemon, `~/.tokenops`, `~/.config/tokenops`
or Homebrew. A published release can be supervised under the test label only
once it honours `TOKENOPS_SUPERVISOR_LABEL`; until then, pass the old binary:
`make upgrade-check OLD_BINARY=/path/to/tokenops OLD_VERSION=0.126.0`.

Publish with the repository guard:

```bash
scripts/relicta-publish-signed.sh
```

The guard requires an approved Relicta run, a clean `main` aligned with
`origin/main`, and a new version. It refuses an existing local or remote tag,
creates an SSH-signed tag with Git, verifies its signature and target, and only
then pushes the tag that triggers the GoReleaser workflow. After the signed tag
is visible remotely, it cancels the approved Relicta run so another plan can
start. The tag, GitHub release, and assets—not Relicta's state label—are the
publication record.

After publishing, verify the GitHub release assets, Homebrew update, installed
CLI/daemon version and commit, and daemon health/readiness.

Then bring the website's release highlights up to date. The page at
<https://team.klarlabs.de/changelog> is written by hand in the private
`tokenops-team` repository (`site/changelog.md`): add a short headline and
paragraph for the release, set its `Current release` line (the landing page
reads its version from it), run `scripts/sync-reference.sh` there to copy
this release's `docs/reference` pages into the site, and deploy the team server (tag, then the
`newTag` bump RollOps applies). A daily check there,
`scripts/ci/site-current.sh`, fails while the page names an older release
than the latest one here, or while the reference pages are not that
release's.
