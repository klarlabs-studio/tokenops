#!/bin/sh

# Publish an approved Relicta release without invoking Relicta 4.2.0's unsafe
# publish action. Relicta owns planning, notes, and approval; Git owns the
# signed tag and push that triggers GoReleaser.
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

git tag -s "$tag" -m "Release $tag"
git verify-tag "$tag"
[ "$(git rev-list -n 1 "$tag")" = "$(git rev-parse HEAD)" ] || fail "$tag does not point at HEAD"
git push origin "$tag"

# Relicta has no safe "mark externally published" operation. Cancel the
# approved run only after the signed tag is visible remotely so the next plan
# can start; the tag, GitHub release, and assets are the publication record.
relicta cancel

printf 'release guard: pushed verified signed tag %s at %s\n' "$tag" "$(git rev-parse --short HEAD)"
