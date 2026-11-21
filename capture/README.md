# Photostead Capture

Saves the images and videos of a post to Photostead, with the post's text, its author and a link back
to it. It works on:

- **X / Twitter**: the button beside each post's actions (before Share).
- **CivitAI**: a button on the corner of every image and video (the Images and Videos feeds, posts,
  model galleries, profiles), and beside the download button on an image's own page.
- **Instagram**: beside Share on each post in the feed, on a post's own page or its pop-up, and on
  Reels.
- **TikTok**: under Share in each video's action column (For You, Following, a video's own page).
- **Pinterest**: on the corner of each pin in a grid (it shows while the pin is hovered, like
  Pinterest's own buttons, and stays once saved), and beside Share on a pin's own page.

The button is Photostead's mark; a green check on it means that post was saved before.

Install: see `INSTALL.txt`.

## What a save sends

Every file of the selected post, each to `POST /api/v1/uploads` with the post's date (`takenAt`), a
description (the text, the author, the link) and the link as `sourceUrl`. Photostead files it by day
in the photostead share and indexes it like any other file. File names are `<site>-<post id>-<n>`.

| Site | Files | Description | Also |
|---|---|---|---|
| X | original-size photos, the highest-bitrate MP4 of each video | text · name · @handle · link | |
| CivitAI | the original upload (`original=true`), image or video | the post's title, then `Prompt:`, `Negative prompt:`, `Model:`, `Resources:` (LoRAs etc. with weights), and seed, steps, sampler, scheduler, CFG, size, clip skip, denoise · @creator · link | the prompt as `prompt`, which Photostead shows when the file proves it's AI and doesn't say |
| Instagram | every slide of a carousel at its largest size; the video of a Reel or video slide | caption · name · @handle · link | |
| TikTok | the video at its highest resolution (H.264 when two are as large), or every picture of a photo post | caption · name · @handle · link | |
| Pinterest | the pin's original image, or its video (its 720p MP4, or its HLS stream joined into one MP4), or every page of an idea pin | title, description, where it's from · name · @handle · link | |

Where the details come from, per site (so it's clear what to look at when a site changes):

- **X**: X's own GraphQL answers, caught on the page (`content/inject.js`, `forensic-capture.js`).
- **CivitAI**: CivitAI's API from the page (`image.get`, `image.getGenerationData`, `post.get`;
  the public `/api/v1/images` if those fail, without the generation settings).
- **Instagram**: Instagram's API when signed in (`/api/v1/media/<id>/info/`); signed out, the
  post's data embedded in its own page, so open the post first.
- **TikTok**: the data TikTok embeds in a video's page (`__UNIVERSAL_DATA_FOR_REHYDRATION__`),
  fetched for videos further down a feed.
- **Pinterest**: Pinterest's API from the page (`PinResource`).

## How it works

- Each site is one file in `content/sites/` (`civitai.js`, `instagram.js`, `tiktok.js`,
  `pinterest.js`). Everything that depends on the site's markup or API is in a `SELECTORS` (and
  `API`) object at the top of the file: when a site changes and the button stops appearing, that's
  the place to fix. `common.js` is shared: the button, its states, toasts, and the hand-off to the
  service worker. `sites.css` sizes the button to each site's own buttons.
- The service worker (`background/service-worker.js`) checks each post against `lib/sites.js`
  before anything is downloaded: the post's link must be on the site it came from, and every file
  must come from that site's own media servers (redirects may only move to another of them).
- Media is downloaded by the service worker, without cookies, except TikTok's videos: TikTok's video
  servers turn away requests that don't come from its pages, so the page downloads the file and
  hands it to the service worker in pieces (`saveWithFiles` in `common.js`, `lib/transfer.js`).
  TikTok's pictures (photo posts) are the other way round: pages can't read them, so the service
  worker downloads those.
- Pinterest keeps most videos uploaded lately (TikToks pinned there among them) only as an HLS
  stream, with no MP4. The service worker reads the stream's largest picture and its sound (each a
  fragmented MP4, CMAF) and joins them into one ordinary MP4 with its index at the front, without
  re-encoding (`lib/hls.js`), so it plays in Safari and the iPhone app as it is. A video that has an
  MP4 is saved from that. Every address in a stream is checked against Pinterest's servers too.
- Photostead keeps one copy of the same bytes. Saving a picture that's already there (reposted, or
  saved before) adds the new post's text to its description instead.
- Each attachment keeps a checkpoint in the extension's local database. Retrying after an error never
  sends a file twice.
- It pairs with Photostead using a code from Devices & remote access (or the link that carries it):
  `POST /api/v1/devices/pair` gives it a device token (`psd_…`), the server's id and its addresses
  (tailnet, public, home). The token lives only in the extension's storage (trusted contexts, never
  content scripts), and the device can be signed out from Photostead. An API key from
  `photostead apikey add` still works instead.
- Before a save it checks which address answers (`GET /api/v1/server`, a few seconds each, and that
  it's the same server id), so it works at home and over Tailscale away from home, and starts with
  the one that answered last. Saves are kept apart by server id, not address.

## When a site changes

Sites rename things often. If the button disappears on a site, or saves fail with "couldn't tell
which post this is" or "didn't give this post's details":

1. Open the site's file in `content/sites/` and look at `SELECTORS` / `API` at the top.
2. Find the new markup in the browser's inspector (the Share button, the post's link, the card's
   link around its picture) and update the selector.
3. Reload the extension (Extensions page → reload) and the site's tab.

## Tests

`node --test 'capture/tests/*.test.mjs'` from the repository root. `sites.test.mjs` checks each site's reading
of its data (from the shapes the sites sent on 2026-11-06), the checks on hosts and links, the
uploads, and the TikTok hand-off. One test checks that this extension and FeedSilo's coexist on the
page. It runs only when FeedSilo's source is mounted at `~/legion/coding/codex/feedsilo/extension`.
