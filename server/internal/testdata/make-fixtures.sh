#!/usr/bin/env bash
# Regenerates the small library used by the integration test. Needs ffmpeg, exiftool and vips.
set -euo pipefail
cd "$(dirname "$0")"
rm -rf library && mkdir -p library/copies library/.hidden library/extra
L=library
ffmpeg -loglevel error -f lavfi -i testsrc2=size=1200x800 -frames:v 1 -q:v 3 "$L/photo-tokyo.jpg"
exiftool -q -overwrite_original -DateTimeOriginal="2024:05:01 23:30:00" -OffsetTimeOriginal="+09:00" \
  -Make=Apple -Model="iPhone 15 Pro" -Orientation#=6 \
  -GPSLatitude=35.6812 -GPSLatitudeRef=N -GPSLongitude=139.7671 -GPSLongitudeRef=E "$L/photo-tokyo.jpg"
cp "$L/photo-tokyo.jpg" "$L/copies/photo-tokyo-copy.jpg"          # same bytes: one asset, two files
ffmpeg -loglevel error -f lavfi -i testsrc=size=768x1024 -frames:v 1 "$L/generated.png"   # no metadata at all
ffmpeg -loglevel error -f lavfi -i testsrc=size=640x360:rate=24 -t 2 -pix_fmt yuv420p -c:v libx264 \
  -metadata creation_time=2023-08-12T18:00:00Z "$L/clip.mp4"
vips copy "$L/generated.png" "$L/still.heic" 2>/dev/null || echo "(no HEIC encoder; skipping still.heic)"
printf 'not media\n' > "$L/notes.txt"
printf '<x:xmpmeta xmlns:x="adobe:ns:meta/"/>\n' > "$L/photo-tokyo.jpg.xmp"
cp "$L/photo-tokyo.jpg" "$L/.hidden/private.jpg"                # hidden folders are never indexed
printf 'AppleDouble\n' > "$L/._photo-tokyo.jpg"                  # macOS clutter is never indexed
cp "$L/generated.png" "$L/copies/generated-older.png"           # same bytes, older file date
: > "$L/empty.jpg"                                                # 0 bytes: skipped, never an item
ffmpeg -loglevel error -f lavfi -i testsrc2=size=320x240 -frames:v 1 "$L/extra/only-here.png"   # unique to a folder that gets excluded
# Fixed modification times so the test can check they never change.
find "$L" -exec touch -h -t 202501020304.05 {} +
touch -h -t 202403030303.03 "$L/copies/generated-older.png"     # the oldest copy dates the item
ls -laR "$L" | grep -v '^total'
