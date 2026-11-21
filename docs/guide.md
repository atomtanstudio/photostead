# Photostead guide

How to install Photostead, bring your photos in, and run it. For how it works inside, see
[architecture](architecture.md).

## 1. Install

### On Unraid

1. **Make a share named `photostead`.** Set Primary storage to the cache, Secondary storage to the
   array, and Mover action to cache → array. Your originals live here, protected by parity: new
   ones land on the fast cache, and the mover puts them on the array overnight.
2. **Add the container.** In Community Applications (the Apps tab) search for Photostead. If it isn't
   listed yet, add the template from [`deploy/unraid/ca/photostead.xml`](../deploy/unraid/ca/photostead.xml)
   in Unraid's terminal, then **Docker** → **Add Container** → **Template** → **Photostead**:
   `wget -O /boot/config/plugins/dockerMan/templates-user/my-Photostead.xml https://raw.githubusercontent.com/atomtanstudio/photostead/main/deploy/unraid/ca/photostead.xml`. The defaults put the
   database and thumbnails in `appdata/photostead` on the cache pool, and originals in the
   `photostead` share.
3. **Point it at photos you already have** (optional). Map each folder read-only to
   `/libraries/<name>`, for example `/mnt/user/pictures` → `/libraries/pictures`. Photostead finds
   them on its own and indexes them in place. It never writes to them.
4. **Open the web UI** (port 4800) and create the first account. That account is the admin.
   A **Get started** checklist at the top of the timeline then walks through the rest: bringing
   photos in, backing up a phone, reaching Photostead away from home, inviting the household and
   keeping a second copy. Each step ticks itself off once it's done; skip any, or put the list
   away (it waits in the sidebar until everything's done).

### What it needs

- A 64-bit x86 or ARM machine: a NAS, a mini PC, a Raspberry Pi 5, or a Mac or PC running Docker.
  No GPU; a GPU machine can join later and take the heavy AI work.
- 4 GB of memory, 8 GB for a big library. Photostead gives its database an eighth of what's there
  (up to 2 GB), worked out each time it starts. Health warns under 4 GB.
- Fast storage for `/data`: about 0.65 GB for every 1,000 photos and videos (thumbnails, previews,
  video copies and the database). A 100,000-item library uses about 65 GB.
- Room for originals in the photo share, for what's uploaded and backed up from phones.

### With Docker Compose

[`deploy/compose/compose.yaml`](../deploy/compose/compose.yaml) is everything in one file. In
Synology's Container Manager choose **Project › Create**, in Portainer **Stacks › Add stack**, or
paste it into CasaOS or a TrueNAS custom app. Change the folders on the left of each volume, and set
`PUID` and `PGID` to the user who owns them, then start it and open port 4800.

### With Docker

```bash
docker run -d --name photostead -p 4800:4800 --shm-size=1g --stop-timeout 60 \
  -v /srv/photostead-data:/data \
  -v /srv/photos/photostead:/photos \
  -v /srv/photos/existing:/libraries/existing:ro \
  ghcr.io/atomtanstudio/photostead:latest
```

`/data` holds the database, thumbnails and previews, so fast storage is best. `/photos` is where
uploads and phone backups are filed. Folders under `/libraries/` are indexed in place, read-only.
Give the container 60 seconds to stop, so its database shuts down cleanly.

## 2. Where your photos live

- **Uploads and phone backups** are filed in the `photostead` share as
  `library/<username>/photos/<year>/<date>/` (and `videos/…`), by the day each was taken. Names
  are kept as they came. You can browse the folders without the app.
- **Existing folders** stay exactly as they are. Photostead watches them for new files, and
  rescans them every night at 4:30 as a safety net.
- **What you add in Photostead** goes in its database, and it's also written to an XMP sidecar
  that Lightroom, digiKam and darktable read. That covers stars, labels, keywords, descriptions,
  dates you set, people's names and edits. Sidecars sit beside originals in the share (`IMG_1234.HEIC.xmp`); for read-only
  folders they go in `/data/sidecars/`. Originals are never changed.
- **The same file twice** (byte for byte) is one item, wherever its copies are.

## 3. Bring photos in

- **From the browser**: drag files or whole folders onto the page, or use **Upload** in the
  sidebar. Google Takeout exports and Apple Photos exports (with their sidecars) keep their dates,
  descriptions, albums and favorites. **Import** in the sidebar shows every way in.
- **From Immich**: **Import** → Immich, with your server's address and an API key (`asset.read`,
  plus `album.read`, `person.read` and `tag.read` for albums, people and tags). It only ever
  reads from Immich. Mount Immich's upload folder read-only under `/libraries/` too, so the files
  are indexed in place.
- **From your iPhone**: [Photostead for iPhone](../ios/README.md) backs up your Photos library in
  the background and lets you browse, search and share. Pair it with **Devices & remote access** →
  Show a pairing code.
- **From the web**: [Photostead Capture](../capture/README.md), a Chrome or Edge extension, saves
  posts from X, CivitAI, Instagram, TikTok and Pinterest with their text, prompts and a link back.
- **From ComfyUI**: the [ComfyUI sync](../integrations/comfyui-sync/README.md) files every
  finished output, with its workflow.
- **From your AI assistant**: see [MCP](#8-your-ai-assistant) below.

## 4. Find things

- **Search** (the bar at the bottom) looks for what's in a picture, the words in its prompt or
  description, text in it, and file names, all at once. **Ask** answers questions such as
  "how many videos of Ada did I make last year?".
- **People** are found by face recognition. Name them once; corrections stick. Cats and dogs show
  up as pets to name.
- **Places** shows a map and a list of where things were taken. Places are named offline, so no
  location leaves the server. The map's background tiles come from OpenFreeMap; set
  `PHOTOSTEAD_MAP=off` to hide the map.
- **Utilities** sorts out screenshots, documents, receipts, IDs, QR codes, AI-made images and
  camera photos. **In the timeline: hidden** keeps the first five out of your timeline.
- **Adult content**: a local model rates every photo and video. Each person chooses, in the
  sidebar (or the iPhone app's Settings), whether it's shown, blurred until clicked, or hidden
  everywhere but the Adult page; the iPhone app follows the same choice. "Not adult" in an item's
  details corrects it.

## 5. Organize

Favorites, the archive, a 30-day trash and a PIN-locked vault are in the sidebar. Albums can hold
albums, smart albums keep themselves up to date from rules, and stacks group RAW+JPEG pairs,
bursts, AI batches and look-alikes. Turn on **Ratings & flags** for stars, pick/reject and color
labels with Lightroom's keys (0–5, P, X, U, 6–9). Select several items to edit keywords,
descriptions and dates together. Every change offers Undo.

## 6. Share

- **The Household**: invite your family in **Household**. Each person keeps their own library,
  and auto-share rules put what you choose (from a date, with some people) in everyone's timeline.
- **Albums**: share one with someone in the Household, or with anyone by name. They get a
  personal link that opens on up to 3 devices, with nothing to sign up for. Someone shared with by
  name is a guest. Stopping sharing with them leaves their guest account; the admin removes it under
  **Household** → Guests. That signs out their devices and deletes their comments and picks. The
  albums and photos stay.
- **Share links**: a link to an album, with an optional password and last day. Downloads have
  their location taken out.

## 7. Reach it from anywhere

The simplest way is **Devices & remote access** → Tailscale. Photostead joins your tailnet as a
device of its own, with an HTTPS address, so passkeys and the iPhone app work away from home.
Turn on **Funnel** there to open share links and personal links to people without Tailscale.

With Funnel on, a paired iPhone also works from anywhere with no Tailscale on it: pair it once at
home (or with Tailscale on), and from then on its own token gets it in through Funnel. So only the
person who sets Photostead up needs Tailscale. From the internet a paired phone can't pair other
devices, add passkeys or two-step sign-in, invite people or change remote access; those happen at
home or on the tailnet. Untick **Paired phones work from anywhere** to keep phones to home and the
tailnet. Signing in on the web from the internet stays guests-only.

Behind your own reverse proxy or Cloudflare Tunnel, set `PHOTOSTEAD_PUBLIC_URL` to the address
people use, and `PHOTOSTEAD_SECURE_COOKIES=true` when it's HTTPS. Phones pair with that address too,
and work through it from anywhere. Cloudflare turns away a request over 100 MB, so through it the
iPhone sends a big video in 64 MiB pieces, by itself. Behind another proxy that caps a request's size
(nginx's `client_max_body_size`, say), set `PHOTOSTEAD_UPLOAD_PIECE` to the most it takes, in MiB.
Uploads from a browser still go in one request each, so allow big ones in your proxy.

Ten wrong pairing codes or share-link passwords in ten minutes make that person wait. Behind a
proxy, Photostead tells people apart by the address the proxy says each request came from (its
`X-Forwarded-For`), so one stranger's guesses don't lock everyone else out. It believes that only
from a proxy on the same machine, Docker network or home network. Caddy, Traefik and Cloudflare
Tunnel send it as they are; with nginx, add
`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`. A proxy anywhere else, such as a
VPS that reaches Photostead over Tailscale, needs its address in `PHOTOSTEAD_TRUSTED_PROXIES`.

A pairing code lasts 10 minutes, so the phone has to be with you. For someone who can't come to
your home network (family far away, or Apple's App Review with a demo server), the admin can
**Make a code that lasts** under the pairing code instead: it pairs a few devices (5 unless you
say, up to 50) into the account you pick, for some days (14 unless you say, up to 90), with a short
note to tell it apart in the list ("App Review", "Mum's iPad"). It's shown once, with its QR code
and link. The phone still has to reach Photostead to pair, at the public
address or on your tailnet; pairing never happens through Funnel. Revoking the code stops more
devices pairing with it. The ones it paired stay signed in until they're signed out under
**Where you're signed in**, in that account's own Photostead.

Sign-in has two-step codes and passkeys under **Security**, and single sign-on with any OpenID
Connect provider (the `PHOTOSTEAD_OIDC_…` settings).

## 8. AI

**Workers.** The container has a small CPU worker, so search, faces, pets, text in pictures,
adult content and videos' copies all work without a GPU, just slowly on a big library. A machine
with an NVIDIA GPU can do the heavy lifting:

```bash
photostead worker add -name gpu-box     # on the server (docker exec -it photostead photostead …): prints a token
docker run -d --gpus all -e PHOTOSTEAD_SERVER=http://<server>:4800 -e PHOTOSTEAD_WORKER_TOKEN=<token> \
  -v photostead-models:/models ghcr.io/atomtanstudio/photostead-worker
```

On a GPU that other apps share, add `-e MISSION_CONTROL_URL=…` so the worker takes turns through
[Mission Control](../worker/photostead_worker/mission_control.py). Workers pull work, so a GPU
machine that sleeps costs nothing but time.

**Your AI assistant.** Claude Code, Codex and other MCP clients can search, look at, organize and
add to your library, under your own subscription:

```bash
photostead apikey add -name claude      # prints a key, once
claude mcp add --transport http photostead http://<server>:4800/mcp --header "Authorization: Bearer psk_…"
```

It acts as you: the same gate, your adult-content setting, and never the vault.

**What leaves your server.** Nothing about your photos. The map's background tiles are fetched
from the map's server (which sees the areas you look at), and GeoNames' list of towns is
downloaded once. Models are downloaded once from Hugging Face, pinned by checksum.

## 9. Backups

The database is copied every night at 3:30 and checked by reading it back, and every original is
checked against its hash over time. See [backups](backups.md) for where copies go, how to restore
one, and what to copy offsite: the `photostead` share (originals, sidecars and database copies) and,
for sidecars of read-only folders, `/data/sidecars`.

**Health** in the sidebar (for the admin) looks over the server and says, in plain words, what needs
a look and what to do: whether Photostead can write to the photo share, disk space, memory, whether
each folder indexed in place is still there, whether search, faces and text are being worked
through, backups, and remote access. It says "fine" or how many things to look at, and checks again
every few minutes.

## 10. Settings

| Setting | Default | What it does |
|---|---|---|
| `PHOTOSTEAD_PUBLIC_URL` | (each visit's address) | The address people reach Photostead at, for links and single sign-on |
| `PHOTOSTEAD_SECURE_COOKIES` | `false` | `true` behind HTTPS |
| `PHOTOSTEAD_TRUSTED_PROXIES` | loopback and private addresses | The reverse proxies whose `X-Forwarded-For` is believed, to count wrong codes and passwords per person: addresses and ranges, `private` for the default, `off` for none |
| `PHOTOSTEAD_TIMEZONE` | `TZ`, then the system's | A default until each person picks their own |
| `PHOTOSTEAD_MANAGED_ROOT` | `/photos` | The photostead share |
| `PHOTOSTEAD_LIBRARIES` | `/libraries` | Where existing folders are mounted, each found on its own |
| `PHOTOSTEAD_WATCH` | on | `off` stops watching folders (they're still rescanned nightly) |
| `PHOTOSTEAD_BACKUP_DIR` | the share's `backups/database` | Where database copies go; `off` for none |
| `PHOTOSTEAD_BACKUP_KEEP` | `14` | How many copies to keep |
| `PHOTOSTEAD_MAP` | on | `off` hides the map |
| `PHOTOSTEAD_UPLOAD_PIECE` | (whole; 64 MiB through Cloudflare) | The most a phone sends in one request, in MiB, behind a proxy that caps it |
| `PHOTOSTEAD_MAP_STYLE_LIGHT`, `_DARK`, `_HOSTS` | OpenFreeMap | Other MapLibre styles, and the https origins they load from |
| `PHOTOSTEAD_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_NAME`, `_NEW_ACCOUNTS` | off | Single sign-on |
| `PHOTOSTEAD_IMAGE_WORKERS`, `PHOTOSTEAD_VIDEO_WORKERS` | `8`, `2` | How many photos and videos are indexed at once |
| `PHOTOSTEAD_DISCARD_UPLOADS` | off | `on` for a demo server: every upload is answered as if filed, then thrown away, so nothing anyone sends is kept |
| `PUID`, `PGID` | `99`, `100` | Who Photostead runs as (Unraid's nobody:users) |

## 11. From a shell

`docker exec -it photostead photostead <command>` runs these on the server:

- `status`: files, renders and queued work
- `worker add|list|revoke`: GPU workers' tokens
- `apikey add|list|revoke`: keys for Capture, the ComfyUI sync and MCP
- `pairing add [-email E] [-days N] [-devices M] [-note "App Review"]|list|revoke -id ID`: pairing
  codes that last, each with a short note if you like (see [Reach it from anywhere](#7-reach-it-from-anywhere))
- `library list`, `library sidecars -name N beside|shadow|off`, `library exclude -name N -folder F`,
  `library remove -name N` (a folder whose items all have a copy in the share; unmount it first)
- `videos status`, `sidecars status`, `places`: progress of those
- `labels refresh`: label adult content again after a rule change (the server also does it by itself)
- `backup now|list|check|restore`: see [backups](backups.md)
