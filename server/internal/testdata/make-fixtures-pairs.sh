#!/usr/bin/env bash
# Regenerates the small library for the pairing and move-plan test. Needs ffmpeg, exiftool and vips.
set -euo pipefail
cd "$(dirname "$0")"
rm -rf pairs && mkdir -p pairs/phone pairs/cam pairs/ai pairs/a pairs/b pairs/dated/2024-03-05 pairs/later/2026-01-01
P=pairs
# A Live Photo exported without Apple's identifier: same name, Apple-made still, a short clip.
ffmpeg -loglevel error -f lavfi -i testsrc2=size=640x480 -frames:v 1 -q:v 3 "$P/phone/IMG_0001.JPG"
exiftool -q -overwrite_original -Make=Apple -Model="iPhone 13" -DateTimeOriginal="2022:01:02 10:00:00" -OffsetTimeOriginal="-08:00" "$P/phone/IMG_0001.JPG"
ffmpeg -loglevel error -f lavfi -i testsrc=size=480x360:rate=30 -t 1.5 -pix_fmt yuv420p -c:v libx264 -metadata creation_time=2022-01-02T18:00:00Z "$P/phone/IMG_0001.MOV"
# RAW+JPEG (the "RAW" is TIFF data with a .DNG name; pairing goes by name).
ffmpeg -loglevel error -f lavfi -i smptebars=size=300x200 -frames:v 1 -q:v 3 "$P/cam/DSC_0001.JPG"
exiftool -q -overwrite_original -Make=Nikon -DateTimeOriginal="2021:06:06 09:00:00" "$P/cam/DSC_0001.JPG"
vips copy "$P/cam/DSC_0001.JPG" "$P/cam/DSC_0001.tif" && mv "$P/cam/DSC_0001.tif" "$P/cam/DSC_0001.DNG"
# An AI tool's image and video with the same name: not a Live Photo.
ffmpeg -loglevel error -f lavfi -i mandelbrot=size=512x512 -frames:v 1 "$P/ai/render_00001.png"
ffmpeg -loglevel error -f lavfi -i mandelbrot=size=512x512:rate=24 -t 2 -pix_fmt yuv420p -c:v libx264 "$P/ai/render_00001.mp4"
# Two different photos with the same name on the same day, and a sidecar.
ffmpeg -loglevel error -f lavfi -i testsrc2=size=400x300 -frames:v 1 -q:v 3 "$P/a/beach.jpg"
ffmpeg -loglevel error -f lavfi -i rgbtestsrc=size=400x300 -frames:v 1 -q:v 3 "$P/b/Beach.JPG"
exiftool -q -overwrite_original -DateTimeOriginal="2024:05:01 09:00:00" "$P/a/beach.jpg"
exiftool -q -overwrite_original -DateTimeOriginal="2024:05:01 15:00:00" "$P/b/Beach.JPG"
printf '<x:xmpmeta xmlns:x="adobe:ns:meta/"/>\n' > "$P/a/beach.jpg.xmp"
# No date inside: the folder's date is earlier than the file time, so it wins; a later one doesn't.
ffmpeg -loglevel error -f lavfi -i testsrc=size=320x240 -frames:v 1 "$P/dated/2024-03-05/shot.png"
ffmpeg -loglevel error -f lavfi -i rgbtestsrc=size=320x240 -frames:v 1 "$P/later/2026-01-01/late.png"
find "$P" -exec touch -h -t 202501020304.05 {} +
find "$P" -type f | sort
