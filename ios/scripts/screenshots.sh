#!/bin/sh
# Takes the App Store's screenshots (6.9" and 6.3" iPhones) from a real Photostead library:
#
#   PAIR_ADDRESS=demo.photostead.com PAIR_CODE=ABCD-EFGH ios/scripts/screenshots.sh
#
# PAIR_CODE is best a code that lasts (photostead pairing add -days 1 -devices 4): each simulator
# pairs with it. They go in ios/build/screenshots/<size>/. SCREENSHOTS_PHOTO picks the item the
# viewer shows (0, the newest, unless set); SCREENSHOTS_ALBUM names an album to end on (none unless set),
# with its Activity when it's shared.
#
# The simulators are made for this ("Photostead shots 6.9", "… 6.3") with nothing in their Photos
# library, so backing up sends nothing to the server and the library shows only the server's
# pictures. The status bar says 9:41, full battery, full signal.
set -eu

cd "$(dirname "$0")/.."
export DEVELOPER_DIR="${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}"
: "${PAIR_ADDRESS:?set PAIR_ADDRESS (the server, e.g. demo.photostead.com)}"
: "${PAIR_CODE:?set PAIR_CODE (a pairing code; one that lasts does both simulators)}"

out="$PWD/build/screenshots"
dd="$PWD/build/screenshots-dd"

# A simulator made for screenshots, its Photos library emptied once (iOS fills it on first boot).
simulator() { # name, device type
  udid=$(xcrun simctl list devices -j | /usr/bin/python3 -c '
import json, sys
name = sys.argv[1]
for devs in json.load(sys.stdin)["devices"].values():
    for d in devs:
        if d["name"] == name and d.get("isAvailable"):
            print(d["udid"]); sys.exit()' "$1")
  if [ -z "$udid" ]; then
    udid=$(xcrun simctl create "$1" "$2")
    xcrun simctl boot "$udid"
    xcrun simctl bootstatus "$udid" -b >/dev/null
    sleep 20 # Photos adds its sample pictures
    xcrun simctl shutdown "$udid"
    rm -rf "$HOME/Library/Developer/CoreSimulator/Devices/$udid/data/Media/DCIM" \
           "$HOME/Library/Developer/CoreSimulator/Devices/$udid/data/Media/PhotoData"
  fi
  echo "$udid"
}

echo "Building for testing…"
xcodebuild build-for-testing -quiet -project Photostead.xcodeproj -scheme Photostead \
  -destination 'generic/platform=iOS Simulator' -derivedDataPath "$dd"

for size in ${SIZES:-6.9 6.3}; do # SIZES=6.3 for one
  case $size in
    6.9) type="iPhone 18 Pro Max" ;;
    6.3) type="iPhone 18 Pro" ;;
  esac
  udid=$(simulator "Photostead shots $size" "$type")
  xcrun simctl boot "$udid" 2>/dev/null || true
  xcrun simctl bootstatus "$udid" -b >/dev/null
  xcrun simctl ui "$udid" appearance light
  xcrun simctl status_bar "$udid" override --time 9:41 --dataNetwork wifi --wifiMode active --wifiBars 3 \
    --cellularMode active --cellularBars 4 --batteryState charged --batteryLevel 100
  xcrun simctl privacy "$udid" reset photos studio.atomtan.photostead 2>/dev/null || true # asked again, and allowed
  rm -rf "${out:?}/$size"
  echo "Taking the $size\" screenshots on $type…"
  failed=no
  TEST_RUNNER_SCREENSHOTS_DIR="$out/$size" TEST_RUNNER_PAIR_ADDRESS="$PAIR_ADDRESS" \
  TEST_RUNNER_PAIR_CODE_SCREENSHOTS="$PAIR_CODE" TEST_RUNNER_SCREENSHOTS_PHOTO="${SCREENSHOTS_PHOTO:-0}" \
  TEST_RUNNER_SCREENSHOTS_ALBUM="${SCREENSHOTS_ALBUM:-}" \
    xcodebuild test-without-building -project Photostead.xcodeproj -scheme Photostead \
      -derivedDataPath "$dd" -destination "id=$udid" \
      -only-testing:PhotosteadUITests/StoreScreenshotTests || failed=yes
  xcrun simctl status_bar "$udid" clear
  xcrun simctl shutdown "$udid"
  if [ $failed = yes ] || ! ls "$out/$size"/*.png >/dev/null 2>&1; then
    echo "The $size\" screenshots didn't all come out on $type (see the test's log above)." >&2
    exit 1
  fi
  for f in "$out/$size"/*.png; do
    echo "  $(basename "$f"): $(sips -g pixelWidth -g pixelHeight "$f" | awk '/pixel/ {printf "%s ", $2}')"
  done
done
echo "Screenshots: ios/build/screenshots/"
