#!/bin/bash
# Builds Photostead for Windows (64-bit x86) as a folder, zipped:
#
#   Photostead/Photostead.exe       the launcher, with its tray icon
#   Photostead/runtime/bin/         photostead.exe (the server) and the DLLs it uses (libvips…),
#                                   ffmpeg.exe and ffprobe.exe (LGPL), exiftool.exe
#   Photostead/runtime/lib/         libvips' modules and libheif's plugins (HEIC)
#   Photostead/runtime/postgres/    PostgreSQL with pgvector
#   Photostead/runtime/python/      a standalone Python 3.12 with the AI worker
#   Photostead/runtime/web/         the web app
#
# Runs in an MSYS2 UCRT64 shell that also sees Windows's own PATH (GitHub's windows runner, see
# .github/workflows/desktop.yml): MSYS2 gives libvips, PostgreSQL, gcc and ntldd; Go, Node, uv and
# Python come from the runner. Run from the repository root.
set -euo pipefail

PGVECTOR_VERSION=0.8.7
EXIFTOOL_VERSION=13.64
PYTHON_VERSION=3.12

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
B="$ROOT/desktop/build"
OUT="$B/windows/Photostead"
RT="$OUT/runtime"
SRC="$B/src"
UCRT=/ucrt64
rm -rf "$B/windows"
mkdir -p "$RT/bin" "$RT/lib" "$RT/postgres/bin" "$RT/postgres/lib" "$RT/postgres/share" "$SRC"

fetch() { # url [name] → file in $SRC
  local f="$SRC/${2:-$(basename "$1")}"
  [ -s "$f" ] || curl -fsSL --retry 3 -o "$f" "$1"
  echo "$f"
}

# dlls DEST FILE...: copies the MSYS2 DLLs the files need (and theirs) into DEST.
dlls() {
  local dest=$1; shift
  for f in "$@"; do
    # (A file may need nothing from MSYS2: then grep finds nothing, which isn't a failure.)
    { ntldd -R "$f" 2>/dev/null || true; } | { grep -i "ucrt64" || true; } | sed -E 's/^[[:space:]]*([^ ]+) => (.+) \(0x[0-9a-fA-F]+\)$/\2/' | while IFS= read -r dll; do
      dll=$(cygpath -u "$dll")
      [ -f "$dest/$(basename "$dll")" ] || cp "$dll" "$dest/"
    done
  done
}

echo "== the server"
(cd "$ROOT/server" && CGO_ENABLED=1 CC=gcc go build -trimpath -ldflags="-s -w" -o "$RT/bin/photostead.exe" ./cmd/photostead)
dlls "$RT/bin" "$RT/bin/photostead.exe"
# libvips' modules (HEIC may be one) and libheif's decoder plugins, with what they need.
for m in "$UCRT"/lib/vips-modules-*; do
  [ -d "$m" ] || continue
  mkdir -p "$RT/lib/$(basename "$m")"
  for f in "$m"/vips-heif.dll; do [ -f "$f" ] && cp "$f" "$RT/lib/$(basename "$m")/" && dlls "$RT/bin" "$f"; done
done
if [ -d "$UCRT/lib/libheif/plugins" ]; then
  mkdir -p "$RT/lib/libheif"
  cp "$UCRT"/lib/libheif/plugins/*.dll "$RT/lib/libheif/"
  dlls "$RT/bin" "$RT"/lib/libheif/*.dll
fi

echo "== PostgreSQL and pgvector"
if [ ! -f "$UCRT/lib/postgresql/vector.dll" ]; then
  if ! pacman -S --noconfirm --needed mingw-w64-ucrt-x86_64-pgvector; then
    V=$PGVECTOR_VERSION
    tar -xzf "$(fetch "https://github.com/pgvector/pgvector/archive/refs/tags/v$V.tar.gz" "pgvector-$V.tar.gz")" -C "$SRC"
    (cd "$SRC/pgvector-$V" && make PG_CONFIG="$UCRT/bin/pg_config" OPTFLAGS="" && make PG_CONFIG="$UCRT/bin/pg_config" install)
  fi
fi
for p in postgres pg_ctl initdb psql pg_dump pg_restore pg_isready; do cp "$UCRT/bin/$p.exe" "$RT/postgres/bin/"; done
dlls "$RT/postgres/bin" "$RT"/postgres/bin/*.exe
cp -R "$UCRT/lib/postgresql" "$RT/postgres/lib/"
dlls "$RT/postgres/bin" "$RT"/postgres/lib/postgresql/*.dll
cp -R "$UCRT/share/postgresql" "$RT/postgres/share/"
rm -rf "$RT/postgres/share/postgresql/doc"
"$RT/postgres/bin/postgres.exe" --version

echo "== ffmpeg (LGPL)"
URL=$(curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN:-}" https://api.github.com/repos/BtbN/FFmpeg-Builds/releases/latest |
  grep -oE '"browser_download_url": "[^"]+win64-lgpl-[0-9]+\.[0-9]+\.zip"' | grep -v shared | sort -V | tail -1 | cut -d'"' -f4)
[ -n "$URL" ] || { echo "no LGPL ffmpeg build found" >&2; exit 1; }
unzip -q -o "$(fetch "$URL" ffmpeg-win64-lgpl.zip)" -d "$SRC/ffmpeg"
cp "$SRC"/ffmpeg/*/bin/ffmpeg.exe "$SRC"/ffmpeg/*/bin/ffprobe.exe "$RT/bin/"
"$RT/bin/ffmpeg.exe" -hide_banner -version | head -1

echo "== exiftool"
# (exiftool.org's own download links go to SourceForge.)
unzip -q -o "$(fetch "https://sourceforge.net/projects/exiftool/files/exiftool-${EXIFTOOL_VERSION}_64.zip/download" "exiftool-${EXIFTOOL_VERSION}_64.zip")" -d "$SRC/exiftool"
E=$(find "$SRC/exiftool" -name "exiftool(-k).exe" | head -1)
cp "$E" "$RT/bin/exiftool.exe"
cp -R "$(dirname "$E")/exiftool_files" "$RT/bin/"
"$RT/bin/exiftool.exe" -ver

echo "== the AI worker"
uv python install "$PYTHON_VERSION" >/dev/null
PY=$(cygpath -u "$(uv python find "$PYTHON_VERSION")")
cp -R "$(dirname "$PY")" "$RT/python"
rm -f "$RT/python/Lib/EXTERNALLY-MANAGED"
# (Windows programs get Windows paths: MSYS doesn't translate one with "[cpu]" on the end.)
uv pip install --quiet --python "$(cygpath -m "$RT/python/python.exe")" "$(cygpath -m "$ROOT/worker")[cpu]"
find "$RT/python" -name "__pycache__" -type d -prune -exec rm -rf {} +
rm -rf "$RT/python/Lib/test" "$RT/python/Lib/idlelib" "$RT/python/Lib/tkinter" "$RT/python/tcl"

echo "== the web app"
(cd "$ROOT/web" && npm ci --no-audit --no-fund --silent && npm run build --silent)
cp -R "$ROOT/web/dist" "$RT/web"

echo "== the launcher"
(cd "$ROOT/desktop" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-H windowsgui -s -w" -o "$OUT/Photostead.exe" .)

echo "== the zip"
(cd "$B/windows" && zip -q -r Photostead-windows.zip Photostead)
du -sh "$OUT" "$B/windows/Photostead-windows.zip"
echo "Built: desktop/build/windows/Photostead"
