# Getting started without a NAS: options

Written 2026-10-22, from a conversation about onboarding. Nothing here is decided yet.

Some people will set up Photostead's server on Unraid or a NAS without trouble. Many won't, and
onboarding done well is a large part of whether people stay. These are ways to get them started
with less, roughly from the least setup to the most. Photostead is well placed for most of them:
the server is one Go program with its database bundled.

## 1. Phone only, no server

The iPhone app opens the phone's own Photos library with Photostead's interface: the grid, the
viewer, Memories, search.

- **Apple allows it.** Any app may read and edit the Photos library once the person grants access
  (PhotoKit). The App Store has many alternative photo apps (Photomator, Darkroom, plenty of
  galleries), and Apple features some. It's an ordinary, supported use, not an antitrust question.
- **More works on the phone than you'd expect.** iOS groups faces on the device, iOS 27's on-device
  models can describe pictures, and apps like Queryable already run CLIP search ("dog on the beach at
  sunset") entirely on the phone.
- **What it can't do:** backups, share links and guests, the Household, and the heavy AI (Ask,
  generation details at scale). So it's a front door: "you like this? add a server and it backs up
  too."

The App Store download is the whole setup.

## 2. A desktop app for Mac and Windows

One installer; Photostead lives in the menu bar or the system tray; the library is a folder or an
external drive. No Docker, no NAS.

- The server is Go, which builds natively for both.
- Postgres can ship inside the app (as embedded-Postgres builds do), with libvips and ffmpeg.
- Faces and search already run on an ordinary processor (CPU ONNX models); a GPU is optional.
- Remote access is mostly built: the server has Tailscale inside it (tsnet), so the phone reaches it
  from anywhere with no router setup.
- The first screen shows the pairing QR code.

The catch: the computer has to be on for backups and remote access, and laptops sleep.

On a Mac there's a further option: Photostead could read the Mac's own Apple Photos library in
place (PhotoKit on macOS), so a Mac user gets Photostead over the photos they already have, with
nothing moved.

## 3. One click on the NAS people already own

Synology, QNAP, TrueNAS, Umbrel and CasaOS have app stores. Listing Photostead there (as the Unraid
template does now) makes setup "find it, click Install" for people who'd never touch Docker.

## 4. A plain Linux installer

A one-line install for a mini PC or a Raspberry Pi 5 that sets everything up: for the moderately
technical who don't want containers.

## 5. Hosted (optional, paid)

"Don't want to run anything? We'll run it for you." It removes setup entirely and is the clearest way
to earn from Photostead, but it pulls against "your photos, your hardware", so it works best as a
clearly separate option.

## Onboarding itself, whichever way in

- **Finding the server:** on the same Wi-Fi the phone finds it by itself (Bonjour, as AirPlay devices
  are found), so pairing is "tap your server" rather than a code.
- **One guided first run:** where photos live, pair the phone, invite the Household.
- **Bringing what people already have:** Apple Photos, Google Photos (a Takeout export), and Immich
  (built).

## A first ranking

1. The phone-only mode and the desktop app together cover the people who'd struggle: one lets them
   try it in a minute, the other gives them a real server without a NAS.
2. The NAS app stores are cheap to add after that.
3. Hosting is a business decision for later.
