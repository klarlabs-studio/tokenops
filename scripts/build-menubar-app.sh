#!/usr/bin/env bash
# Builds TokenOps.app, the menu bar app (apps/menubar), for the release.
#
# macOS only: Vitra's native tray needs cgo against AppKit. The app is one
# universal binary (arm64 + x86_64) so both darwin archives carry the same
# bundle. It is signed ad hoc: there is no Developer ID, so it is not
# notarized, and it reaches people through the Homebrew cask, whose
# post-install hook clears the quarantine flag as it does for the CLI. A
# copy downloaded by a browser is blocked by Gatekeeper.
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
  --app-id de.klarlabs.tokenops.menubar --name TokenOps --version "${version#v}" \
  --description "TokenOps in the menu bar: every plan's windows, pace, cost and the coach's findings" \
  --homepage https://github.com/klarlabs-studio/tokenops --license Apache-2.0

app=$(find "$work/pkg" -maxdepth 2 -name '*.app' -type d | head -1)
[ -n "$app" ] || { echo "vitra package produced no .app" >&2; exit 1; }
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"

mkdir -p "$out"
rm -rf "$out/TokenOps.app"
cp -R "$app" "$out/TokenOps.app"
echo "built $out/TokenOps.app ($(lipo -archs "$out/TokenOps.app/Contents/MacOS/"* | head -1))"
