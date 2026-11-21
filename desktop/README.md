# Photostead for Mac and Windows

The Photostead server on a computer people already have, without Docker: one app that starts the
database, the server and its AI worker, with a menu-bar (Mac) or tray (Windows) icon to open
Photostead, pair a phone, choose where originals go, keep the computer awake, open at login, and
quit. The plan and its reasoning are in `docs/plans/desktop-app-plan.md` (not in git).

## What's here

- `*.go`: the launcher, one program for both. It starts PostgreSQL (creating the database the
  first time), then `photostead serve` with everything it's told (where the database, data and
  originals are, the bundled Python as its worker, the web app), restarts the server if it stops on
  its own, and stops both cleanly (the server by closing its input, `PHOTOSTEAD_STOP_ON_STDIN`, then
  `pg_ctl stop -m fast`). On the Mac Postgres listens only on a socket in `/tmp`; on Windows on
  `127.0.0.1:54329`, with passwords made on first run.
- `mac/build.sh`: builds `build/mac/Photostead.app` and a `.dmg` (Apple silicon, macOS 14+).
  PostgreSQL 18 with pgvector and an LGPL ffmpeg (VideoToolbox, no libx264) are built from source
  once; libvips and its HEIC module come from Homebrew and are copied in by `mac/relink.py`, which
  points every library reference inside the app. Signed with the keychain's Developer ID
  Application certificate (hardened runtime, secure timestamps; the worker's Python has
  `mac/python.entitlements`), or ad hoc when there's none, and then it runs only on the Mac that
  built it.
- `mac/notarize.sh`: has Apple notarize the signed app, staples the ticket, then makes, notarizes and
  staples the disk image (`mac/dmg.sh`: the app and a shortcut to Applications). It uses a notarytool
  keychain profile, `photostead-notary`, made once with `xcrun notarytool store-credentials`.
- `windows/build.sh`: builds `build/windows/Photostead` and a zip, in an MSYS2 UCRT64 shell
  (`.github/workflows/desktop.yml` runs it on GitHub's Windows runner): MSYS2's libvips and
  PostgreSQL with pgvector, BtbN's static LGPL ffmpeg, exiftool's Windows build.
- `test/smoke.sh`: the app end to end without its icon (`-headless`): the first account, a JPEG, an
  iPhone HEIC and a video with thumbnails, a database copy (pg_dump, zstd), the worker's Python, and
  with `SMOKE_AI=1` the worker looking at the pictures; then stopping, starting again and stopping.
- `assets/`: the icons (the house mark), made from `ios/Photostead/AppIcon.icon/Assets/mark.svg`
  with `rsvg-convert`.

## Where it keeps things

| | Mac | Windows |
|---|---|---|
| Database, thumbnails, models, settings, logs | `~/Library/Application Support/Photostead` | `%LOCALAPPDATA%\Photostead` |
| Originals (uploads, phone backups) | `~/Pictures/Photostead Library` | `Pictures\Photostead Library` |
| Settings | `desktop.json` in the first | the same |
| Opens at login | a LaunchAgent, `com.photostead.desktop` | the Run key, `Photostead` |

The web app is on port 4800 (`desktop.json`'s `port`), so a phone pairs with
`<computer name>.local:4800` at home, or through Photostead's built-in Tailscale away.

## Building and testing

```bash
desktop/mac/build.sh
desktop/test/smoke.sh desktop/build/mac/Photostead.app/Contents/MacOS/Photostead desktop/build/mac/Photostead.app/Contents/Resources/runtime
desktop/mac/notarize.sh    # for a release: Photostead.dmg, notarized and stapled
```

Windows: push to the `desktop-apps` branch (or a `v*` tag, or run the workflow by hand); the zip is
the run's artifact.

## Not yet

- A code-signing certificate for Windows: until then SmartScreen warns about an unrecognized app
  ("More info" → "Run anyway").
- Running before anyone logs in (a launch daemon, a Windows service), updates (Sparkle), a trimmed
  libvips, the Mac's Photos library as an import, holding uploads while an external drive is
  unplugged. See the plan.
