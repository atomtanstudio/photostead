#!/bin/bash
# Makes desktop/build/mac/Photostead.dmg from Photostead.app: the app, and a shortcut to
# Applications to drag it to. When the app is signed with a Developer ID, so is the image.
#
#   desktop/mac/dmg.sh
set -euo pipefail

OUT="$(cd "$(dirname "$0")/../.." && pwd)/desktop/build/mac"
APP="$OUT/Photostead.app"
[ -d "$APP" ] || { echo "no $APP: run desktop/mac/build.sh first" >&2; exit 1; }

rm -f "$OUT/Photostead.dmg"
STAGE=$(mktemp -d "${TMPDIR:-/tmp}/photostead-dmg.XXXXXX")
chmod 755 "$STAGE" # the image's top folder (mktemp makes it owner-only)
ditto "$APP" "$STAGE/Photostead.app"
ln -s /Applications "$STAGE/Applications"
hdiutil create -quiet -volname Photostead -srcfolder "$STAGE" -ov -format UDZO "$OUT/Photostead.dmg"
rm -rf "$STAGE"

SIG=$(codesign -dv --verbose=2 "$APP" 2>&1 || true)
ID=$(printf '%s\n' "$SIG" | awk '/^Authority=Developer ID Application: / { sub(/^Authority=/, ""); print; exit }')
if [ -n "$ID" ]; then codesign --force --timestamp --sign "$ID" "$OUT/Photostead.dmg"; fi
du -sh "$OUT/Photostead.dmg"
