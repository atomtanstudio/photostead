#!/bin/sh
# Makes a store build of the iPhone app: a Release archive in ios/build/, signed by your team
# (Local.xcconfig). With --upload, it also sends it to App Store Connect for TestFlight, which needs
# the app's record there and Xcode signed in to your Apple account (Settings → Accounts).
#
#   ios/scripts/archive.sh            # archive only: checks the store build
#   ios/scripts/archive.sh --upload   # archive, then upload to App Store Connect
#
# The build number is the time in UTC (20261113.2230), so every upload's is higher than the last,
# whatever happens to the git history. The version people see is MARKETING_VERSION in project.yml.
set -eu

cd "$(dirname "$0")/.."
export DEVELOPER_DIR="${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}"

upload=no
case "${1:-}" in
  --upload) upload=yes ;;
  "") ;;
  *) echo "usage: $0 [--upload]" >&2; exit 2 ;;
esac

if [ ! -f Config/Local.xcconfig ] || ! grep -q '^DEVELOPMENT_TEAM *= *[A-Z0-9]' Config/Local.xcconfig; then
  echo "Put your team in ios/Config/Local.xcconfig first (DEVELOPMENT_TEAM = …)." >&2
  exit 1
fi

build=$(date -u +%Y%m%d.%H%M)
version=$(sed -n 's/^ *MARKETING_VERSION: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' project.yml)
archive="build/Photostead-$version-$build.xcarchive"

echo "Archiving Photostead $version ($build)…"
xcodebuild archive -quiet \
  -project Photostead.xcodeproj -scheme Photostead -configuration Release \
  -destination 'generic/platform=iOS' -archivePath "$archive" \
  CURRENT_PROJECT_VERSION="$build"

# What App Review looks at first: the version, the encryption answer, the upload address, the
# privacy manifests, and no debug switches.
app="$archive/Products/Applications/Photostead.app"
ext="$app/Extensions/PhotosteadUploads.appex"
plist() { /usr/libexec/PlistBuddy -c "Print :$2" "$1/Info.plist"; }
[ "$(plist "$app" CFBundleShortVersionString)" = "$version" ]
[ "$(plist "$app" CFBundleVersion)" = "$build" ]
[ "$(plist "$app" ITSAppUsesNonExemptEncryption)" = "false" ]
[ "$(plist "$ext" BackgroundUploadURLBase)" = "https://photostead.com" ]
[ -f "$app/PrivacyInfo.xcprivacy" ] && [ -f "$ext/PrivacyInfo.xcprivacy" ]
if strings "$app/Photostead" | grep -q -e '-demoLibrary' -e '-welcome'; then
  echo "A debug switch is in the Release build." >&2
  exit 1
fi
echo "Archived: ios/$archive"

[ "$upload" = yes ] || exit 0

options=$(mktemp -t photostead-export).plist
cat > "$options" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key><string>app-store-connect</string>
  <key>destination</key><string>upload</string>
  <key>signingStyle</key><string>automatic</string>
  <key>manageAppVersionAndBuildNumber</key><false/>
</dict>
</plist>
EOF
echo "Uploading to App Store Connect…"
xcodebuild -exportArchive -archivePath "$archive" -exportOptionsPlist "$options" \
  -exportPath build/export -allowProvisioningUpdates
rm -f "$options"
echo "Sent. It shows in App Store Connect → TestFlight once Apple has processed it (usually 5 to 30 minutes)."
