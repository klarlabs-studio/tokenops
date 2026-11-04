#!/usr/bin/env bash
# Notarizes TokenOps.app and tokenops-presence, as built into <dir> by
# scripts/build-menubar-app.sh, and staples the ticket to the app. A bare
# binary cannot hold a ticket: Gatekeeper looks the helper's up online the
# first time it runs. Without MACOS_SIGN_IDENTITY (an ad hoc build) it does
# nothing.
#
# env: MACOS_SIGN_IDENTITY, MACOS_NOTARY_KEY (the .p8, base64),
#      MACOS_NOTARY_KEY_ID, MACOS_NOTARY_ISSUER_ID, RUNNER_TEMP.
#
# usage: scripts/macos-notarize.sh <dir>
set -euo pipefail

dir="${1:?dir}"
if [ "${MACOS_SIGN_IDENTITY:--}" = "-" ]; then
  echo "::notice::ad hoc signed: not notarizing"
  exit 0
fi
: "${MACOS_NOTARY_KEY:?no App Store Connect key (MACOS_NOTARY_KEY)}"
: "${MACOS_NOTARY_KEY_ID:?no App Store Connect key ID (MACOS_NOTARY_KEY_ID)}"
: "${MACOS_NOTARY_ISSUER_ID:?no App Store Connect issuer ID (MACOS_NOTARY_ISSUER_ID)}"

work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/notarize.XXXXXX")
trap 'rm -rf "$work"' EXIT
key="$work/AuthKey.p8"
printf '%s' "$MACOS_NOTARY_KEY" | base64 --decode > "$key"

# One submission for both: notarytool takes a zip, and ditto keeps the
# bundle's signature intact where zip would not.
mkdir "$work/submit"
cp -R "$dir/TokenOps.app" "$dir/tokenops-presence" "$work/submit/"
ditto -c -k "$work/submit" "$work/submit.zip"

xcrun notarytool submit "$work/submit.zip" \
  --key "$key" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" \
  --wait --timeout 30m --output-format json > "$work/result.json"
status=$(plutil -extract status raw "$work/result.json")
id=$(plutil -extract id raw "$work/result.json")
if [ "$status" != "Accepted" ]; then
  echo "::error::notarization $id ended $status; Apple's log follows"
  xcrun notarytool log "$id" \
    --key "$key" --key-id "$MACOS_NOTARY_KEY_ID" --issuer "$MACOS_NOTARY_ISSUER_ID" || true
  exit 1
fi
echo "notarization $id accepted"

xcrun stapler staple "$dir/TokenOps.app"
xcrun stapler validate "$dir/TokenOps.app"
spctl --assess --type execute --verbose=2 "$dir/TokenOps.app"
codesign --verify --strict "$dir/tokenops-presence"
