#!/usr/bin/env bash
# Build the team plane's container image (linux/amd64 + linux/arm64) from a
# release's tokenops-team archives, and push it with --push.
#
#   scripts/build-team-image.sh <version> <archives-dir> [--push]
#
# <archives-dir> holds checksums.txt and tokenops-team_<version>_linux_{amd64,arm64}.tar.gz:
# the release's assets (release.yml), or goreleaser's dist/ from a snapshot
# (ci.yml), so CI builds the image exactly as a release does. Every archive is
# checked against checksums.txt before it is unpacked. Before anything is
# pushed, the linux/amd64 image is loaded and run, and must report <version>.
#
# Environment: TEAM_IMAGE (default ghcr.io/klarlabs-studio/tokenops-team),
# TEAM_IMAGE_REVISION (default: git HEAD). Pushing needs a prior
# `docker login ghcr.io`.
set -euo pipefail

usage() { echo "usage: $0 <version> <archives-dir> [--push]" >&2; exit 2; }
[ $# -ge 2 ] && [ $# -le 3 ] || usage
version=$1
dir=$2
push=false
if [ $# -eq 3 ]; then
  [ "$3" = "--push" ] || usage
  push=true
fi
[ -n "$version" ] || usage

repo_root=$(cd "$(dirname "$0")/.." && pwd)
image=${TEAM_IMAGE:-ghcr.io/klarlabs-studio/tokenops-team}
revision=${TEAM_IMAGE_REVISION:-$(git -C "$repo_root" rev-parse HEAD)}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

ctx=$(mktemp -d)
builder="tokenops-team-$$"
cleanup() {
  docker buildx rm "$builder" >/dev/null 2>&1 || true
  rm -rf "$ctx"
}
trap cleanup EXIT

# Verify exactly the two archives against the release's manifest.
(
  cd "$dir"
  grep -E "  tokenops-team_${version//./\\.}_linux_(amd64|arm64)\.tar\.gz\$" checksums.txt > "$ctx/team.sha256" || true
  if [ "$(wc -l < "$ctx/team.sha256")" -ne 2 ]; then
    echo "::error::checksums.txt does not list both tokenops-team ${version} linux archives" >&2
    exit 1
  fi
  sha256 --check --strict "$ctx/team.sha256"
)

for arch in amd64 arm64; do
  mkdir -p "$ctx/linux/$arch"
  tar -xzf "$dir/tokenops-team_${version}_linux_${arch}.tar.gz" -C "$ctx/linux/$arch" tokenops-team
done
cp "$repo_root/deploy/team/Dockerfile.release" "$ctx/Dockerfile"

labels=(
  --label "org.opencontainers.image.title=tokenops-team"
  --label "org.opencontainers.image.description=TokenOps team plane: derived figures only, aggregated by team, repository and kind of work"
  --label "org.opencontainers.image.source=https://github.com/klarlabs-studio/tokenops"
  --label "org.opencontainers.image.licenses=Apache-2.0"
  --label "org.opencontainers.image.version=${version}"
  --label "org.opencontainers.image.revision=${revision}"
)

# A builder of our own (docker-container driver) for the multi-platform
# build, never made the default. The Dockerfile has no RUN step, so no
# emulation is needed for arm64.
docker buildx create --name "$builder" --driver docker-container >/dev/null

# Smoke test: the amd64 image starts and reports the version.
smoke="tokenops-team-smoke:${version}-$$"
docker buildx build --builder "$builder" --platform linux/amd64 "${labels[@]}" \
  --tag "$smoke" --load "$ctx"
out=$(docker run --rm "$smoke" --version)
docker image rm "$smoke" >/dev/null
echo "$out"
case "$out" in
  *"version ${version} "*) ;;
  *) echo "::error::the image reports '$out', want version ${version}" >&2; exit 1 ;;
esac

tags=(--tag "${image}:${version}")
# A prerelease (1.2.3-rc.1, a snapshot) never moves latest.
case "$version" in
  *-*) ;;
  *) tags+=(--tag "${image}:latest") ;;
esac

if $push; then
  output=(--push)
else
  # Build both platforms to the builder's cache only: CI proves the
  # release's build works without publishing anything.
  output=(--output type=cacheonly)
fi
docker buildx build --builder "$builder" --platform linux/amd64,linux/arm64 \
  "${labels[@]}" "${tags[@]}" "${output[@]}" "$ctx"

if $push; then
  echo "pushed ${tags[*]//--tag /}"
else
  echo "built ${image}:${version} for linux/amd64 and linux/arm64 (not pushed)"
fi
