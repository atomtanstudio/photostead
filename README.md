# Photostead

A self-hosted home for your photos and videos. It's built for a household's library, with a
photographer's toolkit, and it treats AI as part of the library: AI organizes your photos (faces,
search, captions), and AI-made images and videos are first-class media with their prompts kept.

https://github.com/user-attachments/assets/0ec50f60-2721-475e-92e3-0324465f4780

Photostead runs on your own server (an Unraid box, a NAS, any Linux machine with Docker) or as an
app on a Mac or Windows PC. Your originals stay byte for byte, in plain folders you can browse
without the app.

> **Status: 1.0.** Photostead is in daily use on its author's 120,000-item library. Set it up with
> the [install guide](https://photostead.com/install/), and pair Photostead for iPhone from the App
> Store. What's next is in the [build order](docs/architecture.md#build-order).

## What it does today

**Your library**
- A justified timeline that stays fast at 100,000+ items, with a year scrubber, Recently added,
  favorites, archive, a 30-day trash and a PIN-locked vault
- Existing folders indexed in place, read-only, and watched for changes; uploads filed into a
  readable `person/photos/year/date/` layout
- Live Photos, RAW+JPEG pairs, bursts, AI batches and look-alikes stacked as one
- Nested albums, smart albums, a read-only Folders view, keywords, bulk edits of descriptions and dates
- Stars, pick/reject flags and color labels for culling (Lightroom's keys), each person's own
- What you add (stars, labels, keywords, descriptions, dates, people's names, edits) written to XMP
  sidecars that Lightroom, digiKam and darktable read; originals are never touched
- A first photo editor (crop, turn, mirror, light, color) kept beside the untouched original;
  frame grabs and trims of videos
- Videos that not every browser plays (HEVC from phones, AV1, MKV, HDR, camera sound) get a
  copy that does, made with NVENC on a GPU worker or on the CPU, with HDR tone-mapped
- Memories: on this day, and people and pets over the years

**Finding things**
- Search by meaning (SigLIP 2), text in pictures (OCR), file names, prompts and descriptions, fused
  into one ranking; "More like this"
- Ask: plain-language questions ("videos of Mia from last summer"), answered on your own server
- Faces grouped with clustering that doesn't chain look-alike strangers together; corrections that
  re-grouping never undoes; cats and dogs as pets
- Screenshots, documents, receipts, IDs and QR codes sorted out, with a switch to keep them out of
  the timeline
- Adult content (nudity or sex) found by a local model, photos and videos alike, and shown, blurred
  or hidden as each person chooses

**AI-made media**
- Prompts, models, LoRAs, seeds and full ComfyUI workflows read from the files themselves
  (ComfyUI, Automatic1111/Forge, Midjourney, C2PA, IPTC), shown with one-click copy
- An evidence-based AI badge: nothing is called AI unless the file says so
- Characters (from their LoRAs and lineage, kept apart from real People), a prompt library, and a
  model & LoRA catalog
- Image-to-video clips linked to the image they started from

**Places**
- Where items were taken, named offline from GeoNames (no location leaves your server), as a list
  by country and a map of photo pins

**Sharing**
- A Household space with auto-share rules, invitations and roles
- Albums shared with people, and guests who never sign up: a personal link that opens on up to 3
  devices
- Public share links with passwords, expiry, QR codes and printable cards; location stripped from
  anything downloaded
- Comments, activity and picks (client proofing) on shared albums

**Getting photos in**
- Upload from the browser (files and whole folders), with Google Takeout and Apple Photos sidecars read
- An Immich importer: albums, favorites, archive, descriptions, tags and people's names
- **Photostead for iPhone**: background backup of the Photos library, plus browsing, search,
  sharing and the vault ([ios/](ios/README.md))
- **Photostead Capture**, a browser extension that saves posts from X, CivitAI, Instagram, TikTok
  and Pinterest with their prompts and a link back ([capture/](capture/README.md))
- A ComfyUI sync that files every finished output ([integrations/comfyui-sync](integrations/comfyui-sync/README.md))

**Running it**
- One container: the server, the web app and Postgres with pgvector
- Optional GPU workers on another machine pull AI jobs over HTTP, with
  [Mission Control](worker/photostead_worker/mission_control.py) VRAM leases for shared GPUs
- Remote access built in: Photostead joins your Tailscale network with its own HTTPS address, and
  can put share links, and paired phones, on the internet with Funnel, so the rest of the family
  never installs Tailscale. It also works behind any reverse proxy
- Passwords with two-step codes, passkeys, and OpenID Connect single sign-on
- Nightly database backups that are restored and checked, and a nightly integrity check of every
  original against its hash ([docs/backups.md](docs/backups.md))

## Running it

**What it needs:** a 64-bit x86 or ARM machine (a NAS, a mini PC, a Raspberry Pi 5, a Mac with
Docker) with 4 GB of memory, 8 GB for a big library. The database, thumbnails, previews and video
copies take about 0.65 GB of fast storage for every 1,000 photos and videos (a 100,000-item library
uses about 65 GB); originals stay where they are, or go in the photo share. No GPU is needed.

### Docker Compose

[deploy/compose/compose.yaml](deploy/compose/compose.yaml) runs it as it is in Synology's Container
Manager, Portainer, CasaOS or TrueNAS, or with `docker compose up -d`: change the folders and the user
in it first.

### Docker

```bash
docker build -f deploy/docker/Dockerfile -t photostead .
docker run -d --name photostead -p 4800:4800 --shm-size=1g --stop-timeout 60 \
  -v /path/to/appdata/photostead:/data \
  -v /path/to/photostead:/photos \
  -v /path/to/existing/photos:/libraries/photos:ro \
  photostead
```

Open `http://<server>:4800` and create the first account; a checklist on the timeline walks through the rest. `/data` holds the database, thumbnails
and previews (fast storage is best). `/photos` is where uploads and phone backups are filed, in a
folder per person. Each folder mounted under `/libraries/<name>` is found on its own and indexed in
place, never written to. The [guide](docs/guide.md) covers the rest.

### Unraid

[deploy/unraid/ca/photostead.xml](deploy/unraid/ca/photostead.xml) is the Community Applications
template, with the recommended share layout: originals on the array (a `photostead` share, cache
then array), the database and thumbnails on the cache pool. Images are published to
`ghcr.io/atomtanstudio/photostead` when a version is tagged.

### Mac and Windows

The same server as an app, without Docker: it starts its own database, the server and its AI worker,
with a menu-bar (Mac) or tray (Windows) icon to open Photostead, pair a phone and choose where
originals go. Download it from the [latest release](https://github.com/atomtanstudio/photostead/releases/latest):
`Photostead.dmg` for a Mac with Apple silicon (macOS 26 or later), `Photostead-windows.zip` for
64-bit Windows 10 or 11. How it's built is in [desktop/](desktop/README.md).

### Your AI assistant (MCP)

Claude Code, Codex and other MCP clients can search, look at and organize your library, and add
what they make with its prompt, under your own AI subscription:

```bash
photostead apikey add -name claude      # on the server: prints a key, once
claude mcp add --transport http photostead http://<server>:4800/mcp --header "Authorization: Bearer psk_…"
```

### A GPU worker (optional)

The container includes a small CPU worker, so search, faces and OCR work without a GPU. A machine
with an NVIDIA GPU can take the heavy work:

```bash
photostead worker add -name gpu-box          # on the server: prints a worker token
docker run -d --gpus all -e PHOTOSTEAD_SERVER=http://<server>:4800 -e PHOTOSTEAD_WORKER_TOKEN=<token> \
  -v photostead-models:/models ghcr.io/atomtanstudio/photostead-worker
```

Or without Docker: `pip install "./worker[gpu]"`, then `photostead-worker run --server … --token-file … --gpu`.

## Developing

You'll need Go (see `server/go.mod`), Node 24, Docker, libvips, exiftool and ffmpeg.

```bash
make env       # deploy/dev/.env with random secrets
make db-up     # Postgres 18 + pgvector in Docker
make web       # build the web app
make run       # build and run the server on the built web app
make test      # server tests (the integration tests use the dev database)
```

`make help` lists the rest. The web app's own checks are `npm test` and `npm run typecheck` in `web/`.

| Folder | What's in it |
|---|---|
| `server/` | The Go server: API, indexing, media pipeline, jobs, sharing, sync |
| `web/` | The React + Vite web app |
| `worker/` | The Python AI worker (ONNX Runtime): search embeddings, faces, pets, adult content, OCR, video copies |
| `ios/` | Photostead for iPhone (SwiftUI) |
| `capture/` | Photostead Capture, the browser extension |
| `integrations/` | ComfyUI sync |
| `desktop/` | Photostead for Mac and Windows: the server as an app, without Docker |
| `deploy/` | Dockerfile, Compose file and Unraid template |
| `docs/` | The [guide](docs/guide.md), [architecture](docs/architecture.md), [backups](docs/backups.md) |

## Credits

Photostead's AI runs on openly licensed models, each downloaded once as a pinned, checksummed file:

| What | Model | By | License |
|---|---|---|---|
| Search | [SigLIP 2](https://huggingface.co/google/siglip2-base-patch16-256) base, 256 px | Google; ONNX by Hugging Face's [ONNX community](https://huggingface.co/onnx-community/siglip2-base-patch16-256-ONNX) | Apache-2.0 |
| Faces: finding | [YuNet](https://github.com/opencv/opencv_zoo/tree/main/models/face_detection_yunet) 2023mar | OpenCV Zoo | MIT |
| Faces: recognizing | [AuraFace v1](https://huggingface.co/fal/AuraFace-v1) | fal | Apache-2.0 |
| Pets: finding | [RF-DETR](https://github.com/roboflow/rf-detr) Nano | Roboflow; ONNX by Hugging Face's [ONNX community](https://huggingface.co/onnx-community/rfdetr_nano-ONNX) | Apache-2.0 |
| Pets: recognizing | [pet-recognition-base](https://huggingface.co/open-noodle/pet-recognition-base) | open-noodle | Apache-2.0 |
| Adult content | [nsfw_image_detector](https://huggingface.co/Freepik/nsfw_image_detector) (EVA-02) | Freepik; [ONNX](https://huggingface.co/doppeltilde/nsfw_image_detector-ONNX) by doppeltilde | MIT |
| Text in pictures | [RapidOCR](https://github.com/RapidAI/RapidOCR) with PaddleOCR's PP-OCR models | RapidAI, PaddlePaddle | Apache-2.0 |

Places come from [GeoNames](https://www.geonames.org) (CC BY 4.0), and the map is drawn with
[MapLibre GL JS](https://maplibre.org) (BSD-3-Clause), with tiles from
[OpenFreeMap](https://openfreemap.org): © OpenMapTiles, data © OpenStreetMap contributors. Pictures
and videos are handled with [libvips](https://www.libvips.org), [FFmpeg](https://ffmpeg.org) and
[ExifTool](https://exiftool.org), and kept in [PostgreSQL](https://www.postgresql.org) with
[pgvector](https://github.com/pgvector/pgvector).

## License

[AGPL-3.0](LICENSE). If you run a modified Photostead for other people, you share your changes with
them.
