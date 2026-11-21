# Photostead architecture and data model

Status: build order confirmed 2026-10-16. The technical choices are proposals that stand unless revisited. It describes the system as built, and what's planned. Where this doc makes a new technical choice, it says so under "Choices this doc makes", so each one can be questioned on its own.

## Principles

These come from the decisions record and from what the prototype taught us. Every part of the design below follows them.

1. **Originals are never modified.** Files stay byte-for-byte. Edits are recipes, metadata goes to XMP sidecars, and derived files live elsewhere.
2. **Evidence before labels.** Nothing is called AI, a person, a pet or a duplicate unless something in the file or a user decision supports it. Machine suggestions are stored apart from confirmed facts. (The prototype's "no camera data means AI" guess was wrong for 97% of a sample.)
3. **One visibility gate.** Archived, hidden, vault and trashed items are filtered in one place in the data layer, never per feature. (The prototype's creator index briefly leaked non-timeline items before this rule existed.)
4. **Local first, GPU optional.** Everything works on a CPU-only server. A GPU worker makes it faster and unlocks heavier models. Cloud AI is opt-in per feature and only ever sees downsized, GPS-stripped thumbnails.
5. **Stable contracts.** The API is versioned and published as OpenAPI. Database migrations are automatic and reversible, and no release ever requires deleting the database.
6. **One container for the household server**, plus an optional worker image for GPU machines.

## System overview

```
                         ┌────────────────────────── Unraid (no GPU) ───────────────────────────┐
 Web (React + Vite) ───▶ │  photostead (Go, single binary)                                       │
 iOS / Android apps ───▶ │   ├─ REST API /api/v1 (OpenAPI 3.1) · sync feed · uploads (resumable) │
 Capture extension  ───▶ │   ├─ MCP server /mcp · generation ingest API                          │
 Claude / Codex (MCP) ─▶ │   ├─ library indexer + watcher · media pipeline (libvips, ffmpeg,      │
                         │   │    exiftool, c2pa) · job scheduler (River on Postgres)             │
                         │   └─ worker API /api/worker/v1 (pull)                                  │
                         │  built-in CPU worker (Python, ONNX Runtime) ── same pull API           │
                         │  Postgres 18 + pgvector                                               │
                         │  /photos (originals, array) · /data (DB, derived files, cache pool)   │
                         └──────────────────────────────────▲────────────────────────────────────┘
                                                            │ pull jobs, fetch renditions, post results
                         ┌──────────────── Legion (RTX 5090, Linux) ──────────────────────────────┐
                         │  photostead-worker (Docker, CUDA) ── GPU arbiter ── Mission Control   │
                         │  (VRAM leases) · llama.cpp router (VLM captions) · ComfyUI (re-run)   │
                         └────────────────────────────────────────────────────────────────────────┘
```

| Component | Technology | Responsibility |
|---|---|---|
| Server | Go, single binary | API, auth, sharing, indexing, media pipeline, scheduling, sync, MCP |
| Database | Postgres 18 + pgvector | All metadata, job queue (River), vectors, full-text search |
| Built-in worker | Python + ONNX Runtime (CPU) | Default models: SigLIP 2 search embeddings, faces, OCR |
| GPU worker | Same Python worker, CUDA image | Heavy models, video transcoding (NVENC), ComfyUI jobs |
| Web | React + Vite, TypeScript | The Lightbox design: timeline, viewer, creator tools, Ask |
| Mobile | SwiftUI, Jetpack Compose | Backup and basic browsing in v1; clients generated from OpenAPI |
| Capture | Chrome/Edge MV3 extension | Save X media with prompts and source attribution |

The built-in worker is just another worker that happens to run inside the container and talk over localhost. There is one worker protocol, and installs with a GPU machine add a second worker with more capabilities.

## Repository layout

One monorepo, so the API contract, the clients and the server change together.

```
server/          Go module: cmd/photostead, internal/…, migrations/, openapi/
web/             React + Vite app (the Lightbox design)
worker/          Python worker: models, GPU arbiter, Mission Control adapter
apps/ios/        SwiftUI backup app
apps/android/    Compose backup app
extension/       Photostead Capture
deploy/          Dockerfiles, Unraid template, compose files for development
site/            photostead.com: landing page, App Store pages, the in-browser demo (Cloudflare Workers)
docs/            The guide, this architecture, backups
```

### Server packages

| Package | Owns |
|---|---|
| `api` | HTTP handlers, OpenAPI types, auth middleware, rate limits |
| `store` | Postgres access (pgx + sqlc), the visibility gate, migrations |
| `library` | Libraries, folder scanning, file watching, content hashing, change detection |
| `media` | Thumbnails, previews, ThumbHash, video probing and transcode jobs, Live Photo splitting |
| `metadata` | exiftool (`-stay_open`), normalization, XMP sidecar read/write, offline geocoding |
| `provenance` | Generation metadata parsers (ComfyUI, A1111/Forge, C2PA, IPTC/XMP AI fields, video containers, capture posts), source type |
| `search` | Filters, keyword (tsvector + trigram), vector search, hybrid ranking, the Ask planner |
| `people` | Faces, clustering, constraints, people, pets, Characters |
| `organize` | Albums, smart albums, stacks, keywords, ratings and flags, trash, vault |
| `share` | Household space and auto-share rules, links, guests, comments, activity |
| `sync` | Change feed, device checkpoints, backup state |
| `ingest` | Resumable uploads (IETF + tus), hash pre-check, importers (Immich, Takeout, Apple Photos, sidecar folders) |
| `workers` | Worker registry, task leasing, capability routing, results |
| `jobs` | River job definitions and the job dashboard |
| `mcp` | MCP server tools over the same services |

## Storage layout

Decided 2026-10-16. Every install gets the same default, and the Unraid template ships these paths. Photostead gets a share of its own instead of living inside a general-purpose one, and inside it the files are plain folders you can browse without the app.

```
/mnt/user/photostead/                      share on the array: originals, protected by parity      → /photos
  library/
    <person>/
      photos/<yyyy>/<yyyy-MM-dd>/<filename>
      videos/<yyyy>/<yyyy-MM-dd>/<filename>
  inbox/<person>/                          drop folder: files are filed into library/ and removed once verified
  backups/
    db/                                    nightly database dumps
    manifests/                             backup manifest: the hash of every original

/mnt/user/appdata/photostead/              cache pool, Unraid's usual home for app data              → /data
  postgres/
  derived/                                 thumbnails, previews, transcodes, Live Photo motion, rendered edits (sharded by asset ID)
  sidecars/                                shadow XMP sidecars for read-only libraries, mirroring their paths
  blobs/                                   ComfyUI workflows and C2PA manifests, stored once by hash
  config/

any existing folder (optional, read-only)                                                         → /libraries/<name>
```

**Filing rules for the managed library:**
- **`<person>` is the owner's username,** a stable short name. Changing a display name never moves files.
- **`photos/` or `videos/` follows the asset's type.** Files that belong to an asset stay together:
  - A Live Photo's motion clip stays beside its still in `photos/`.
  - RAW+JPEG pairs both live in `photos/`.
  - A frame saved from a video becomes a new photo in `photos/`.
- **The date is the local day the item was taken** (`local_date`). If there's no capture date, the file's own modified date is used, never the upload time.
- **Filenames are kept as they arrived.** A clash in the same day folder gets a numeric suffix.
- **XMP sidecars sit beside their original** as `<filename>.xmp`, for example `IMG_1234.HEIC.xmp`. Using the full name stops a RAW and a JPEG with the same base name from sharing one sidecar.
- **Only two things ever move a file.** A date correction re-files it, through a recorded job. And a file moves from the inbox into the library. Classification such as AI or not AI, people or albums never moves files; the app shows those from the database.
- **The pattern is configurable per library** (`libraries.folder_template`). The default is `{person}/{type}/{yyyy}/{yyyy-MM-dd}/{filename}`.

**Indexed libraries** (`/libraries/<name>`) keep their own structure. Photostead never moves or writes into them; their sidecars go to `/data/sidecars`. At cut-over, a consolidate step copies their files into `library/` and checks each copy's hash. Only after that can the old folders be archived.

**Originals never live permanently on the cache pool** (decided 2026-10-16). The `photostead` share is set to *Primary storage: cache, Secondary storage: array, Mover action: cache → array*. New uploads land on the fast cache, and Unraid's mover puts them on the parity-protected array overnight. Bulk imports bigger than the cache's free space, like the initial migration, write straight to the array instead. The template and setup guide must say this, because a cache-only share has no parity protection.

**Why the split between array and cache.** Everything that can be rebuilt (thumbnails, previews, transcodes) lives on the fast cache pool. Everything that can't (originals, database dumps and the backup manifest) lives on the parity-protected array. Backups need the `photostead` share; `/data/derived` never needs backing up.

## Data model

Postgres 18. IDs are UUIDv7 (time-ordered, generated by `uuidv7()`). All timestamps are `timestamptz`. The tables are grouped by domain below, and only the important columns are listed.

### Accounts and spaces

| Table | Key columns | Notes |
|---|---|---|
| `users` | id, email, display_name, role (`admin`, `member`, `guest`), time_zone, settings jsonb | Guests have no library of their own. `time_zone` is an IANA name chosen at onboarding (decision 1.6); empty until chosen, when the server's zone is used |
| `households` | id, name | v1 creates exactly one per server |
| `household_members` | household_id, user_id, role (`owner`, `adult`, `member`) | |
| `credentials` | user_id, kind (`password`, `passkey`, `totp`, `oidc`), name, data, last_used_at | Passkeys via WebAuthn; see Signing in |
| `device_tokens` | id, user_id, kind (`app`, `extension`, `display`, `worker`), scope jsonb, last_used_at, revoked_at | Revocable per device; display tokens scoped to albums or people |
| `sessions` | id, user_id, device_token_id, expires_at | |

**Ownership model.** Every asset has one owner, the user who contributed it. A user's personal library is simply their owned assets. The Household space is explicit membership in `space_assets`, filled by auto-share rules and manual adds. Only the owner can permanently delete an asset, which is the Apple shared-library model from decision 6.1.

**The Household as built** (migration 00036, `internal/api/household.go`). One household per server, with one space; every account but a guest's joins it when it's made (a trigger; the first is its owner). The gate, `visible_assets(viewer)`, is the viewer's own items as before, plus what's in a space they're a member of from others (in the owner's timeline: not archived, hidden, in the vault or the trash), with a shared Live Photo's clip; each part of the union reads from its own index. So shared items show everywhere a member's own do (the timeline, search, memories, albums they make), and the timeline's switch narrows it to Everything, Mine or the Household (`library=mine|household`). Others' items can be viewed, favorited, marked, downloaded and put in one's own albums; changing or trashing them stays with their owner, and any member may take an item out of the space (`space_exclusions`: no rule puts it back). Each person has one auto-share rule (`share_rules`): their items from a day on (or all), with any of some people from their People (or anyone), AI-made images only if they say so, never screenshots, documents and the like or items stacked under another. It applies at once when saved (what it alone shared and no longer matches comes out), and every 10 minutes for new items. Moving an item into the vault takes it out of the space for good. The owner (or the server's admin) invites people with a link (`/#/invite/{token}`, good once for 7 days), where they choose their name and password, and sets roles (adult, member: stored for features to come). People stay each owner's own for now; naming faces once per household comes later.

| Table | Key columns | Notes |
|---|---|---|
| `spaces` | id, household_id, kind (`household`) | A table rather than a flag, so more shared spaces can come later |
| `space_assets` | space_id, asset_id, added_by, added_at, via_rule_id | |
| `auto_share_rules` | id, user_id, space_id, people ids, geofence, from_date, enabled | |

### Files and assets

A **file** is bytes on disk. An **asset** is the photo or video a person sees. One asset can have several files: the original, a Live Photo's motion part, sidecars, and copies of the same bytes found in two places.

| Table | Key columns | Notes |
|---|---|---|
| `libraries` | id, owner_id, kind (`managed`, `indexed`), root_path, read_only, sidecar_mode (`beside`, `shadow`, `off`), folder_template, watch | |
| `files` | id, library_id, rel_path, size, mtime, sha256, alt_hashes jsonb, mime, role (`original`, `motion`, `sidecar`, `companion`), state (`present`, `missing`, `changed`), first_seen_at, last_seen_at, last_verified_at | Unique (library_id, rel_path). `alt_hashes` holds Immich's SHA-1 for import matching |
| `assets` | id, owner_id, type (`image`, `video`), primary_file_id, sha256, taken_at, taken_offset_min, local_date, width, height, orientation, duration_ms, source_type, visibility (`timeline`, `archived`, `hidden`, `vault`), trashed_at, current_version_id, thumbhash, created_at, updated_at | `sha256` is the identity: the same bytes found twice make one asset with two files |
| `asset_metadata` | asset_id, make, model, lens, focal_mm, fnumber, exposure, iso, lat, lon, alt, place_id, description, title, raw jsonb | Normalized columns for filters; `raw` keeps every tag exactly as exiftool found it |
| `places` | id, name, admin1, country, lat, lon, geom | Offline geocoder (GeoNames); location never leaves the server |
| `asset_user_state` | asset_id, user_id, favorite, rating (0–5), flag (`pick`, `reject`), color_label | Per user (decision 3.1). Favorites for everyone; the rest shown only to users who turn them on |

**The visibility gate.** `store` exposes a single `visible_assets(viewer)` predicate that applies owner or space membership, `visibility`, `trashed_at` and vault unlock state. Every list, search, count, memory, share and AI export goes through it. A test asserts that no query reads `assets` directly outside `store`.

**`source_type`** follows IPTC Digital Source Type: `camera`, `screenshot`, `scan`, `download`, `ai_generated`, `ai_edited`, `composite`, `unknown`. It is set from evidence and can be corrected by hand. The UI's AI badge means `ai_generated` or `ai_edited` *with* a provenance record. `unknown` is the honest default.

**Time.** `taken_at` is the true instant and `local_date` is the day the timeline groups by. `date_basis` records how that day was decided, so it can follow the owner's time zone (decision 1.6):
- `recorded_offset`: the file says which offset it was taken at (`taken_offset_min`). The day is the day *there*. A photo taken at 23:30 in Tokyo belongs to that Tokyo day, whoever views it.
- `camera_clock`: a camera clock with no offset, or a date written in a file or folder name (decision 1.7). The day is the day the clock or name shows; the instant assumes the owner's zone.
- `instant`: a moment in time (QuickTime and Matroska UTC clocks, file dates). The day is the day that moment falls on in the owner's zone.

Changing a person's zone moves camera clocks to the new zone and re-dates instants; recorded offsets never change. Nothing uses UTC or the server process's clock as a default.

### AI provenance and creator tools

This is the AI pillar's backbone. The prototype's creator index (35,410 items, 259 models, 812 LoRAs, 6,692 prompts) is the shape to keep.

| Table | Key columns | Notes |
|---|---|---|
| `generations` | asset_id, source (`comfyui`, `a1111`, `c2pa`, `iptc`, `midjourney`, `grok`, `capture`, `ingest_api`, `manual`), evidence (where it was found, e.g. `png:prompt`, `mp4:comment`, `xmp:AIPromptInformation`, `c2pa:manifest`, `post:text`), tool, tool_version, model_id, prompt_id, negative, seed, sampler, scheduler, steps, cfg, gen_width, gen_height, workflow_blob, raw_blob, confidence (`embedded`, `declared`, `manual`) | One row per asset. Raw metadata is kept exactly as found, in `blobs` |
| `generation_loras` | asset_id, lora_id, weight | |
| `prompts` | id, owner_id, text, normalized_hash, first_used_at, last_used_at, use_count, starred, tags | Deduplicated by normalized text; the prompt library |
| `ai_models` | id, kind (`checkpoint`, `lora`, `vae`, `video`), name, file_hash, family, first_seen_at, last_used_at, use_count | The model & LoRA catalog |
| `characters` | id, owner_id, name, rule jsonb (LoRA family patterns, reference assets, prompt tokens), cover_asset_id | Separate from People (decision 5.6) |
| `character_assets` | character_id, asset_id, basis (`metadata`, `suggested`, `confirmed`, `rejected`) | Membership comes from metadata; face similarity may only add `suggested` rows |
| `captures` | asset_id, site, post_url, post_id, author_handle, author_name, posted_at, captured_at, post_text, alt_text | Photostead Capture attribution |
| `asset_links` | from_asset, to_asset, kind (`img2img_input`, `variation`, `upscale`, `edit_of`, `frame_of`, `remix`, `rerun`), created_by | Lineage |

AI generation batches (the same prompt and seed run) become `stacks` of kind `ai_batch`. The settings diff compares two `generations` rows. "Re-run" and "Remix" send `workflow_blob` back to ComfyUI through a worker, and the results link back with kind `rerun` or `remix`.

**What's built (2026-10-17).** `internal/provenance` reads the evidence in this order, and the first match wins:
1. A ComfyUI graph (PNG text chunks, video keys, or VideoHelperSuite's JSON comment). A graph with no generating node, such as an upscale or re-encode, isn't evidence.
2. Automatic1111/Forge/Civitai parameters.
3. Midjourney's prompt and Job ID.
4. Grok Imagine's download signature.
5. China's AIGC label.
6. Content Credentials whose digital source type is a trained model.
7. IPTC's digital source type, or Google's "Made with Google AI" credit.

Prompts, models and LoRAs (with weights) go to `prompts` and `ai_models`. Seeds are kept exactly, past 2^63. ComfyUI's graph and workflow are kept as found, for download now and re-runs later.
- **Prompts from importers (2026-10-25, migration 00048).** Some files prove they're AI but don't say what they were asked for: ChatGPT's pictures carry Content Credentials and no prompt. An upload's `prompt` (from an importer that knows, such as a script reading a ChatGPT data export) is kept in `given_prompts` and becomes the item's prompt only when its evidence shows it's AI and gives none. It never makes anything AI, never replaces a file's own prompt, and survives a newer parser reading the item again. A later upload's prompt replaces an earlier given one. A description brought in that only repeats an item's prompt, or its start (some apps keep them cut short, with line breaks spelled out as `\n`), isn't shown a second time; one written here always is.
- **At ingest** it reads exiftool's full output.
- **For items indexed earlier** a pass over `asset_metadata.raw` runs at every start (`provenance_version`). It reads no files.
- **Values over 8 KB are trimmed in the index**, which covers most ComfyUI graphs and workflows. Those items get a provisional ComfyUI generation and `provenance_needs_file`. `photostead provenance read-files` then reads those files on the `provenance` queue, one at a time.
- **First run on this library:** a sample of 5,000 items showed the parser finds every evidence type present. The trimmed items were 14,400, nearly all ComfyUI.

**Characters (built 2026-10-17).** `characters` holds a name and LoRA-name patterns (`loras text[]`). This is simpler than the `rule jsonb` sketched above, and prompt tokens and reference images can join it later. Patterns are matched in Go with RE2, against normalized names. `character_assets.basis` records why each item belongs:
- **metadata:** made with one of the character's LoRAs.
- **lineage:** a ComfyUI image-to-video clip whose graph loads, as a reference, an AI image of theirs. Text-to-video models are skipped, since they ignore start images. A reference names its start image by file name. The candidates are the AI images with that name taken before the clip, and the one that looks most like the clip wins (search embeddings): it must be at least 0.80 alike and 0.05 ahead of any other candidate. Until looks can be compared, a name that's unique in the library still counts (the prototype's rule).

  The prototype's rule found the start image of 4 clips in the owner's library. Names recur, and every same-named file counted against a match, including later and non-AI ones. On 2026-10-17 it finds 617 of the 636 clips that name one (17 weren't alike enough, 2 were too close to call). Only 19 of those start images belong to a character, so character links went from 10 to 19; most clips start from images made without a character's LoRA. Each clip's start image is kept in `generation_starts` (with how alike they look; empty when the name alone settled it) and shows as "Made from" in the viewer's Generation panel, read through the gate so a start image the viewer can't see isn't shown. Clicking it shows the image on its own in the viewer, with a way back to the clip. "More like this" around it wouldn't do: it leaves out the item it starts from, and finds nothing until looks can be compared. Because start images don't need a character, the refresh runs for anyone with a character or a ComfyUI video.
- **confirmed** and **rejected:** set by the owner. The matching never touches these.

Memberships are recomputed at start-up, every 15 minutes, after AI details are read, and on every edit. The 17 character families from the face benchmark were imported with `photostead characters import`.

### Edits and versions

| Table | Key columns | Notes |
|---|---|---|
| `edit_versions` | id, asset_id, parent_id, recipe jsonb, label, created_by, created_at, rendered_file_id | Non-destructive recipes (crop, light, color, masks…); any step can be revisited or deleted |

Generative edits produce a new version with a `generations` record of its own and `source_type = ai_edited`. Recipes are mirrored to the XMP sidecar.

Built so far (2026-10-25): one recipe per photo, in `asset_edits` (asset_id, recipe jsonb, orig_width, orig_height). A recipe turns (0/90/180/270), mirrors, crops (shares of the turned picture), and sets exposure, contrast, saturation and warmth. It's applied when the previews are made and to a full-size JPEG on download (`/assets/{id}/edited`); the original file is never written. An edit that only changes light and color keeps what was read from the picture (faces, text, the search embedding); one that turns or crops reads it again. Thumbnail and preview URLs carry a version from the ThumbHash, so cached copies of the old ones aren't shown. Versions and masks are still to come; the recipe goes to the XMP sidecar (`photostead:EditRecipe`). A video's frame can be kept as a photo (`/assets/{id}/frame`): filed in the share like an upload, dated when the moment was. And a part of a video can be saved as a new one (`/assets/{id}/trim`): copied without re-encoding, so it starts at the keyframe at or before the chosen time.

### Organizing

| Table | Key columns | Notes |
|---|---|---|
| `albums` | id, owner_id, parent_id, title, description, cover_asset_id, kind (`manual`, `smart`), query jsonb, hide_from_timeline, sort | Nested; smart albums store a saved search plan |
| `album_assets` | album_id, asset_id, added_by, added_at, position | |
| `album_members` | album_id, user_id, role (`viewer`, `contributor`, `editor`) | |
| `stacks` | id, kind (`raw_jpeg`, `burst`, `near_duplicate`, `ai_batch`, `manual`), cover_asset_id, state (`suggested`, `confirmed`) | RAW+JPEG and bursts are confirmed automatically; near-duplicates wait for the user |
| `stack_assets` | stack_id, asset_id, position | |
| `keywords` | id, owner_id, parent_id, name | Hierarchical (`Travel › Japan › Kyoto`) |
| `asset_keywords` | asset_id, keyword_id, source (`file`, `user`, `ai_suggested`) | |

Trash is `assets.trashed_at`, purged after 30 days by a job. The vault is `visibility = 'vault'` plus a PIN or biometric unlock held per session. The archive is `visibility = 'archived'`: `POST /api/v1/assets/archive` (`{assetIds, archived}`) moves the owner's own items there or back, with their Live Photo clips and a RAW+JPEG pair's RAW (not others' items, nor the vault's or the trash's); albums keep them.

- **The vault** (migration 00032). Items moved there are left out by the gate (`visible_assets`) and the trash (`trashed_assets`), so they're gone from the timeline, collections, albums (which keep them for when they come out), search, People (no cover face comes from one) and sharing. Local analysis still runs on them. Their owner sees them through `vault_assets(viewer)`, which the API calls with the viewer only for a signed-in session that unlocked the vault (`sessions.vault_until`), and with no one otherwise, so a locked vault reads as empty. It stays open 10 minutes after its last use, and the web app locks it on leaving it or the page. API keys never open it. The PIN (4 to 12 digits, argon2id, `users.vault_pin`) is set the first time with nothing else; a new one needs the current PIN or the account password, and locks the vault in every other session. From the fifth wrong PIN in a row each one makes the next try wait: a minute, doubling, up to an hour. Going in, items take their Live Photo clips and a RAW+JPEG pair's RAW, and leave other stacks. The vault's trash is its own: deleted vault items stay in the vault (Deleted), never in the main trash, and emptying the main trash doesn't reach them. Their files are served `no-store`. Immich's locked folder can't be read with an API key, so items locked there aren't put in the vault by the import.

- **Signing in** (migration 00034, `internal/auth`, `internal/api/signin.go`). A password, and then, with two-step sign-in on, a six-digit code from an authenticator app (TOTP: SHA-1, 30 seconds, a step either side for a clock a little off, no code accepted twice) or one of ten recovery codes (each good once; only their SHA-256 is kept). A password sign-in with two-step on gives a ticket (5 minutes, 5 tries) for the code, not a session. Passkeys (go-webauthn) are discoverable, so signing in needs nothing typed and no second step; a passkey belongs to the host name it was made at, and browsers offer passkeys only over HTTPS or on localhost (so not at `http://192.168.1.10:4800`). Single sign-on is OpenID Connect (go-oidc, with PKCE and a nonce), set up by `PHOTOSTEAD_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_NAME` (the button) and `_NEW_ACCOUNTS=on`; the return address is `PHOTOSTEAD_PUBLIC_URL` (else the address each visit came in on) + `/api/v1/auth/oidc/callback`. A provider's account is linked by issuer and subject: the first time, to the account with the same verified email (or a new member account when allowed), and it holds after that whatever the email; the trip's state is also kept in a cookie, so it finishes only in the browser that set off. A sign-in under way waits in `auth_flows`, found by a token whose hash is kept. Turning on two-step, adding a passkey, linking single sign-on and making new recovery codes ask for the password again (an account made by single sign-on has none); the last way to sign in can't be removed.

- **Memories** (migration 00033, `internal/api/memories.go`). Made when asked for, from the viewer's items through the gate, so nothing is stored but what's kept out. "On this day" is each earlier year's same day (February 29th is the 28th in other years), or, when no year has anything on the day itself, the week around it. One named person or pet (shown, in six or more items over two months or more) is remembered over the years, a different one each day. A memory shows its best 24: favorites, stars, picks, named people, a date from the file's own metadata rather than when it was copied, then a shuffle that changes daily. Screenshots, documents, receipts, ID cards and QR codes, rejects, items stacked under another, and the vault are never in one; AI-made images only if the viewer's settings say so (off by default: on this library, AI images would outnumber everything else). Each person keeps their own: the strip above the timeline on or off (`users.settings.hideMemories`, `memoriesAi`), days and stretches of days kept out (`memory_hidden_days`, which "Not this one" on a card adds), and people and pets kept out (`people.in_memories`), which leaves out every item they're in. Trips can now build on places (not yet): on this library only 6 items have a location.

**Trash, favorites and albums (built 2026-10-17).**
- **Trashing.** "Delete" (the selection bar, the viewer, or the Delete key) sets `trashed_at` on the owner's items, along with a Live Photo's clip and the rest of a stack it tops. They come back together. The gate hides trashed items everywhere. The Trash view reads them through `trashed_assets(viewer)`, its counterpart. The asset detail serves the owner's trashed items too, so the trash can be looked through.
- **Deleting for good.** "Empty trash", "Delete forever", or the nightly job at 5:00 for items trashed 30 days ago (`internal/trash`) deletes only Photostead's own copies: files in writable, managed libraries, under the library root, and the renders. Files in read-only indexed folders are never touched. The item stays as a record (`assets.purged_at`), with its deleted files marked `deleted` (rescans leave that alone). A copy still in an indexed folder can't bring it back. An item whose file can't be removed stays in the trash.
- **Favorites and albums.** Favorites (F) and albums (the Album picker: an existing album or a new one; "Remove from album" inside one) act on items the viewer can see through the gate. Every edit offers Undo.
- **Albums inside albums.** An album can sit inside another (`albums.parent_id`) and hold items of its own as well. Albums shows the top level. An album's page shows the albums inside it, where it sits ("Albums › Trips › Japan"), and actions to add an album inside, rename, move (never into itself or one of its own) and delete. Deleting an album removes only the album: its items stay, and the albums inside it move up. The Album picker lists every album by its path.
- **Smart albums** (`albums.kind = 'smart'`, rules in `albums.query`) are saved filters. Their items are whatever their rules pick from the timeline, kept up to date. Rules: photos or videos, favorites only, at least N stars, a flag, a color, all of some people, any of some keywords, a date range, and made with AI / not AI / taken with a camera. The library timeline takes the same filters. The viewer's own filter narrows a smart album further, and "Save as smart album" in the filter menu keeps the filter in use. Nothing can be added to a smart album by hand.
- **Stacks** (`internal/stacks`, migration 00028) show items that belong together as one, the rest tucked under the top. Besides RAW+JPEG pairs:
  - **AI batches:** images (or clips) from one prompt and model, each made within 3 minutes of the last. They're stacked on their own, with the favorite or best-rated on top. On this library that was about 2,400 stacks, holding 11,400 images.
  - **Bursts:** a camera's shots a second or less apart, or sharing the burst identifier it wrote, three or more. Stacked on their own.
  - **Look-alikes:** real photos whose search embeddings are 0.99 alike or more (the same picture saved twice, say; at 0.97 similar but different shots came in). Photos go only with photos, and videos only with videos that run as long (within half a second or 1%), since different videos often open on the same frame. Blank pictures are never look-alikes: an item whose ThumbHash has a brightness spread under 6 (of 255), such as a video whose poster is a black frame or a plain title card, looks like every other blank one. These are only suggested. The Look-alikes page offers stack them, keep the best and trash the rest, or not look-alikes, group by group, and Stack all to stack every waiting group at once (best on top, undoable; there's deliberately no bulk trash). A job compares new items every 30 minutes, through the HNSW index.
  - **By hand:** the selection bar's Stack.
  - **Changing them:** the viewer shows an item's stack, with put on top, take out, and unstack. Whatever the owner takes out or keeps apart stays out (`stack_exclusions`).
  - **Only a RAW+JPEG pair moves together.** It goes into and out of the trash as one, moves in time as one, and is re-filed as one. Trashing the top of any other stack trashes just that item, and the stack gets a new top.
- **Folders** is a read-only view of the folders on disk, per library: the folders inside each (with how many photos and videos are under them), and the items directly in one (`collection=folder:{library}` with `folder=`). A path can't climb out of its library.
- **Stars, flags and color labels** (migration 00026) are each person's own, in `asset_user_state`, like favorites. They show only for those who turn them on (Ratings & flags in the rail; `users.settings.organize`). Keys follow Lightroom: 0–5 stars, P pick, X reject, U unflag, 6–9 red, yellow, green, blue. They work on the item in the viewer and on a selection (and the selection bar's Mark menu). Tiles show stars, a pick flag and a color strip; rejects are dimmed. The timeline and collections filter by at least N stars, picks, rejects or "hide rejects", and a color. They come in from a file's own XMP or EXIF rating and label when it's first indexed (XMP sidecars aren't read yet), and from Immich's ratings. Their owner's are written to the item's XMP sidecar (see XMP sidecars below).
- **Bulk edits** (the Edit dialog, from the selection bar or the viewer) touch only the owner's items:
  - **Keywords:** put on or taken off many items at once, matched by name ignoring case.
  - **Descriptions:** one description for all of them. It's shown instead of the file's or an import's, and an empty one hides them.
  - **Dates:** move items in time by an amount, or set when the earliest was taken and move the rest with it, so their spacing is kept. Live Photo clips and stacked versions move along. The date's source becomes `user`, so nothing read from files changes it again.
- **Re-filing** (`internal/refile`, migration 00027). After a date change, a `refile` job moves the item's files in the managed share to the new day's folder (`photos|videos/<year>/<date>/`), together with each one's `<name>.xmp`. Live Photo clips go with their still into photos/, and stack members go with their top item. A name already taken there gets "-2", "-3"…. Emptied day and year folders are removed. Indexed folders (another app's) are never touched, and neither is a file kept anywhere but a day folder. Each move is written down in `refiles` before it's made. The move itself is one transaction: it points the file's row at the new path, marked as seen now, and only then renames the file. So a scan running alongside never counts it as missing or as a new file, and a move cut short by a restart is finished, not repeated.

**XMP sidecars (built 2026-11-08, migration 00052, `internal/sidecar`; decision 1.4).** What an item's owner adds in Photostead is written to an XMP sidecar named `<file name>.xmp`, so Lightroom, digiKam, darktable or Bridge, and a future without Photostead, keep it. Originals are never written to. Where the sidecar goes is the library's `sidecar_mode`:
- **`beside`** (the managed share's default): next to the original.
- **`shadow`** (indexed libraries, and any read-only one whatever its mode says): `/data/sidecars/<library id>/<path>.xmp`. It starts from a copy of a sidecar another app left beside the original, if any.
- **`off`**: none are written.

`photostead library sidecars -name N beside|shadow|off` changes the mode, and existing files are left where they are. The sidecar holds:
- the owner's stars, as `xmp:Rating`, with a reject as -1 as in Lightroom;
- their color label (`xmp:Label`, e.g. Green), favorite and pick or reject (`photostead:Favorite`, `photostead:Flag`);
- keywords: `dc:subject` gets the last part of each, and `lr:hierarchicalSubject` the whole path, e.g. `travel|japan`;
- the description (`dc:description`), and a date the owner set (`photoshop:DateCreated`, `exif:DateTimeOriginal`);
- the named people in it (`Iptc4xmpExt:PersonInImage`);
- how an AI item was made, in IPTC 2025.1's fields (`DigitalSourceType`, `AISystemUsed`, `AIPromptInformation`), plus `photostead:AIModel`;
- a photo's edit recipe (`photostead:EditRecipe`).

Photostead's namespace is `https://photostead.com/ns/xmp/1.0/`, declared in an exiftool config. exiftool does the writing (`-ec`, so a line break in a description stays one argument). Every owned tag is written each time, empty ones removed, so whatever another app put in the sidecar stays.

Triggers queue an item (`sidecar_queue`) when its marks, keywords, description, edit, given prompt, faces, a date the owner set, or a named person's name change. A job writes the queue every 5 minutes (`photostead sidecars write|status` by hand). It skips an item whose sidecar would come out the same (`sidecar_writes.hash`), and items in the vault. A change made while an item is being written queues it again. An item whose only metadata is what its own file carries gets no sidecar. Shadow sidecars live on the data volume, so a backup of `/data` should include `sidecars/`.

### People, pets and faces

| Table | Key columns | Notes |
|---|---|---|
| `faces` | id, asset_id, bbox, landmarks, quality, detector, embed_model, embedding halfvec(512), in_ai_media, person_id, assignment (`auto`, `suggested`, `confirmed`) | Detected with YuNet, embedded with AuraFace (benchmark result). Embeddings are tagged with their model, so a later model can replace them |
| `people` | id, household_id, kind (`human`, `pet`), species, name, visibility (`household`, `private`), owner_id, cover_face_id, birth_date | Named once per household |
| `person_references` | person_id, face_id, age_bucket | Several reference faces per person, grouped by age |
| `face_constraints` | face_id, person_id, kind (`is`, `is_not`), created_by | User merges and splits; re-clustering can never override them |
| `person_constraints` | person_a, person_b, kind (`different`) | |

Clustering runs on real-photo faces only (`in_ai_media = false`). It uses the benchmark's winning setup: AuraFace with mirror averaging and the streaming average-of-3 rule, which doesn't chain. Faces join a person at distance 0.45, calibrated so that baby and toddler photos aren't misfiled. Faces in AI media never join People clusters; they can only produce Character *suggestions* at a strict threshold.

As built (migration 00022, `internal/faces`, `worker/photostead_worker/faces.py`):

- **Workers find the faces.** A `faces` task gets the item's preview. YuNet (2023mar) finds faces, which are aligned to ArcFace's 112 px template and embedded with AuraFace v1 (the face and its mirror, averaged). Each face is saved with its box and landmarks (fractions of the picture), detector score, size, yaw and blur. `face_scans` records every item looked at, including those with no faces.
- **The server groups them** (`group_faces`, every 20 minutes when there's something new, and right after a correction). It loads an owner's faces in real photos, oldest first, and runs the streaming average-of-3 rule: threshold 0.45, K 3, founding a person needs 3 faces that pass the strict gate (64 px or more, score 0.8 or more, yaw within 0.3, not in the blurriest tenth). Faces that fail the gate can still join an existing person. Grouping is incremental: faces it has already looked at keep their places, and only groups reached from new faces are rechecked. The Go port gives the same groups as the benchmark's Python on Gallagher, FG-NET and LFW.
- **Names come from an import when they agree.** A new group takes an imported person's name (from the Immich importer) when at least 3 of its items, and at least half, are ones the import suggested for that person. Otherwise it's an unnamed person (`origin = 'faces'`) until the owner names it.
- **Who is in what** is the `person_items` view: faces decide for every item looked at for faces. Imported suggestions stand only for items not looked at yet.
- **Corrections hold.** Renaming, hiding, merging ("same person"; a name that's already taken offers this), and "Not this person" (`face_rejections`). Grouping never puts a rejected face back with that person, and a merge carries the rejections over. Faces the owner placed (`confirmed`) never move.
- **Faces in AI images** are kept but never grouped into People.
- **A group can be a character** ("It's a character"): in an AI-heavy library many groups are AI characters whose files lost their generation metadata. The group leaves People (`people.character_id`), and every item with its faces, including faces grouped with it later, is the character's (`character_assets.basis = 'faces'`, kept in step after each grouping run). "Not this character" holds, and "Faces back to People" undoes it.
- **Someone new must be seen in two items.** Two faces (or animals) in one picture are two different people (or pets), so a group found in a single item isn't founded.
- **People and pets seen in fewer than 5 items** and not named are tucked behind "Show more" on the People page.
- **Pets** (migration 00024, `worker/photostead_worker/pets.py`). A `pets` task gets the preview of each real photo (AI images are skipped). RF-DETR Nano (Roboflow's COCO weights, Apache-2.0, as converted to ONNX by Hugging Face's ONNX community) finds cats and dogs: sigmoid scores of 0.3 or more, with birds and farm animals scored too so a horse or cow isn't taken for a dog. Each animal's whole-body crop is embedded with open-noodle's pet-recognition-base (Apache-2.0; 92% top-1 on held-out cats, 61% on dogs). Animals are stored in `faces` with their `species`, so pets get grouping, corrections, covers and "who is in what" for free. Each species is grouped on its own at threshold 0.5. Detections scoring under 0.5 are left out of grouping (on this library, search agreed that fewer than a quarter of them show a cat or dog). Founding a pet needs detections of 48 px and 0.7, in at least two photos. Unnamed pets show as suggestions ("Name this cat"); naming confirms them. A pet merges only with a pet of the same species.

### Search

| Table | Key columns | Notes |
|---|---|---|
| `asset_embeddings` | asset_id, model, embedding halfvec(n) | One row per model, so a model upgrade re-embeds side by side and switches over when done. HNSW index per model |
| `asset_text` | asset_id, filename, description, ocr, caption, prompt, keywords, tsv tsvector | Full-text + trigram; captions marked with their model |

The Ask and search planner from the prototype moves into `search` unchanged in shape. A query becomes a plan: people, character, model or LoRA, prompt text, OCR text, filename, place, dates, media type, favorites, plus a "looks like" description. The plan runs as SQL filters combined with vector similarity, using pgvector's iterative index scans so filters and vectors work together. Results are ranked with reciprocal rank fusion. "Never a dead end" relaxation and follow-up merging come with it. An LLM is an optional planner that must output the same plan shape.

### Places and the map

**Built 2026-11-08 (migration 00053, `internal/places`, `web/src/Places.tsx`).**
- **Naming places, offline.** Every 30 minutes (and with `photostead places`) items with a location are matched to the nearest town within 60 km in GeoNames' list of places with at least 1,000 people (CC BY 4.0), with its region and country. The list goes in `places` (one row per town used) and `asset_metadata.place_id`. The three GeoNames files (32 MB) are downloaded once into `/data/geodata`, the first time any item has a location. They're matched in memory on a one-degree grid that reaches as far as 60 km does at that latitude and wraps at the date line. No location leaves the server. A changed location (a trigger on `lat`, `lon`) is named again, and so is every item when `places.Version` goes up.
- **The Places page.** Browse › Places shows a map, or a list of places by country, each with its newest item as a cover. A place's page is the collection `place:{id}`, and the viewer's Location links to it. All of it goes through the gate, without adult items for a viewer who hides them.
- **The map.** It's MapLibre GL 6, loaded only when the map is shown (a 300 KB chunk, its worker a file of its own), with every located item from `GET /api/v1/map`. Nearby items group into circles showing the newest one's photo and a count. A click zooms in, and an item's photo opens it.
- **Tiles.** They come from OpenFreeMap's Positron and Dark styles (free, no key, no tracking), following the app's theme. The tile server sees the areas looked at, never where items are. `PHOTOSTEAD_MAP=off` turns the map off (places are still named). `PHOTOSTEAD_MAP_STYLE_LIGHT`, `_DARK` and `_HOSTS` point it elsewhere, such as a self-hosted Protomaps. The content security policy allows only the style's origins, plus `worker-src blob:`.

### Jobs, workers and AI calls

| Table | Key columns | Notes |
|---|---|---|
| `river_job` and friends | (River's own schema) | Server-side jobs: scanning, metadata, thumbnails, stacks, purge, scrub, sync compaction |
| `worker_tasks` | id, kind, asset_id, input jsonb, needs jsonb (capabilities), priority, state, lease_owner, lease_expires_at, attempts, model, model_version, result jsonb | Model work for workers, claimed with `FOR UPDATE SKIP LOCKED` |
| `workers` | id, token_id, name, capabilities jsonb (GPU, VRAM, models, NVENC), last_seen_at, state | |
| `ai_calls` | id, user_id, feature, provider, model, asset_ids, bytes_sent, cost, at | Cloud AI audit log |

The worker protocol is pull-based:
1. **Claim:** `POST /api/worker/v1/claim` with capabilities returns a batch of leased tasks.
2. **Fetch:** the worker downloads renditions, never originals unless the task needs them (transcoding).
3. **Report:** it heartbeats while working, posts results, and releases unfinished tasks.

Leases expire, so a worker that sleeps or goes offline costs nothing but time. New uploads take priority over backfill.

**GPU arbiter (worker side).** The default checks free VRAM through NVML before loading models. The Mission Control adapter reserves VRAM, heartbeats, releases with `linger_s` while more tasks are queued, and requeues on preemption. It also reuses the Legion's llama.cpp router for VLM captions. A worker holding a GPU lease must refuse to fall back silently to CPU (found by the face benchmark).

**What's built (2026-10-17): search by meaning.**
- **The worker.** `worker/` is a Python package (`photostead-worker`, ONNX Runtime) that runs SigLIP 2 base (patch16, 256 px, `onnx-community/siglip2-base-patch16-256-ONNX`, fp32). It's small enough for query text and new items on a modest CPU. Every worker uses the same weights, so embeddings always agree.
- **Inside the container,** the server starts and supervises `photostead-worker builtin` with a fresh token at each start. It runs the text service for queries on 127.0.0.1:4801, plus a gentle CPU worker (2 threads; `PHOTOSTEAD_BUILTIN_EMBED=off` turns it off).
- **GPU workers** (`photostead-worker run --gpu`) work only inside a Mission Control lease: resumable, never pre-empting. They give the lease back when the queue is empty or another app is allowed to take over.
- **Tokens and tasks.** Workers authenticate with tokens from `photostead worker add`, kept only as SHA-256 hashes. `worker_tasks` replaces the sketch above with `kind`, `asset_id`, `model`, `priority` and leases. A worker can only read inputs for tasks it holds.
- **Storage.** Embeddings go in `asset_embeddings` (`halfvec`, an HNSW index per model).
- **Search.** It runs through the gate with pgvector's iterative index scans; on the dev library it takes about 7 ms for 300 results. Meaning results come best first, in one group without dates. The app falls back to file names whenever meaning search isn't ready, and "More like this" uses an item's own embedding.
- **Text in pictures (OCR).** The same workers read text with RapidOCR (PP-OCR small detection and recognition models, Apache-2.0; `kind = 'ocr'`, engine `rapidocr-ppocrv6`). They read from the 1600 px preview rather than the thumbnail, and keep lines read with confidence 0.8 or more. The models download on first use into the models folder. Workers claim search embeddings first, then OCR in small batches (16 on a CPU), so a slow CPU never outlives its lease. Text goes in `asset_ocr`, one row per item, and `''` means read with nothing found. It's indexed with `to_tsvector('simple')`, so names, brands and any language match as typed. This stands in for the `ocr` column of `asset_text`, which was only sketched. The viewer shows it under "Text in the picture."
- **Utility collections (built 2026-10-17).** `internal/labels` sorts items into Screenshots, Documents, Receipts, IDs and QR codes using evidence already at hand:
  - the file name (a screenshot's name in several languages: basis `metadata`);
  - the search embedding compared with short descriptions of each kind, plus everyday kinds ("a photo of food", "a portrait of a woman") so a portrait can't win as an ID card for lack of competition (zero-shot with the same SigLIP 2 model: basis `visual`);
  - the text in the picture. A document needs some text. Receipt words make a page a receipt, and ID words let a weaker ID match count.

  AI-generated items and videos are never labeled. Labels live in `asset_labels`. Corrections (`confirmed`, `rejected`, from "Not a screenshot" in the viewer or the selection bar) outlive every re-sort; bumping `labels.Version` recomputes only the automatic ones. An item waits until its text has been read, and `labels_checked` makes sure it's looked at again once it has. The job runs every 15 minutes (`photostead labels refresh|status` by hand). "AI-generated" and "Captured by me" (camera or phone details) are collections of what the index already knows. The rail's "In the timeline: hidden" keeps the five labeled kinds out of the timeline (decision 4.3); it adds about 2 ms.
- **Adult content (built 2026-11-08, migrations 00050 and 00054, `internal/labels/adult.go`, `worker/photostead_worker/adult.py`).** Workers rate every rendered item with Freepik's EVA-based NSFW classifier (EVA-02 base at 448 px, MIT; `kind = 'adult'`, engine `freepik-eva02-nsfw-v2`). It gives four probabilities, which Freepik calls neutral, low, medium and high. Checked by eye on 90,000 photos of a real library, low is swimwear, tight clothes and cleavage, medium is lingerie, bikinis and revealing outfits (rarely nudity), and high is nudity and sex. Freepik trained it on 100,000 images, AI-made ones included, and on AI images it beats the other open models by a wide margin. It runs on ONNX Runtime from a pinned, checksummed conversion that matched Freepik's PyTorch weights to five decimal places on 40 library pictures. AI images are rated too (in this library most adult pictures are AI-made). A video is rated by its poster plus frames spread through it (6, or 2 under three seconds; `input?frame=n`, cut by the server with ffmpeg at 640 px), with every picture's probabilities kept in `adult_scans`. The server labels each picture **adult** when high is nearly certain (0.85) or when some high (0.05) comes with a real share of medium (0.15, and the two at even odds), which is how nudity rates. A close-up of skin, an eye or a hand can rate high with nothing in medium, torn between neutral and high, and is neither. A picture is **suggestive** when low and medium together are at even odds. (Freepik's own detector calls medium and up adult. On this library that made half of the adult items lingerie and bikinis.) In a video two pictures must agree, or one when only two were looked at, so one odd frame of a music video doesn't decide it. `adult_scans.rule` records which version of the rule labelled each item (`labels.AdultRule`): a new rule relabels everything from the kept probabilities on the next queue run, with no new scan. The labels go in `asset_labels` (basis `visual`) beside the utility labels, so "Not adult" and "Label as… Adult" corrections (`confirmed`, `rejected`) outlive every re-scan, and marking an item adult drops its suggestive label. On a CPU it takes about 0.2 s a picture; a 440-item sample took 4 minutes on a Mac and found 29 adult and 32 suggestive. Each person chooses in the rail (`users.settings.adult`): **shown** (the default), **blurred** (tiles blurred with a mark, the viewer asks before showing, videos don't play on hover), or **hidden**. Hidden leaves adult items out of the timeline, smart albums, albums and other collections (in SQL, so counts and months add up), and out of search, Ask, More like this and Recently added (after the read). Adult and suggestive items are listed on one Adult page, with a switch between the two lists. Memories never include adult items. The setting rides in the session (read with the session's user), so API keys and the sync feed see everything. The sync feed marks adult items (`adult`; an `asset_labels` trigger, migration 00057, sends an item again when it's found adult or no longer is), and the iPhone app hides or blurs them itself by the setting, which it reads from `/auth/me` and can change in its Settings (since 2026-11-13). People's faces and the web's album covers don't blur yet.
- **Recently added (built 2026-11-06, migration 00049)**: what the timeline shows, newest-arrived first (`assets.created_at`: uploaded, saved by Photostead Capture, imported, or found by a scan), not by the date it was taken, so a saved post from years ago is easy to find. `GET /api/v1/timeline/recent` gives a page (500 by default, up to 2000) and a cursor for the next; the web app shows the newest 1,000, grouped by the day they arrived ("Added today", "Added yesterday", the weekday, then the date). The page is picked first and only its items filled in: about 70 ms on a 100,000-item library.
- **Ask (built 2026-10-17).** `internal/ask` is the prototype's deterministic planner, ported to Go: RE2 has no lookarounds, so free-text phrases are cut at the next preposition in code. A question becomes a plan made of:
  - intent (list, count, first, last), dates in the viewer's zone, media type, favorites;
  - people and characters by name (a name that's both goes to the character when the question is about AI work);
  - models and LoRAs (a family name or any catalog name), where it was made (ComfyUI, A1111, Midjourney, Grok, OpenAI, Google);
  - prompt words, text in the picture, file names and utility kinds;
  - whatever is left, which describes what the items look like.

  `POST /api/v1/ask` runs the filters as one gated query, each filter an uncorrelated `IN` that Postgres hashes once. When there's a description, the filtered items (up to 20,000) are ranked by meaning. An empty answer tries dropping each filter and offers the one that finds the most ("never a dead end"). Follow-ups send the last plan back: "only favorites", "and in 2024" and other short phrases refine it, and a new subject or a whole new question starts over. Chips drop filters. The command bar's Ask button switches between searching and asking.
- **No JIT.** Photostead's connections turn Postgres's JIT compiler off. A scan of the whole library crosses its cost threshold, and compiling then takes longer than the query: the rail's counts took 450 ms with JIT and 50 ms without.
- **Hybrid ranking.** A search by meaning fuses four ranked lists with reciprocal rank fusion (k = 60): meaning, prompt words, text in the picture and file names. An item found more than one way rises, and an exact word is never buried under look-alikes.

### Sharing and activity

| Table | Key columns | Notes |
|---|---|---|
| `share_links` | id, token_hash, target (album or asset set), password_hash, expires_at, max_download_bytes, allow_upload, strip_location, watermark | GPS and sensitive EXIF stripped by default |
| `comments`, `reactions` | id, album_id or asset_id, user_id, body or kind, at | |
| `activity` | id, actor_id, verb, object, at | Feed and notifications |

Built so far (2026-10-25): `album_comments` (on an album or an item in it) and `album_picks` (each person's picks, for proofing). An album's activity is its comments and who added what, grouped by day. Anyone the album is shared with, guests through their personal links included, can comment and pick; the owner sees everyone's picks, others their own. Share links have a QR code and a printable card; a photo downloaded through a link has its location taken out (exiftool, from stdin), and the download fails rather than send it with one.

### Sync, uploads and imports

| Table | Key columns | Notes |
|---|---|---|
| `changes` | seq bigserial, user_scope, entity, entity_id, op, at | Change feed. Apps stream changes after their checkpoint as JSON Lines, deletes included and audited |
| `device_checkpoints` | device_token_id, entity, seq | Per-entity checkpoints |
| `uploads` | id, user_id, device_token_id, length, offset, claimed_sha256, state, temp_path | IETF resumable uploads with a tus layer; parts under 100 MB |
| `device_assets` | device_token_id, device_asset_id, asset_id, sha256, verified_at | Backup state; "free up space" only after a server-verified checksum |
| `imports`, `import_map` | source (`immich`, `takeout`, `apple_photos`, `sidecar`), source_id → asset_id | Importing again updates what an earlier import brought, rather than duplicating it |

Built so far (2026-10-25): the web app uploads files and whole folders (drag and drop, or pick), three at a time, skipping ones the library already has by their SHA-256. Google Takeout's JSON and XMP sidecars (Apple Photos exports, Lightroom and others) are read in the browser and sent with each file as its date, description, keywords, album, favorite, archive, stars and place; the server applies them once the file is an item, and only the owner's own uploads may set favorites, archive, stars and places.

## Key flows

### Ingest

1. **Discover:** a folder scan, the file watcher, an upload, an importer or the capture extension produces a file.
2. **Hash:** compute SHA-256 and look up `assets.sha256`. A known hash just adds a `files` row; a new hash creates an asset.
3. **Read metadata:** exiftool reads everything into `asset_metadata.raw`. The provenance parsers read PNG text chunks, video container tags, XMP/IPTC AI fields, C2PA manifests and capture post text, then write `generations` and set `source_type`.
4. **Render:** a ThumbHash, a WebP thumbnail around 250px and a 1440px preview, made in the asset's own color space. Full-size renders happen on demand. Videos get probed, and a small H.264 copy is queued only if browsers can't play the original directly. A video's poster is the first frame that isn't blank (judged by its ThumbHash, as look-alikes judge items) at one second in, then a tenth, a quarter and half of the way through; one that's dark throughout keeps the one-second frame. A re-render that changes an item's picture drops what was read from the old one (search embedding, text, faces, pets, labels, look-alike comparison) so the workers read the new one. `photostead render -blank-posters` redoes the videos rendered before posters skipped blank frames.
5. **Queue model work:** search embedding, faces, OCR, and captions if opted in. New uploads go ahead of backfill.
6. **Group:** RAW+JPEG, burst and AI-batch stacks; near-duplicate suggestions after embeddings exist.
7. **Publish:** write to `changes` for sync and to the activity feed; apply auto-share rules.

Changes made outside Photostead are caught by the watcher plus periodic rescans: size and mtime first, then a re-hash. A changed original becomes a new file version and never silently overwrites history.

**Videos every browser plays (built 2026-11-08, migration 00051, `internal/videos`, `worker/photostead_worker/transcode.py`).** Every 10 minutes (and with `photostead videos probe`) new videos are probed with ffprobe, reading headers only, and the result is kept in `video_probes`: container, codec, bit depth, color transfer, sound. One that isn't H.264 8-bit 4:2:0 SDR in MP4 or QuickTime with AAC or MP3 sound gets a `transcode` task. That covers HEVC from phones, AV1, MPEG-4 Part 2, ProRes, MKV and AVI, 10-bit, HDR, and AC3 or PCM sound. Any worker with ffmpeg takes the task (`kind = 'transcode'`, recipe `h264-1920-sdr-v1`). It fetches the original (`/tasks/{id}/original`) and makes a copy: H.264 High, 8-bit, at most 1920 px on the long side, AAC stereo, index first. It sends the copy back with `PUT /tasks/{id}/copy`, which keeps it only if it probes as playable, at `derived/…/{id}.play.mp4`. A GPU worker encodes with NVENC (falling back to libx264 if NVENC refuses), and a CPU worker, including the container's own, with libx264. HDR (PQ, HLG, Dolby Vision without a base layer) is tone-mapped to SDR with zscale where the ffmpeg build has it; the GPU box's does. A video whose picture is already fine, and only its sound isn't, keeps its picture as it is and gets new sound (seconds, not minutes). Long or big videos (over 5 minutes or 500 MB) wait for a GPU worker when one has been seen this week. A worker keeps a long task alive with `/tasks/{id}/extend`. On the dev library, 11 of 208 videos needed a copy, and a Mac's CPU made all 11 in 70 seconds. The viewer gets the original's type with its codecs (`video.type`, e.g. `video/mp4; codecs="hvc1.2.4.L150.B0, mp4a.40.2"`) and plays the original when the browser says it `probably` can (Safari and HEVC, Chrome and AV1), else the copy from `/assets/{id}/play`. It also switches to the copy if the original fails. Hover previews play the copy whenever there is one, since it's smaller and sure to play. Copies go with an item when it's deleted for good. Share links' pages and the iPhone app (whose player takes HEVC and HDR itself) still play originals.

**The folder watcher (built 2026-10-17).** Indexed libraries are watched by default (`libraries.watch`, `photostead library watch -name N on|off`); the managed share isn't, because Photostead records what it writes there.
- **How it works.** One inotify watch per folder; about 25,000 for this library, against Unraid's limit of 524,288. A changed folder is scanned once it has been quiet for 10 seconds, or after 2 minutes if it keeps changing. Only that folder is scanned, or that folder and everything in it when a folder appears or goes. A file still being written is retried until it settles.
- **What it sees on Unraid.** Tested on the server: a watch through a share (`/mnt/user/...`) sees anything written through the share, such as network copies and apps using `/mnt/user` paths. It doesn't see writes made straight to a disk path such as `/mnt/disk1` or `/mnt/cache`. A watch on a disk path sees everything written there.
- **Safety nets.** At start-up, folders whose time changed since the watcher last ran (`libraries.watched_at`, a 5-minute heartbeat) are scanned, so files added while the server was down aren't missed. A nightly rescan of every watched library at 4:30 in the server's zone catches anything else. That includes a burst of changes too big to follow one by one, which also triggers an immediate rescan.
- **Removed files** are marked missing, never deleted.

### Timeline at a million assets

- The timeline API returns month buckets with counts, then the assets for a requested month window.
- Bucket counts per viewer are kept in `timeline_buckets`, updated by the jobs that change visibility, so they are never a full scan.
- The browser lays out justified rows from each asset's width and height, with ThumbHash placeholders. It only renders the windows near the viewport.

### Moving in from Immich

1. Mount Immich's upload folder read-only under `/libraries/`, so its files are indexed in place. Nothing is copied or moved.
2. Import through Immich's API with a read-only key (see "How the import works" below). Assets in Immich's own upload folder are matched by its content SHA-1, kept in `files.alt_hashes`; assets in its external libraries by path, because for those Immich stores a SHA-1 of the path, not of the contents.
3. Photostead's own face recognition runs as usual. Immich's face assignments come in as suggestions only, never as confirmed facts.
4. Run both side by side for as long as you like, then move the files into the photostead share.

**Uploads and API keys (built 2026-10-17).**
- **API keys.** Other programs act for a person with an API key from `photostead apikey add -name …`. It's shown once, kept as a SHA-256 hash, and revocable by name. Operations that accept one say so (the `key` security scheme).
- **Uploading.** `POST /api/v1/uploads` takes multipart: an optional `meta` JSON part (fileName, takenAt, description, sourceUrl, keywords, album; from importers also favorite, archived, rating, latitude, longitude and an AI item's prompt), then the `file`.
  - It streams into the person's managed library under `.uploads` and checks the SHA-256.
  - A new file is filed by day like the rest of the share (`photos/2026/2026-10-12/name.png`, dated from `takenAt`, which also becomes the file's time) and goes through the normal ingest job.
  - What came with it (`uploads`) is applied once the item exists: the description (with the source link), keywords, and the album, which is found by title or made.
  - The same bytes again add only their description; there's no second copy.
- **Resumable uploads (built 2026-10-22)** for phones and big videos, by the IETF draft (draft-ietf-httpbis-resumable-upload-14, interop version 10; `internal/api/resumable.go`). `POST /api/v1/uploads/resumable` makes one (`Upload-Complete`, and `Upload-Length` when known); its address comes back in `Location`, early in a `104` for a client that sends `Upload-Draft-Interop-Version: 10`. `PATCH` there with `Content-Type: application/partial-upload` sends the next piece from `Upload-Offset`; `HEAD` says how much arrived; `DELETE` gives up; `OPTIONS` gives the limits. Through Cloudflare (a `CF-Ray` header), or everywhere when `PHOTOSTEAD_UPLOAD_PIECE` is set, `Upload-Limit` carries `max-append-size` (64 MiB, under Cloudflare's 100 MB a request): the app then sends a big file in pieces, and iOS's own uploads leave files bigger than that to it. What comes with the file goes in a `Photostead-Meta` header (base64 JSON, the multipart upload's meta part), with `sha256` to check the whole file against. The bytes so far are a partial file in the share's `.uploads` (its size is the offset), and `upload_sessions` holds who it's for and what came with it, for three days. A request on an upload first ends one still holding it (a dropped connection not noticed yet). Once complete it's filed like any upload, with the same reply, which is kept to answer a retry of the last piece. The address is a credential, and the owner's key or session is checked on every request too. Not through Funnel: phones of household members use the tailnet. iOS's own background uploads (Photos' upload jobs, iOS 27) speak an earlier version of the draft, so those are understood too: `Upload-Incomplete` for `Upload-Complete` (the other way round), the `104` for whichever version the client names, a piece in any content type, `OPTIONS` answering `200`, and a `POST` with none of these headers is the whole file. A finished upload is answered `200` (not `202`, which iOS takes for unfinished and sends the whole file again), and iOS's upload of an item Photostead has, or that the app is sending, is turned away at once (`409`).
- **A phone's items (built 2026-10-22, migration 00043)**: `device_items` (owner, item, sha256) keeps the phone's name for what it sent, from `deviceItem` in a resumable upload's meta ("<Photos item>|photo"), noted once the upload is filed or found to be here already. `POST /api/v1/uploads/device-items/check` says which of a phone's items it has, and which are on their way (an unfinished upload of it begun in the last two hours whose partial file grew in the last ten minutes), so the app and iOS's uploads don't both send the same file; `POST /api/v1/uploads/device-items` notes items sent before uploads named them, from their files' hashes, only for files the person has. For iOS's background uploads, which can't hash a file before sending it. Kept when an item is deleted, as the upload check counts deleted files.
- **Checking first.** `POST /api/v1/uploads/check` says which hashes are already in the library, deleted ones included, so a sync never sends them. With an `album`, those known items are put in it.
- **Descriptions and keywords** that live outside the files are in `asset_descriptions` (source `immich`, `upload` or `user`, where `user` always wins) and `keywords` / `asset_keywords`. Search by meaning fuses them in as a fifth list.

**Photostead Capture and the ComfyUI sync (2026-10-17).** Photostead Capture (`capture/`, [README](../capture/README.md)) is a browser extension that saves a post's media with its text through the upload API. It started with X; since 2026-11-06 it also saves from CivitAI (with the prompt and generation settings), Instagram, TikTok and Pinterest, one module per site in `capture/content/sites/`. The ComfyUI sync (`integrations/comfyui-sync`) files every finished output, with its workflow.

**The sync feed (built 2026-10-22, `internal/api/sync.go`)** is how a phone app keeps the library. `GET /api/v1/sync/stream` (a session, a paired device's token or an API key) answers in JSON Lines. Without `since` it sends every item the person may see (through the gate, `visible_assets`) and every album of theirs or shared with them, with its items in order. It ends with `{"type":"checkpoint","cursor":…}`. With `?since=cursor` it sends only the items and albums changed since: each as it is now, or `assetGone` / `albumGone` when it's no longer there or no longer theirs. Items carry what the timeline shows plus what a phone needs: the original's `sha256` (to match its own photos), the file name, `inLibrary`, `stacked` and `motionOf` (to place them), and `adult` (to blur or hide them by the person's setting). Triggers on assets, stars and favorites, Household spaces and membership, stacks, albums and their items and people, and adult labels write `sync_changes` with the writing transaction (`xid8`). The cursor is the oldest transaction still running when the feed read (`pg_snapshot_xmin`), inside one repeatable-read snapshot. So a change that commits after a read comes next time; some come twice, harmlessly. Changes are kept 30 days. A cursor from before the oldest kept, or from ahead of the database (restored from a backup since), gets `{"type":"reset"}` and everything again. Gzip when asked for. Phones of household members reach it on the tailnet; through Funnel only a guest could.

**The iPhone app (started 2026-10-22, `ios/`, [README](../ios/README.md))** backs up the Photos library. It's SwiftUI for iOS 17 and later, with the Xcode project made by XcodeGen from `project.yml`. It pairs by the web app's code or QR link (`photostead://pair`, registered so the Camera app opens it), keeps the device token in the Keychain, and reaches the server at whichever address answers. Each item's originals are hashed as they're read out of Photos, asked about (`/uploads/check`), and sent with resumable uploads; a ledger on the phone remembers what's done. Sending is iOS's (a background URLSession), so it carries on while the app is closed, and iOS's background tasks get more ready. The library is browsed from the sync feed, kept in SQLite on the phone: timeline by month, albums, and a viewer.

**How the import works (built 2026-10-17).** The owner connects Immich in the app ("Import from Immich") with the server's address and an API key. The key is checked first and kept only on the server, in `connections`. It needs `asset.read`. `album.read`, `person.read` and `tag.read` bring albums, people and tags too; a missing one is reported, not fatal. Descriptions come with the items (`withExif`); one written in Photostead is never replaced. Photostead only ever reads from Immich: the client makes GETs and `POST /search/metadata`, and a test fails if it sends anything else.
- **Matching.** Immich's paths are its container's (`/mnt/external/images/…`), so no path mapping is configured. Each Immich path votes for the Photostead library whose relative path it ends in (longest suffix first), and a folder is trusted after two agreeing items, so one shared file name can't map a folder. Items in Immich's upload folder also match by content SHA-1.
- **What comes over.** Favorites (`asset_user_state`), archive state (`assets.visibility`), albums with their covers (`albums`, `album_assets` with `added_via = 'immich'`), and named people with their items as `person_suggestions`. Unnamed face clusters are left to Photostead's face recognition. Trashed items, Live Photo clips and the locked folder (which needs Immich's PIN) are skipped.
- **Importing again.** Every Immich id is kept in `import_map`, so albums and people are updated rather than duplicated. `import_asset_state` keeps the favorite and archive state the last import brought over, and a later import changes only what changed in Immich since. Changes made in Photostead are kept. Album items are removed only if an import added them.
- **One item, several in Immich.** When the same bytes are in two places, Immich has two items and Photostead one. It's a favorite if either is, and archived only if both are.

## API surface

| Surface | Path | Notes |
|---|---|---|
| REST | `/api/v1/…` | OpenAPI 3.1 generated from the Go types; breaking changes caught in CI by diffing the spec; published support policy |
| Live updates | `/api/v1/events` (SSE) | Upload progress, job state, new assets |
| Sync | `/api/v1/sync/stream` | JSON Lines: everything the person may see, then what changed since a cursor (built 2026-10-22) |
| Uploads | `/api/v1/uploads`, `/api/v1/uploads/resumable` | Multipart in one go; or IETF resumable uploads (draft-14): answers `OPTIONS` with `Upload-Limit`, sends `104` |
| Generation ingest | `/api/v1/generations` | Upload media with prompt, model, provider and parameters attached |
| Worker | `/api/worker/v1/…` | Claim, heartbeat, fetch, report |
| MCP | `/mcp` | Streamable HTTP (stateless), an API key or a paired device's token; search, look, organize and add tools (built 2026-11-08) |

**The MCP server (built 2026-11-08, `internal/api/mcp.go`).** `/mcp` speaks MCP over Streamable HTTP, stateless, with JSON responses, using the official Go SDK. A person's own Claude Code, Codex or other MCP client searches and organizes their library with it, under their own AI subscription.
- **Signing in.** It takes an API key (`photostead apikey add`, sent as `Authorization: Bearer psk_…`) or a paired device's token, never a guest's. It isn't reachable through Funnel.
- **How tools run.** Each tool calls the API itself, in-process, as that person. The server puts who it's for in the request's context (`trustedKey`), and `requireSession` honors it. Only the server's own code can do that, so the gate, the person's adult-content setting and the vault (always locked here) hold exactly as in the app.
- **The tools.** Reading: `search`, `ask`, `get_item` (when, where, people, keywords, description, albums, text, and how an AI item was made), `view_item` (the thumbnail or 1440 px preview as an image, with no location in it), `library_counts`, `list_albums`, `list_people`. Changing: `set_favorite`, `edit_keywords`, `set_description`, `create_album`, `add_to_album`, and `add_media`, which adds a file (base64) with the prompt, model and tool it was made with. `add_media` goes through the upload endpoint, so its prompt is kept with the item and written to its sidecar. The item is labeled AI only when the file itself shows it, as most image APIs' files do (Content Credentials).

## Deployment

**A new install sets itself up (built 2026-11-08, `internal/libraries`).** At start-up, every 10 minutes, and right after the first account is made, each account but a guest's gets its folder in the photostead share (`/photos/library/<username>`) as its managed library. A first upload makes it at once too. Every folder mounted under `/libraries` (`PHOTOSTEAD_LIBRARIES`) that isn't a library yet becomes an indexed, read-only, watched library of the first admin's, and is scanned; an empty mount point is passed over. The container's entrypoint makes the share's `library/` and `backups/` for `PUID:PGID` when they aren't there, so a share owned by root still works, and says so when the share isn't writable. Tested from an empty install in Docker: setup, an upload filed in the share, and a mounted folder found and indexed.

**Releases.** Tagging `v*` publishes `ghcr.io/atomtanstudio/photostead` and `photostead-worker` (`.github/workflows/release.yml`). The worker image (`deploy/docker/worker.Dockerfile`) is Python with onnxruntime-gpu and Debian's ffmpeg (NVENC through the NVIDIA Container Toolkit). The Community Applications template is `deploy/unraid/ca/photostead.xml`; `deploy/unraid/photostead.xml` stays the development server's own.

**Unraid (one container).** It holds the `photostead` binary, Postgres 18 with pgvector, the CPU worker, exiftool, ffmpeg, libvips and c2patool, supervised by s6-overlay. Volumes are `/photos` (the `photostead` share on the array), `/data` (`appdata/photostead` on the cache pool) and optional read-only `/libraries/<name>`, as laid out under Storage layout. Postgres runs inside so there is one app to install. External Postgres is supported for people who want it.

**GPU worker.** `photostead-worker` is a Docker image with the CUDA runtime, configured with a server URL and a worker token. On the Legion it uses the Mission Control arbiter and is registered in Mission Control's `registry.yaml` with an unload endpoint and a "working" probe.

**Remote access** (migration 00035, `internal/remote`, `internal/api/remote.go`). Built in: Photostead joins the owner's Tailscale network as a node of its own (tsnet, userspace networking, so no TUN device or host changes) and serves the app there over HTTPS with a real certificate, at `https://<name>.<tailnet>.ts.net`, with plain HTTP redirected. The admin turns it on in Devices & remote access and approves it once in Tailscale (or gives an auth key); its state is kept in `/data/tailscale`, so a restart rejoins without approving again, and "Leave the tailnet" signs it out and deletes that state. It needs HTTPS certificates turned on for the tailnet. Passkeys work at that address. The same handler also works behind any reverse proxy or tunnel (Caddy, SWAG, Cloudflare Tunnel), with `PHOTOSTEAD_PUBLIC_URL` as the public address. Clients learn the server's id (`server_settings`, so a device knows it's the same server at another address) and its addresses: home (learned from a signed-in visit at a private IP or a `.local`/`.lan`/`.home.arpa` name), tailnet and public.

**Albums shared with people, and guests** (migration 00038, `internal/api/albumshare.go`). A (manual) album can be shared with someone in the household, or a guest: an account with no library of its own, invited to the album with a link like a household invitation. A viewer sees the album; a contributor also adds to it, from their library (`album_assets.added_by`, and they may take out what they put in) or by uploading (into the album owner's photostead share, noted "Added by …"). The gate is two functions now: `library_assets(viewer)` is a library (your own items and the Household's), read by the timeline, its counts and memories; `visible_assets(viewer)` is everything you may see: your library, plus others' items in albums shared with you or in your own albums (a contributor's), each once, with Live Photo clips. So an album shared with you shows in Albums ("Shared by …") and its items in it, not in your timeline. A guest signs in to a pared-down app: the albums shared with them, each album, a simple viewer, and adding photos where they're a contributor. Comments and activity are still to come.

**Backups** (`internal/backup`, [backups.md](backups.md)). The database is copied every night at 3:30 with pg_dump (custom format, zstd) into `PHOTOSTEAD_BACKUP_DIR`, by default the managed share's `backups/database` where the move writes (on Unraid the array, away from the cache pool the database is on). A copy is read back in full with pg_restore before it counts, and the newest 14 are kept. How the last one went is in `server_settings` (`backups`); a failure, or none for a day and a half, shows in the admin's status line. `photostead backup check` restores a copy into a scratch database and compares every table's rows; `photostead backup restore` brings one back with Photostead stopped, keeping the database it replaces under another name.

**Personal links** (migration 00040, `internal/api/personal.go`). Sharing with someone outside the household asks nothing of them: the owner types their name and gets a personal link (`/p/{token}`). Behind it is a guest account with no password (and a made-up `.invalid` address, never shown), and the link is its way in: the first 3 devices to open it get a session (`sessions.via_link`, a year that slides with use, like a paired device's), and a 4th is told to ask for a new one. Opening it again where it's open carries on; signed in there as someone else, it says so rather than switching. It's one link per person: an album shared with them later is there too. Whoever made the link sees how many devices have it open and when it was last used, copies it again, signs its devices out (it then opens on 3 again) or replaces it (the old one stops). Not tied to an IP address: a phone's changes through the day, and many people share one. A guest invitation from before personal links opens the same way (`POST /api/v1/invites/{token}/open`): the guest is made with a personal link from whoever invited them, and that device is its first. Accounts with a password for guests (accepting the invitation with an email and password) still exist in the API, though the app no longer offers them. A guest's account stays when no album is shared with them any more; the admin sees every guest under Household and removes one (`DELETE /api/v1/household/guests/{id}`, 2026-11-13). In one transaction the account goes, and with it, by `ON DELETE CASCADE`, its sessions, personal link, album shares, comments, picks and sign-ins under way; what they added stays in the album (`album_assets.added_by` is set null, so the activity says "Someone"). It refuses anyone in the household, and a guest who owns a library, items or albums (only possible from the command line). Every other foreign key to `users` cascades or sets null; the e2e test checks that. An open tab runs the version it was opened with, so the web app looks, when the tab comes back into view, at which version the server has (the bundle `index.html` names) and offers a reload after an update.

**Share links and Funnel** (migration 00037, `internal/api/links.go`). A share link opens a (manual) album to anyone with it at `/s/{token}`: a small page of its own (no account, none of the app) with the album's items as its owner sees them, a viewer, and, when the owner allows, downloads and adding photos (uploads go into the owner's photostead share and the album, noted as "Added by … through a share link"). A link can have a password (a browser that gives it may look for a day), a last day, and is deleted at once. Thumbnails and previews carry no metadata; a video or download that records where it was taken is sent with that taken out as it goes (exiftool for photos, an ffmpeg remux for videos), the rest as is. Funnel puts share links on the internet: the Tailscale node opens a Funnel-only listener on 443 beside the tailnet's, serving `api.PublicOnly`: share links (`/s/…`, `/api/v1/public/…`), personal links (`/p/…`), the web app's files, and guests. Requests from there are marked as from the internet, and `requireSession` lets through only share links, a guest's way in (setup status, signing in, opening a personal link, accepting a guest invitation) and what a guest's session needs (their albums, the album timeline, items and their pictures, adding to an album as a contributor). Household members can't sign in, use a session or API key, or accept a household invitation from the internet ("turn on Tailscale"); the API docs and `/api/v1/uploads` aren't there at all. **Paired devices away (2026-11-09):** unless the admin turns it off (`noDevices` in the `tailscale` setting, `POST /api/v1/remote/funnel/devices`), Funnel serves `api.PairedAway` instead: a request signed with a paired device's own token (`psd_…`, checked against its session first; a bad one gets 401) reaches the whole API as at home, marked `awayKey`, but for `notAway` (signing in again, pairing codes, passkeys, two-step, single sign-on, household invitations and roles, removing a guest, remote access settings): a lost phone, once signed out, leaves no way in behind it. `GET /api/v1/server` answers there too, so the phone knows it reached its own server; with devices kept home it stays 404, so a phone doesn't mistake Funnel for a way in. MCP and the API docs aren't there. So only whoever sets Photostead up needs Tailscale; the family's phones pair at home once and then work anywhere. It needs the tailnet's policy to allow Funnel (the `funnel` node attribute). Links, personal links and guest invitations point at the tailnet's address when Funnel is on, else `PHOTOSTEAD_PUBLIC_URL`, else the address the owner is at (then they work only from there); household invitations use the address the inviter is at.

**Pairing devices.** The web app shows a code (eight characters, good once for 10 minutes) and a QR code of `photostead://pair?code=…&server=<id>&url=…` (tailnet, public, then home). A device posts the code to `/api/v1/devices/pair` with its name and platform, and gets a session of its own (`sessions.device`), sent as `Authorization: Bearer psd_…`, good for a year from its last use; it works wherever a browser's session does, the vault included. **Wrong tries** (`internal/api/tries.go`, 2026-11-13): wrong codes and share links' wrong passwords count together, ten in ten minutes from one person (an IPv4 address, or an IPv6 /64) and a hundred from one connection's address; past either, it waits. Behind a reverse proxy the connection is the proxy's, so the person is the rightmost `X-Forwarded-For` entry that isn't itself a trusted proxy (the one the nearest trusted proxy added; what's left of it is whatever the asker sent), believed only when the connection comes from a trusted proxy: by default loopback and private addresses but not Tailscale's (`PHOTOSTEAD_TRUSTED_PROXIES` lists others, `private` for the default, `off` for none). Someone at home is on a private address and could make up a header for every try, which is what the hundred per connection is for. `CF-Connecting-IP` isn't read: Cloudflare puts the same address on the end of `X-Forwarded-For`, and behind any other proxy it's whatever the asker sent. Through Funnel the connection is Tailscale's relay, which says whom it carries (`ipn.FunnelConn.Src`, kept by `remote.FunnelFrom`); headers there are never believed. Devices & remote access lists every browser and device signed in to the account (with the browser each last used) and signs any of them out. **Codes that last** (migration 00056, `internal/api/lasting.go`, 2026-11-13) are for a phone that can't be at home while a 10-minute code is on screen: App Review pairing with a demo server days after the app was sent in, or family far away. The admin makes one for a household member's account (not a guest's), good for 1 to 90 days (14 by default) and 1 to 50 devices (5), with a short note to tell it apart in the list ("App Review", "Mum's iPad"; up to 60 characters on one line, migration 00058); `pairing_codes` keeps its hash (the code is shown once), the account, the note, and how many devices may pair and have. `/api/v1/devices/pair` tries a 10-minute code first, then a lasting one, counting the device in the same statement that checks it still has days and devices left; one that's run out, been revoked (its row deleted) or never was is the same wrong code to the limit. They look like the 10-minute codes, so the phone takes either. Revoking leaves the sessions it made, ended like any other. `photostead pairing add|list|revoke` (`add -note "…"`) does the same from the shell (its link has the public and home addresses; only the running server knows the tailnet's). Making and revoking them are in `notAway`, and pairing stays off Funnel: a demo server for App Review has its own public address.

**Operations.** Nightly database dumps to `/photos/backups/db`; a rolling integrity scrub that re-hashes originals and reports missing, untracked or altered files; a backup manifest of every original's hash for verifying restic, borg or rclone copies; and a job dashboard with pause, throttle and priority.

## Choices this doc makes

These go beyond the decisions record. Each has a reason, and any of them can be revisited before code depends on it.

| Choice | Why |
|---|---|
| Monorepo | Server, clients and the OpenAPI contract change together |
| SHA-256 as content identity | Hardware-accelerated on phones (CryptoKit, ARMv8) and servers; standard for backup manifests. Immich's SHA-1 is kept only for matching during import |
| One asset per unique content, many files | Identical copies found in two folders don't become duplicates |
| Postgres 18 | Built-in `uuidv7()`, asynchronous I/O; pgvector supports it |
| pgx + sqlc for queries, goose for migrations | Plain SQL that is checked at compile time; migrations embedded in the binary, with up and down steps |
| OpenAPI generated from Go types (Huma) | The spec can't drift from the code; CI diffs it to block accidental breaking changes |
| exiftool as a long-running process | The most complete metadata reader and XMP writer; one process avoids per-file start-up cost |
| Built-in CPU worker uses the same pull protocol | One protocol to build and test; GPU workers are purely additive |
| `worker_tasks` beside River | River runs Go jobs inside the server; remote Python workers need a leasing API over HTTP |
| One household per server in v1 | Matches every v1 decision; `spaces` leaves room for more shared spaces later |
| Embeddings stored per model | Model upgrades re-embed in the background and switch atomically |

## Build order

Confirmed 2026-10-16. Each milestone ends with something usable. Milestone 1 is cut so that a real library browses in Photostead as early as possible.

1. **Foundation: your library in Photostead.**
   - Repository skeleton and CI; Go server; Postgres schema and migrations; the visibility gate
   - Indexed libraries: existing folders mounted read-only, hashing, the watcher
   - The metadata and provenance parsers, so the AI badge and Generation panel are evidence-based from day one
   - Thumbnails, previews, ThumbHash, video playback
   - Immich import of albums, favorites, archive state and people names
   - The web app ported from the Lightbox prototype: timeline, rails, viewer, selection
   - The single Unraid container
   - **Done when:** a 121k-item library browses in Photostead at prototype speed or better, with its folders untouched.
2. **Intelligence.**
   - The worker and Mission Control adapter
   - Search by meaning, OCR, hybrid ranking
   - Faces with the new clustering, People, pets
   - Characters, the prompt library, the model & LoRA catalog
   - Utility collections
   - Ask (its planner already exists in the prototype, so it moves up from milestone 5)
3. **Organizing and sharing.** Ratings and flags; nested and smart albums; stacks; keywords; bulk metadata and time fixes; trash and vault; Household space and auto-share rules; links, guests, QR albums, proofing; memories; passkeys and OIDC.
4. **Ingest.** Reaching your server from anywhere, with as little setup as possible, wherever it runs (home network, Unraid, a VPS): the phone apps, Photostead Capture, and the share links and guests of milestone 3 all depend on it, so its design is settled before the phone apps start (pairing a device by scanning a QR code from the web app, which carries the server's home and away addresses and a device token; a built-in way out for people without a domain, such as joining their Tailscale network or a Cloudflare Tunnel). Resumable uploads and the web upload queue; iOS and Android backup apps with the sync feed; Google Takeout, Apple Photos and sidecar-folder importers; Photostead Capture.
5. **Create.** (A first editor, frame grabs and trimming built 2026-10-25: see Edits and versions.) The editor (adjustments, SAM 2.1 masks, LaMa eraser) on versioned recipes; ComfyUI re-run, remix and generative edits; video trim and frame grabs; XMP write-back of edits.
6. **Ship and cut over.** Integrity scrub and backup manifest (built 2026-10-24, see [backups](backups.md)); security review; documentation and the Unraid Community Applications listing.

Compared with the earlier draft:
- Milestone 1 now includes the provenance parsers, because they run at ingest and are cheap.
- Ask moves from milestone 5 to milestone 2, because its planner is already built.
- Photostead Capture is placed explicitly in milestone 4.
