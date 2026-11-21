#!/bin/bash
# The desktop app end to end, without its icon: starts it, makes the first account, uploads a JPEG,
# an iPhone HEIC, a video and an HEVC video (as iPhones make them), waits for their thumbnails and
# for the HEVC one's playable copy (the system's own encoder), makes a database copy (pg_dump, zstd),
# checks the AI worker's Python has its packages, stops it, starts it again and stops it again.
# The same script for the Mac (bash) and Windows (Git Bash on the CI runner).
#
#   desktop/test/smoke.sh LAUNCHER RUNTIME [PORT]
#
#   SMOKE_AI=1        also wait for the AI worker to look at the pictures (search finds them by
#                     meaning); it downloads its models (2.4 GB) first unless SMOKE_MODELS says where
#                     they are already
#   SMOKE_MODELS=DIR  models to use instead of downloading them
#
# Everything goes in a new folder under the system's temporary one, removed when it passes.
set -uo pipefail

LAUNCHER=$1
RUNTIME=$2
PORT=${3:-4870}
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FIX="$ROOT/server/internal/testdata/library"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/photostead-smoke.XXXXXX")
DATA="$WORK/data"
LIB="$WORK/library"
B="http://127.0.0.1:$PORT"
JAR="$WORK/cookies"
PY=$(command -v python3 || command -v python)
mkdir -p "$DATA" "$LIB"
if [ -n "${SMOKE_MODELS:-}" ]; then mkdir -p "$DATA/server" && ln -s "$SMOKE_MODELS" "$DATA/server/models"; fi

say() { echo "== $*"; }
die() {
  echo "FAILED: $*" >&2
  for f in launcher server postgres; do
    [ -f "$DATA/logs/$f.log" ] && { echo "--- $f.log (end)" >&2; tail -40 "$DATA/logs/$f.log" >&2; }
  done
  [ -f "$DATA/logs/server.log" ] && { echo "--- server.log: copies and errors" >&2
    grep -iE "copy|copies|transcode|ffmpeg|error|fail" "$DATA/logs/server.log" | grep -v "Producer job" | tail -25 >&2; }
  if [ -n "${PID:-}" ] && kill -0 "$PID" 2>/dev/null; then # stop it cleanly, Postgres too
    touch "$DATA/stop"
    for _ in $(seq 1 60); do kill -0 "$PID" 2>/dev/null || break; sleep 1; done
    kill "$PID" 2>/dev/null
  fi
  exit 1
}
json() { "$PY" -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }

start() {
  "$LAUNCHER" -headless -data "$DATA" -library "$LIB" -port "$PORT" -runtime "$RUNTIME" >"$WORK/launcher.out" 2>&1 &
  PID=$!
  for _ in $(seq 1 240); do
    curl -sf -o /dev/null "$B/api/v1/setup" && return 0
    kill -0 "$PID" 2>/dev/null || die "the launcher exited: $(tail -5 "$WORK/launcher.out")"
    sleep 1
  done
  die "Photostead didn't answer in 4 minutes"
}

stop() {
  touch "$DATA/stop"
  for _ in $(seq 1 120); do
    kill -0 "$PID" 2>/dev/null || break
    sleep 1
  done
  kill -0 "$PID" 2>/dev/null && die "the launcher didn't stop in 2 minutes"
  [ -f "$DATA/postgres/postmaster.pid" ] && die "Postgres was left running"
  return 0
}

say "starting"
start
[ "$(curl -s "$B/api/v1/setup" | json 'd["needed"]')" = "True" ] || die "a new install should ask for setup"

say "the first account"
PW="smoke-test-$(date +%s)-password"
curl -sf -o /dev/null -H 'Content-Type: application/json' -H "Origin: $B" \
  -d "{\"displayName\":\"Tester\",\"email\":\"tester@example.com\",\"password\":\"$PW\",\"timeZone\":\"UTC\"}" "$B/api/v1/setup" || die "setup"
curl -sf -o /dev/null -c "$JAR" -H 'Content-Type: application/json' -H "Origin: $B" \
  -d "{\"email\":\"tester@example.com\",\"password\":\"$PW\"}" "$B/api/v1/auth/login" || die "sign in"

say "uploading a JPEG, a HEIC, a video and an HEVC video"
cp "$ROOT/desktop/test/hevc.mov" "$WORK/"
for f in photo-tokyo.jpg still.heic clip.mp4 "$WORK/hevc.mov"; do
  d="$FIX"
  case "$f" in */*) d=$(dirname "$f"); f=$(basename "$f") ;; esac
  # From the fixture's folder, by its name: on Windows a path after "@" isn't turned into a Windows one.
  code=$(cd "$d" && curl -s -o "$WORK/up.json" -w '%{http_code}' -b "$JAR" -H "Origin: $B" -F "file=@$f" "$B/api/v1/uploads")
  [ "$code" = 202 ] || [ "$code" = 200 ] || die "uploading $f: $code $(cat "$WORK/up.json")"
done

say "waiting for their thumbnails"
ok=0
for _ in $(seq 1 120); do
  months=$(curl -s -b "$JAR" "$B/api/v1/timeline/buckets" | json '" ".join(b["month"] for b in d.get("buckets") or [])' 2>/dev/null)
  ids=""
  for m in $months; do
    ids="$ids $(curl -s -b "$JAR" "$B/api/v1/timeline?month=$m" | json '" ".join(a["id"] for a in d.get("assets") or [])' 2>/dev/null)"
  done
  n=0; good=0
  for id in $ids; do
    n=$((n + 1))
    t=$(curl -s -o "$WORK/thumb" -w '%{http_code} %{content_type}' -b "$JAR" "$B/api/v1/assets/$id/thumbnail")
    case "$t" in "200 image/"*) [ -s "$WORK/thumb" ] && good=$((good + 1)) ;; esac
  done
  if [ "$n" -ge 4 ] && [ "$good" -ge 4 ]; then ok=1; break; fi
  sleep 2
done
[ $ok = 1 ] || die "thumbnails: $good of $n items (4 uploaded)"
echo "   4 items, 4 thumbnails"

say "a playable copy of the HEVC video"
echo "   encoders: $("$RUNTIME/bin/ffmpeg" -hide_banner -encoders 2>/dev/null | awk '{print $2}' | grep -E '^(libx264|h264_videotoolbox|h264_mf|libopenh264|h264_nvenc|h264_qsv|h264_amf)$' | tr '\n' ' ')"
# The server looks at new videos every 10 minutes (and once at start, before these were uploaded),
# and on a first run the worker downloads its models before it takes the copy: 20 minutes.
copied=0
for _ in $(seq 1 240); do
  copied=0
  for m in $months; do
    c=$(curl -s -b "$JAR" "$B/api/v1/timeline?month=$m" | json 'sum(1 for a in d.get("assets") or [] if a.get("copy"))' 2>/dev/null || echo 0)
    copied=$((copied + ${c:-0}))
  done
  [ "$copied" -ge 1 ] && break
  sleep 5
done
[ "$copied" -ge 1 ] || die "no playable copy of the HEVC video in 20 minutes (see server.log: transcode)"
echo "   made"

say "a database copy (pg_dump, zstd)"
curl -sf -o /dev/null -b "$JAR" -H "Origin: $B" -X POST "$B/api/v1/server/backups" || die "back up now"
for _ in $(seq 1 60); do
  st=$(curl -s -b "$JAR" "$B/api/v1/server/backups")
  [ "$(echo "$st" | json 'd["running"]')" = "False" ] && [ "$(echo "$st" | json 'len(d["copies"])')" -ge 1 ] && break
  sleep 1
done
err=$(echo "$st" | json 'd["status"].get("error") or ""')
[ -z "$err" ] || die "the database copy failed: $err"
[ "$(echo "$st" | json 'len(d["copies"])')" -ge 1 ] || die "no database copy was made"
echo "   $(echo "$st" | json 'd["copies"][0]["name"] if "name" in d["copies"][0] else d["copies"][0]')"

say "the AI worker's Python"
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) WPY="$RUNTIME/python/python.exe" ;; *) WPY="$RUNTIME/python/bin/python3" ;; esac
"$WPY" -I -B -c "import onnxruntime, cv2, tokenizers, rapidocr, numpy, PIL, photostead_worker; print('   onnxruntime', onnxruntime.__version__, onnxruntime.get_available_providers())" ||
  die "the worker's packages don't load"

if [ "${SMOKE_AI:-}" = 1 ]; then
  # (The server queues new pictures for the worker every 10 minutes. The question is a plain one:
  # "a photo" would be read as "show photos", a filter, with nothing left to search by meaning.)
  say "waiting for the AI worker (search by meaning)"
  found=0
  for _ in $(seq 1 180); do
    found=$(curl -s -b "$JAR" -H 'Content-Type: application/json' -H "Origin: $B" -d '{"q":"city"}' "$B/api/v1/ask" |
      json 'd.get("total", 0) if "look like" in (d.get("answer") or "") else 0' 2>/dev/null || echo 0)
    [ "${found:-0}" -ge 1 ] 2>/dev/null && break
    sleep 5
  done
  [ "${found:-0}" -ge 1 ] 2>/dev/null || die "the AI worker didn't look at the pictures in 15 minutes"
  echo "   search by meaning finds $found"
fi

case "$(uname -s)" in
  Darwin) # running it mustn't change the app (its signature covers every file)
    APPDIR=$(cd "$RUNTIME/../../.." 2>/dev/null && pwd)
    if [ -d "$APPDIR/Contents" ]; then
      [ -z "$(find "$APPDIR" -name __pycache__ -type d | head -1)" ] || die "Python wrote __pycache__ inside the app"
      codesign --verify --deep --strict "$APPDIR" 2>/dev/null || die "the app's signature no longer holds"
      echo "   the app is unchanged (signature holds)"
    fi ;;
esac

say "stopping"
stop
say "starting again (the same database)"
start
[ "$(curl -s "$B/api/v1/setup" | json 'd["needed"]')" = "False" ] || die "after a restart it asked for setup again"
say "stopping"
stop

rm -rf "$WORK"
echo "PASSED"
