#!/usr/bin/env bash
# Builds TokenOps.app, the menu bar app (apps/menubar), for the release,
# with the presence helper (scripts/build-presence-helper.sh) inside it and
# beside it in <out-dir>.
#
# macOS only: Vitra's native tray needs cgo against AppKit. The app is one
# universal binary (arm64 + x86_64) so both darwin archives carry the same
# bundle. It is signed with MACOS_SIGN_IDENTITY, the Developer ID the
# release imports (scripts/macos-keychain.sh), with the hardened runtime and
# Apple's timestamp, and notarized after (scripts/macos-notarize.sh); without
# one, as in CI, it is signed ad hoc.
#
# usage: scripts/build-menubar-app.sh <version> <out-dir>
set -euo pipefail

version="${1:?version}"
here="$(cd "$(dirname "$0")/.." && pwd)"
# The output directory is resolved now, before the build changes into
# apps/menubar: a relative path would otherwise land there.
mkdir -p "${2:?out dir}"
out="$(cd "$2" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cd "$here/apps/menubar"
for arch in arm64 amd64; do
  clang_arch=$([ "$arch" = amd64 ] && echo x86_64 || echo arm64)
  CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" CC="clang -arch $clang_arch" \
    go build -trimpath -tags vitra_native -ldflags "-s -w" \
    -o "$work/tokenops-menubar-$arch" .
done
lipo -create -output "$work/tokenops-menubar" "$work/tokenops-menubar-arm64" "$work/tokenops-menubar-amd64"

vitra_version=$(go list -m -f '{{.Version}}' go.klarlabs.de/vitra)
go run "go.klarlabs.de/vitra/cmd/vitra@$vitra_version" package \
  --out "$work/pkg" --format app-dir --accessory \
  --bin "$work/tokenops-menubar" \
  --app-id de.klarlabs.tokenops.menubar --name TokenOps \
  --icon "$here/apps/menubar/AppIcon.icns" --version "${version#v}" \
  --description "TokenOps in the menu bar: every plan's windows, pace, cost and the coach's findings" \
  --homepage https://github.com/klarlabs-studio/tokenops --license Apache-2.0

app=$(find "$work/pkg" -maxdepth 2 -name '*.app' -type d | head -1)
[ -n "$app" ] || { echo "vitra package produced no .app" >&2; exit 1; }
# The presence helper (ADR 0015) goes inside the bundle, before the bundle
# is signed, and beside it in the output for the darwin archives' top level.
"$here/scripts/build-presence-helper.sh" "$work/presence"
cp "$work/presence/tokenops-presence" "$app/Contents/MacOS/tokenops-presence"
# The helper is signed already, with its own identifier; the bundle is
# signed around it, not with --deep, which would re-sign the helper under a
# derived identifier. Verifying with --deep then fails on any nested code
# left unsigned.
identity="${MACOS_SIGN_IDENTITY:--}"
sign=(--force --sign "$identity")
[ "$identity" = "-" ] || sign+=(--options runtime --timestamp)
codesign "${sign[@]}" "$app"
codesign --verify --deep --strict "$app"

mkdir -p "$out"
rm -rf "$out/TokenOps.app"
cp -R "$app" "$out/TokenOps.app"
cp "$work/presence/tokenops-presence" "$out/tokenops-presence"
echo "built $out/TokenOps.app:"
for f in "$out/TokenOps.app/Contents/MacOS/"*; do
  echo "  $(basename "$f") ($(lipo -archs "$f"))"
done
