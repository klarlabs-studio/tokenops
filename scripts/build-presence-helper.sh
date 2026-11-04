#!/usr/bin/env bash
# Builds tokenops-presence, the presence helper (apps/presence, ADR 0015),
# for the release: one universal binary (arm64 + x86_64).
#
# macOS only: it is Swift against CryptoKit, LocalAuthentication and the
# Secure Enclave. The CLI and the daemon stay pure Go (CGO_ENABLED=0) and
# only exec it. It is signed with MACOS_SIGN_IDENTITY, the Developer ID the
# release imports (scripts/macos-keychain.sh), with the hardened runtime and
# Apple's timestamp; without one it is signed ad hoc. Either works for the
# key: the Secure Enclave key it holds is bound to this Mac, not to the
# binary's signature, so an upgrade's re-signed helper keeps signing with the
# enrolled key (ADR 0015, spike results).
#
# usage: scripts/build-presence-helper.sh <out-dir>
set -euo pipefail

here="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "${1:?out dir}"
out="$(cd "$1" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

for arch in arm64 x86_64; do
  swiftc -O -target "$arch-apple-macos13" "$here/apps/presence/presence.swift" -o "$work/tokenops-presence-$arch"
done
lipo -create -output "$out/tokenops-presence" "$work/tokenops-presence-arm64" "$work/tokenops-presence-x86_64"
identity="${MACOS_SIGN_IDENTITY:--}"
sign=(--force --sign "$identity" --identifier de.klarlabs.tokenops.presence)
[ "$identity" = "-" ] || sign+=(--options runtime --timestamp)
codesign "${sign[@]}" "$out/tokenops-presence"
codesign --verify --strict "$out/tokenops-presence"
"$out/tokenops-presence" version
echo "built $out/tokenops-presence ($(lipo -archs "$out/tokenops-presence"))"
