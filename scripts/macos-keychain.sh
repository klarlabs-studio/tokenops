#!/usr/bin/env bash
# Imports the release's Developer ID certificate into a throwaway keychain on
# the macOS runner and hands its identity to the later steps as
# MACOS_SIGN_IDENTITY (through $GITHUB_ENV), which the build scripts sign
# with. Without MACOS_SIGN_P12 it does nothing, and they sign ad hoc.
#
# env: MACOS_SIGN_P12 (the .p12, base64), MACOS_SIGN_PASSWORD, RUNNER_TEMP,
#      GITHUB_ENV. Nothing secret is echoed.
#
# usage: scripts/macos-keychain.sh
set -euo pipefail

if [ -z "${MACOS_SIGN_P12:-}" ]; then
  echo "::notice::no Developer ID certificate (MACOS_SIGN_P12): signing ad hoc"
  exit 0
fi
: "${MACOS_SIGN_PASSWORD:?the .p12 has no password (MACOS_SIGN_PASSWORD)}"
tmp="${RUNNER_TEMP:?}"

# Apple's Developer ID intermediate, which links the certificate to Apple's
# root; macOS does not ship it. Pinned, so a changed file fails here.
ca="$tmp/DeveloperIDG2CA.cer"
ca_sha256=f16cd3c54c7f83cea4bf1a3e6a0819c8aaa8e4a1528fd144715f350643d2df3a
curl -fsSL -o "$ca" https://www.apple.com/certificateauthority/DeveloperIDG2CA.cer
echo "$ca_sha256  $ca" | shasum -a 256 --check --status \
  || { echo "::error::Apple's Developer ID G2 intermediate does not match its pinned checksum"; exit 1; }

p12="$tmp/signing.p12"
trap 'rm -f "$p12"' EXIT
printf '%s' "$MACOS_SIGN_P12" | base64 --decode > "$p12"

kc="$tmp/signing.keychain-db"
kc_password=$(openssl rand -hex 24)
security create-keychain -p "$kc_password" "$kc"
security set-keychain-settings -lut 21600 "$kc"
security unlock-keychain -p "$kc_password" "$kc"
security import "$p12" -k "$kc" -f pkcs12 -P "$MACOS_SIGN_PASSWORD" -T /usr/bin/codesign >/dev/null
security import "$ca" -k "$kc" >/dev/null
# Lets codesign use the key without a dialog no runner could answer.
security set-key-partition-list -S apple-tool:,apple: -s -k "$kc_password" "$kc" >/dev/null
# shellcheck disable=SC2046 # the existing search list, one keychain per word
security list-keychains -d user -s "$kc" $(security list-keychains -d user | tr -d '"')

identity=$(security find-identity -v -p codesigning "$kc" | awk -F'"' '/Developer ID Application:/ { print $2; exit }')
[ -n "$identity" ] || { echo "::error::the .p12 holds no valid Developer ID Application identity"; exit 1; }
echo "MACOS_SIGN_IDENTITY=$identity" >> "${GITHUB_ENV:?}"
echo "signing as $identity"
