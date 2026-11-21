#!/bin/bash
# Builds Photostead.app for Apple silicon Macs (macOS 14 or later): the launcher with its menu-bar
# icon, and in Contents/Resources/runtime everything Photostead runs, so nothing is needed from
# Homebrew, Docker or anywhere else on the Mac that runs it:
#
#   runtime/bin/       photostead (the server), ffmpeg, ffprobe (LGPL, with VideoToolbox), exiftool
#   runtime/lib/       the libraries the server uses (libvips and its HEIC module, from Homebrew here)
#   runtime/postgres/  PostgreSQL with pgvector, built here
#   runtime/python/    a standalone Python 3.12 with the AI worker and its packages
#   runtime/web/       the web app
#
#   desktop/mac/build.sh            builds desktop/build/mac/Photostead.app (and a .dmg)
#
# Needs, on the building Mac: Xcode's command-line tools, Go, Node, uv, and Homebrew's vips and zstd.
# Postgres and ffmpeg are built from source once and kept in desktop/build. With a Developer ID
# Application certificate in the keychain the app is signed for any Mac; desktop/mac/notarize.sh
# then has Apple notarize it. Without one it's signed ad hoc and runs only on this Mac.
set -euo pipefail

PG_VERSION=18.7
PGVECTOR_VERSION=0.8.7
FFMPEG_VERSION=8.1.3
EXIFTOOL_VERSION=13.64
PYTHON_VERSION=3.12

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
B="$ROOT/desktop/build"
SRC="$B/src"
OUT="$B/mac"
APP="$OUT/Photostead.app"
RT="$APP/Contents/Resources/runtime"
JOBS=$(sysctl -n hw.ncpu)
export DEVELOPER_DIR="${DEVELOPER_DIR:-/Applications/Xcode.app/Contents/Developer}"
export MACOSX_DEPLOYMENT_TARGET=14.0
mkdir -p "$SRC"

fetch() { # url [name] → file in $SRC
  local f="$SRC/${2:-$(basename "$1")}"
  [ -s "$f" ] || curl -fsSL --retry 3 -o "$f" "$1"
  echo "$f"
}

# PostgreSQL with pgvector, relocatable (it finds share/ and lib/ beside bin/), with zstd for the
# nightly database copies.
PG="$B/postgres-$PG_VERSION"
# (Its prefix has "postgres" in it, so extensions go straight in lib/ and share/, not a postgresql/ under them.)
if [ ! -x "$PG/bin/postgres" ] || [ ! -f "$PG/lib/vector.dylib" ]; then
  echo "== PostgreSQL $PG_VERSION"
  tar -xjf "$(fetch "https://ftp.postgresql.org/pub/source/v$PG_VERSION/postgresql-$PG_VERSION.tar.bz2")" -C "$SRC"
  ZSTD=$(brew --prefix zstd)
  (cd "$SRC/postgresql-$PG_VERSION" &&
    ./configure --prefix="$PG" --without-icu --without-readline --with-zstd \
      ZSTD_CFLAGS="-I$ZSTD/include" ZSTD_LIBS="-L$ZSTD/lib -lzstd" >/dev/null &&
    make -s -j"$JOBS" world-bin >/dev/null && make -s install-world-bin >/dev/null)
  echo "== pgvector $PGVECTOR_VERSION"
  tar -xzf "$(fetch "https://github.com/pgvector/pgvector/archive/refs/tags/v$PGVECTOR_VERSION.tar.gz")" -C "$SRC"
  (cd "$SRC/pgvector-$PGVECTOR_VERSION" && make -s PG_CONFIG="$PG/bin/pg_config" OPTFLAGS="" >/dev/null &&
    make -s PG_CONFIG="$PG/bin/pg_config" install >/dev/null)
fi

# ffmpeg and ffprobe: LGPL (no libx264), Apple's VideoToolbox for video copies, nothing from Homebrew.
FF="$B/ffmpeg-$FFMPEG_VERSION"
if [ ! -x "$FF/bin/ffmpeg" ]; then
  echo "== ffmpeg $FFMPEG_VERSION"
  tar -xJf "$(fetch "https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz")" -C "$SRC"
  (cd "$SRC/ffmpeg-$FFMPEG_VERSION" &&
    ./configure --prefix="$FF" --disable-doc --disable-ffplay --disable-debug --disable-autodetect \
      --enable-videotoolbox --enable-audiotoolbox --enable-zlib --enable-bzlib --enable-iconv \
      --extra-libs=-liconv >/dev/null &&
    make -s -j"$JOBS" >/dev/null && make -s install >/dev/null)
fi

echo "== the app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$RT/bin" "$RT/lib" "$RT/postgres"

# PostgreSQL: the programs Photostead and the launcher use, its libraries and its share/.
mkdir -p "$RT/postgres/bin" "$RT/postgres/lib"
for p in postgres pg_ctl initdb psql pg_dump pg_restore pg_isready; do cp "$PG/bin/$p" "$RT/postgres/bin/"; done
cp "$PG"/lib/*.dylib "$RT/postgres/lib/" # libpq, extensions (pgvector), encoding conversions
cp -R "$PG/share" "$RT/postgres/"
rm -rf "$RT/postgres/share/doc" "$RT/postgres/share/man"
python3 "$ROOT/desktop/mac/relink.py" "$RT/postgres/lib" "$RT"/postgres/bin/* "$RT"/postgres/lib/*.dylib

cp "$FF/bin/ffmpeg" "$FF/bin/ffprobe" "$RT/bin/"

# exiftool: Perl, run by macOS's own /usr/bin/perl; it finds its lib/ beside it.
# (exiftool.org's own download links go to SourceForge.)
tar -xzf "$(fetch "https://sourceforge.net/projects/exiftool/files/Image-ExifTool-$EXIFTOOL_VERSION.tar.gz/download" "Image-ExifTool-$EXIFTOOL_VERSION.tar.gz")" -C "$SRC"
cp "$SRC/Image-ExifTool-$EXIFTOOL_VERSION/exiftool" "$RT/bin/"
cp -R "$SRC/Image-ExifTool-$EXIFTOOL_VERSION/lib" "$RT/bin/lib"

# The server, linked to Homebrew's libvips here, then made to use the copies in runtime/lib.
echo "== the server"
(cd "$ROOT/server" && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "$RT/bin/photostead" ./cmd/photostead)
VIPS=$(brew --prefix vips)
MODS=$(basename "$(ls -d "$VIPS"/lib/vips-modules-*)")
mkdir -p "$RT/lib/$MODS"
cp "$VIPS/lib/$MODS/vips-heif.dylib" "$RT/lib/$MODS/" # HEIC: iPhone photos
python3 "$ROOT/desktop/mac/relink.py" "$RT/lib" "$RT/bin/photostead" "$RT/lib/$MODS/vips-heif.dylib"

# The AI worker: a standalone Python with the worker and its packages installed into it.
echo "== the AI worker"
PY=$(uv python find "$PYTHON_VERSION")
PYHOME=$(cd "$(dirname "$PY")/.." && pwd -P)
cp -R "$PYHOME" "$RT/python"
rm -f "$RT"/python/lib/python3.*/EXTERNALLY-MANAGED
uv pip install --quiet --python "$RT/python/bin/python3" "$ROOT/worker[cpu]"
find "$RT/python" -name "__pycache__" -type d -prune -exec rm -rf {} +
rm -rf "$RT"/python/lib/python3.*/test "$RT"/python/lib/python3.*/idlelib "$RT"/python/lib/python3.*/tkinter "$RT"/python/lib/tcl* "$RT"/python/lib/tk*

# The web app.
echo "== the web app"
(cd "$ROOT/web" && npm run build --silent >/dev/null)
cp -R "$ROOT/web/dist" "$RT/web"

# The launcher and the bundle around it.
echo "== the launcher"
(cd "$ROOT/desktop" && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o "$APP/Contents/MacOS/Photostead" .)
cp "$ROOT/desktop/assets/Photostead.icns" "$APP/Contents/Resources/"
# The version: from the release tag on this commit (v1.0.0 → 1.0.0), otherwise 0.0.0 and the commit.
TAG=$(cd "$ROOT" && git describe --tags --exact-match --match 'v[0-9]*' 2>/dev/null || true)
if [ -n "$TAG" ]; then SHORT=${TAG#v}; VERSION=$SHORT; else SHORT=0.0.0; VERSION=$(cd "$ROOT" && git rev-parse --short HEAD 2>/dev/null || echo dev); fi
echo "   version $SHORT ($VERSION)"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Photostead</string>
  <key>CFBundleDisplayName</key><string>Photostead</string>
  <key>CFBundleIdentifier</key><string>studio.atomtan.photostead.desktop</string>
  <key>CFBundleExecutable</key><string>Photostead</string>
  <key>CFBundleIconFile</key><string>Photostead</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$SHORT</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>LSUIElement</key><true/>
  <key>LSArchitecturePriority</key><array><string>arm64</string></array>
  <key>NSHumanReadableCopyright</key><string>Photostead</string>
</dict>
</plist>
PLIST

# Nothing may still point at Homebrew or the build folders.
echo "== checking it stands alone"
bad=0
while IFS= read -r f; do
  # (Headers end in ":", one per architecture in a universal file, and name the file itself.)
  if otool -L "$f" 2>/dev/null | grep -v ':$' | grep -qE "/opt/homebrew|/usr/local/|$B"; then
    echo "  still outside: $f"; otool -L "$f" | grep -v ':$' | grep -E "/opt/homebrew|/usr/local/|$B"; bad=1
  fi
done < <(find "$APP" -type f \( -perm -u+x -o -name "*.dylib" -o -name "*.so" \) -exec sh -c 'file -b "$1" | grep -q Mach-O && echo "$1"' _ {} \;)
[ $bad = 0 ] || { echo "Some files still use libraries from outside the app." >&2; exit 1; }
# And every library the server and Postgres name through @rpath is in the app.
for f in "$RT"/bin/* "$RT"/lib/*.dylib "$RT"/lib/vips-modules-*/*.dylib "$RT"/postgres/bin/* "$RT"/postgres/lib/*.dylib; do
  file -b "$f" | grep -q Mach-O || continue
  for ref in $(otool -L "$f" | grep -v ':$' | awk '{print $1}' | grep '^@rpath/'); do
    name=${ref#@rpath/}
    [ -f "$RT/lib/$name" ] || [ -f "$RT/postgres/lib/$name" ] || { echo "  missing: $name (for $f)"; bad=1; }
  done
done
[ $bad = 0 ] || { echo "Some libraries the app needs aren't in it." >&2; exit 1; }

# Signed with the Developer ID in the keychain when there is one (PHOTOSTEAD_SIGN_ID picks another,
# "-" forces ad hoc), with the hardened runtime and a secure timestamp, so it can be notarized
# (desktop/mac/notarize.sh). Without one, ad hoc: it then runs only on this Mac. Every piece of code
# first, then the app. The worker's Python gets python.org's two exceptions (python.entitlements).
SIGN_ID=${PHOTOSTEAD_SIGN_ID:-$(security find-identity -v -p codesigning | sed -nE 's/.*"(Developer ID Application: [^"]+)".*/\1/p' | head -1)}
if [ -n "$SIGN_ID" ] && [ "$SIGN_ID" != - ]; then
  echo "== signing ($SIGN_ID)"
  SIGN=(--force --options runtime --timestamp --sign "$SIGN_ID")
else
  echo "== signing (ad hoc: it runs only on this Mac)"
  SIGN=(--force --sign -)
fi
n=0
while IFS= read -r -d '' f; do
  file -b "$f" | grep -q Mach-O || continue
  case "$f" in
    "$RT/python/bin/python3"*) codesign "${SIGN[@]}" --entitlements "$ROOT/desktop/mac/python.entitlements" "$f" ;;
    *) codesign "${SIGN[@]}" "$f" ;;
  esac
  n=$((n + 1))
done < <(find "$APP/Contents" -type f \( -perm -u+x -o -name "*.dylib" -o -name "*.so" \) -print0)
codesign "${SIGN[@]}" "$APP"
codesign --verify --deep --strict "$APP"
echo "   $n pieces of code and the app"

echo "== the disk image (the app, and a shortcut to Applications to drag it to)"
"$ROOT/desktop/mac/dmg.sh"
du -sh "$APP"
echo "Built: desktop/build/mac/Photostead.app"
