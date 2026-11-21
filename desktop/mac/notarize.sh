#!/bin/bash
# Has Apple notarize the Developer ID–signed Photostead.app, staples the ticket to it, then makes,
# notarizes and staples the disk image, so a Mac that downloads Photostead opens it without a
# warning, even offline. Run desktop/mac/build.sh first, with the Developer ID certificate.
#
#   desktop/mac/notarize.sh [PROFILE]
#
# PROFILE is a notarytool keychain profile (photostead-notary unless named), made once with:
#   xcrun notarytool store-credentials photostead-notary --apple-id <Apple ID> --team-id <team>
set -euo pipefail

PROFILE=${1:-photostead-notary}
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/desktop/build/mac"
APP="$OUT/Photostead.app"
export DEVELOPER_DIR="${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/photostead-notary.XXXXXX")
trap 'rm -rf "$WORK"' EXIT

SIG=$(codesign -dv --verbose=2 "$APP" 2>&1 || true) # (read whole: grep -q in a pipe trips pipefail)
case "$SIG" in *"Authority=Developer ID Application"*) ;;
  *) echo "Photostead.app isn't signed with a Developer ID: run desktop/mac/build.sh with the certificate in the keychain" >&2; exit 1 ;;
esac

submit() { # file → waits for Apple's answer; the log when it isn't Accepted
  xcrun notarytool submit "$1" --keychain-profile "$PROFILE" --wait --output-format json >"$WORK/result.json" || true
  local status id
  status=$(/usr/bin/plutil -extract status raw -o - "$WORK/result.json" 2>/dev/null || echo "?")
  id=$(/usr/bin/plutil -extract id raw -o - "$WORK/result.json" 2>/dev/null || echo "")
  echo "   $(basename "$1"): $status${id:+ ($id)}"
  if [ "$status" != Accepted ]; then
    cat "$WORK/result.json" >&2
    [ -n "$id" ] && xcrun notarytool log "$id" --keychain-profile "$PROFILE" >&2
    exit 1
  fi
}

echo "== the app"
ditto -c -k --keepParent "$APP" "$WORK/Photostead.zip"
submit "$WORK/Photostead.zip"
xcrun stapler staple -q "$APP"

echo "== the disk image"
"$ROOT/desktop/mac/dmg.sh"
submit "$OUT/Photostead.dmg"
xcrun stapler staple -q "$OUT/Photostead.dmg"

echo "== what Gatekeeper says"
spctl --assess --type execute -vv "$APP" 2>&1 | sed 's/^/   /'
spctl --assess --type open --context context:primary-signature -vv "$OUT/Photostead.dmg" 2>&1 | sed 's/^/   /'
xcrun stapler validate -q "$OUT/Photostead.dmg" && echo "   the ticket is stapled to the disk image"
