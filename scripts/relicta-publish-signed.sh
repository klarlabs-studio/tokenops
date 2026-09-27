#!/bin/sh

# Publish an approved Relicta release without trusting Relicta 4.2.0's broken
# gitsign/autocommitchangelog enforcement. Relicta owns planning, notes, and
# approval; Git owns the signed tag and push that triggers GoReleaser.
set -eu

fail() {
	printf 'release guard: %s\n' "$*" >&2
	exit 1
}

root=$(git rev-parse --show-toplevel)
cd "$root"

[ "$(git branch --show-current)" = "main" ] || fail "release must run from main"
[ -z "$(git status --porcelain)" ] || fail "working tree is not clean"
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main is not aligned with origin/main"

status=$(relicta status --json)
state=$(printf '%s\n' "$status" | sed -n 's/.*"state": "\([^"]*\)".*/\1/p')
version=$(printf '%s\n' "$status" | sed -n 's/.*"next_version": "\([^"]*\)".*/\1/p')
[ "$state" = "approved" ] || fail "Relicta release is $state, expected approved"
[ -n "$version" ] || fail "Relicta did not report a next version"

tag="v$version"
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	fail "$tag already exists locally"
fi
if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	fail "$tag already exists on origin"
fi

changelog_before=$(git hash-object CHANGELOG.md)
relicta publish --skip-tag --skip-push

# Relicta 4.2.0 appends generated notes despite autocommitchangelog=false.
# Restore only that known mutation, and refuse every other repository change.
if [ "$(git hash-object CHANGELOG.md)" != "$changelog_before" ]; then
	git restore --source=HEAD -- CHANGELOG.md
fi
[ -z "$(git status --porcelain)" ] || fail "publish changed files other than CHANGELOG.md"

# Defense in depth: configuration disables native tag/push, and the CLI flags
# repeat that intent. Relicta 4.2.0 previously ignored the CLI flags while the
# config enabled tagging. Never overwrite a remotely visible release here.
if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	fail "Relicta unexpectedly pushed $tag; refusing to replace a remote release tag"
fi
if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	git tag -d "$tag"
fi

git tag -s "$tag" -m "Release $tag"
git verify-tag "$tag"
[ "$(git rev-list -n 1 "$tag")" = "$(git rev-parse HEAD)" ] || fail "$tag does not point at HEAD"
git push origin "$tag"

printf 'release guard: pushed verified signed tag %s at %s\n' "$tag" "$(git rev-parse --short HEAD)"
