# The iPhone app: how far toward the web app, and in what order

Written 2026-10-22, after the first working build (backup, browsing, the Photos-style viewer). Android
follows the same plan when it starts.

## The idea

The phone is for **seeing, finding, sharing and quick touches**, and for **backing up**. The web app
stays the place for **heavy organizing and running the server**. Everything on the phone is the
server's own feature (the same API, the same data), so nothing is phone-only except what only a
phone can do. Where the web does something the phone doesn't, the phone shows no dead button; at
most a line saying "on the web".

Rules that keep it simple:

- **Three tabs**: Library, Collections, Search. Settings live behind your picture at the top right,
  not in a tab.
- **One set of gestures**, the iPhone Photos ones: tap to open, swipe down to put back, swipe up for
  details, pinch to zoom, long-press or **Select** to choose several, drag across tiles to choose a
  run of them.
- **A choosing bar of at most five actions**, the rest under **More**.
- **Organizing tools appear only when they're turned on.** Stars, flags and color labels follow the
  same "Ratings & flags" setting as the web app.

## The tabs

| Tab | What's in it |
|---|---|
| **Library** | The timeline (what's there now), with Memories across the top. A **backup pill** under the title ("Backing up · 1,204 left", "Up to date") opens the backup sheet. A filter menu: All, Photos, Videos, Favorites; Mine or the Household's when you're in one. |
| **Collections** | One scrolling page in sections: **Albums** (yours, and the ones shared with you), **People**, **Shared** (your links and who you've shared with), **Made with AI**, **Captured by me** (Photostead Capture), **Screenshots and documents** (the utility labels), then **Archive**, **Trash** and **Vault** (Face ID). Each opens a grid like the Library's. |
| **Search** | One box that searches by meaning ("dog on the beach at sunset"), by name and by text in the picture, with **Ask** for questions ("photos of Mia from last summer"). Before you type: people, recent searches, and the labels. |
| *Your picture* (top right) | Backup (on or off, videos, cellular), the server and account, sign-in, appearance, Photostead Pro, unpair. |

The old Backup tab is now the settings behind your picture, which the backup pill opens too.

## What goes where

**On the phone, free** (the server's features, which never get locked):

- Backup, in the background too, and the backup sheet.
- The timeline, albums, the viewer, videos, Live Photos.
- Search (meaning, name, text, Ask).
- Favorites; adding to and taking out of albums; making an album.
- Sharing: the iPhone share sheet (originals, location taken out on request), plus Photostead's own share links and personal links: make, copy, send, revoke.
- People (browse; name a face).
- Memories.
- Trash and restore; archive; the vault behind Face ID.
- The Household: whose items, and sharing with it.
- Stars and flags when Ratings & flags is on.
- Details, including how an AI picture was made (its prompt, model, LoRAs).
- Saving an original to the phone's own Photos.
- A guest who opens a personal link sees their albums in the app.

**On the phone, Pro** (one-time, shared with the family; extras only the app has):

- Home Screen and Lock Screen **widgets**: on this day, a favorite, backup status.
- **Offline albums**: an album kept on the phone for a flight.
- **More than one server or account**, switched from your picture.
- **Editing on the phone** (crop, rotate, light and color), once the server keeps edits (milestone 5).
- **Shortcuts and Siri**: "Back up now", "Show memories", "Add to album".
- **App icons**.

Pro never touches the server: it's a StoreKit 2 one-time purchase with Family Sharing on (about
$20–30), checked on the phone, with Restore Purchases. Nothing the server does is behind it.

**Web only, on purpose**:

- Bulk keywords, descriptions and date fixes.
- Culling with the keyboard.
- Look-alikes and stacks.
- Smart album rules (the phone shows their items).
- Household members and auto-share rules.
- Devices and remote access, sign-in methods, backups of the database, imports, the time zone.
- Characters, the prompt library, and models and LoRAs (the phone may show the prompt library read-only later).

## In what order

Each step ships on its own and is usable as it lands.

Done between steps 1 and 2 (2026-10-22): **the iPhone Duo and iOS 27**. The app turns with the
phone; grids keep their tiles' size and add more across (an open iPhone Duo, a phone turned
sideways); the viewer's actions are the system toolbar, so they stand down the side of an iPhone Duo
as iOS puts them; turning or folding keeps the place. And iOS's own background uploads (Photos'
upload jobs, iOS 27): iOS sends new photos itself, as it does for iCloud Photos. Apple asks for
iPhone Duo screenshots with every app sent to the App Store from April 2027.

1. **Find and share** (done 2026-10-22)
   - The three tabs; the backup pill and sheet; your picture's settings.
   - The viewer's bottom bar: share, favorite, details (with how an AI picture was made), add to album, trash.
   - The Search tab: meaning, name and text, and Ask.
   - Collections: albums (and making one), Favorites, Videos, Made with AI, Captured by me, Archive, Trash (and restore).
2. **Organize lightly** (done 2026-10-22; scrolling on its own while dragging near the edge isn't)
   - Choosing several: Select, long-press, drag across tiles.
   - Bulk actions: album, favorite, trash, archive, share with the Household.
   - Stars and flags (when on); Memories on the Library; Mine or the Household's.
   - People: browse, and name a face.
   - The vault with Face ID: the PIN kept in the Keychain behind Face ID.
3. **Share from the phone** (done 2026-10-25, except guests in the app)
   - Share links and personal links: make, copy, send, revoke.
   - Guests in the app.
   - Adding to an album shared with you.
   - The iPhone share sheet for originals.
4. **Pro**
   - StoreKit 2 and a quiet "Photostead Pro" page under your picture.
   - Widgets, offline albums, more than one server, Shortcuts, app icons.
5. **Create**
   - Editing, when the server keeps edits as versions.
   - The prompt library to look through.

## The server

Almost everything the phone needs is there already:

- the sync feed (items, albums, favorites);
- search, Ask, albums, the trash and archive, marks, people, memories, the vault, share links and personal links;
- generation details, resumable uploads.

Gaps to fill as they come up:

- People and labels aren't in the sync feed yet. Steps 1–2 read them from the API; they could join the feed later.
- A way to name a face from a phone-sized picker. The web's naming works on the same endpoint.
- Smart albums' items: the timeline API with the album's rules (there today), read page by page.
