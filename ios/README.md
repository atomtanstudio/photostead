# Photostead for iPhone

Backs up the iPhone's Photos library to your own Photostead server: originals, newest first, only
what the server doesn't have yet. It finds the server at home, or anywhere over Tailscale or Funnel.

## Putting it on your iPhone

1. Open `ios/Photostead.xcodeproj` in Xcode.
2. Set your signing team: make `ios/Config/Local.xcconfig` with the line `DEVELOPMENT_TEAM = <your
   team ID>` (it isn't in git, and survives regenerating the project). Or in Xcode: **Photostead**
   project → **Photostead** target → **Signing & Capabilities** → **Team** (lost when the project is
   regenerated). Add your Apple ID in Xcode → Settings → Accounts if it isn't there. A free Apple ID
   works; the app then needs re-installing from Xcode every 7 days.
3. Plug the iPhone in (or pick it over Wi-Fi), choose it at the top of the window, and press ▶.
   The first time, the iPhone needs **Developer Mode** on (Settings → Privacy & Security →
   Developer Mode, then restart), and to trust the developer (Settings → General → VPN & Device
   Management).
4. In Photostead on your computer, open **Devices & remote access** and click **Show a pairing
   code**. Scan its QR code with the iPhone's Camera, tap the banner, and confirm. Allow access to
   Photos.

Away from home it works through Funnel, with no Tailscale on the iPhone, once Funnel is on in
Photostead (Devices & remote access) and **Paired phones work from anywhere** is ticked (it is by
default). Without Funnel, the iPhone needs the Tailscale app, signed in to the same tailnet. At home
it works without either, on your home address.

## How it works

- **Pairing** (`Connection.swift`): the code from the web app's QR code (`photostead://pair?…`,
  which opens the app) or typed in, sent to `POST /api/v1/devices/pair`. The device's token goes in
  the Keychain; the server's addresses (tailnet, public, home) are all asked at once whether they're
  the same server (`GET /api/v1/server`). The home address wins whenever it answers, since the
  tailnet address answers at home too (through Funnel) and backups would go out and back; away,
  the first other one to answer, after a second's grace for home. A link opened from
  outside shows where it would send your photos before anything happens.
- **Backing up** (`Backup.swift`, `Upload.swift`): each item's originals (a Live Photo's photo and
  its clip, which the server pairs again; not a photo's edits) are read out of the Photos library
  (from iCloud when they're only there) into the outbox while they're hashed. The server is asked
  whether it has the SHA-256 already (`POST /api/v1/uploads/check`, deleted ones included); if not,
  an upload is made on it (`/api/v1/uploads/resumable`, IETF draft-14) and the file is handed to iOS.
  A ledger on the phone keeps what's done and what's out.
- **Sending** (`Outbox.swift`): a background URLSession. iOS sends the files itself, while the app
  is open, suspended or closed, and wakes it briefly when they finish, to note it. A transfer that
  stops (no network, Photostead away) goes again from where the server says it got to, as a new
  upload if it ran out. When the server takes only so much in one request (`max-append-size`,
  through Cloudflare), a big file goes in pieces: each one handed to iOS as the last arrives. Up to 12 files (2 GB) are out at once. Swiping the app away cancels them
  (iOS's rule); they go again the next time it runs.
- **When it runs**: opening the app (and pull to refresh); about 30 seconds after leaving it; and in
  iOS's background tasks (`BackgroundWork` in `PhotosteadApp.swift`): a long one when the phone is
  idle (usually overnight, charging) and a short look every so often, which get more items ready and
  send again what stopped. Wi-Fi only unless cellular data is allowed.
- **iOS's own background uploads** (iOS 27 and later; `Uploads/`, the `PhotosteadUploads` extension):
  with full access to Photos, the app turns on Photos' background upload jobs, and iOS then sends new
  photos and videos itself whenever it suits the phone, even when Photostead hasn't been opened for
  days, as it does for iCloud Photos. The extension says what: newest first, what Photostead doesn't
  have. It can't hash a file to ask (iOS reads the file), so every upload, the app's too, names its
  item (`deviceItem`, "<Photos item>|photo"), Photostead keeps those names, and the extension asks
  by them (`POST /api/v1/uploads/device-items/check`); what the app sent before it named them is
  told once from its ledger (`POST /api/v1/uploads/device-items`). After 300 items in a row that are
  there, it takes the older ones to be there too. Failed ones go again to the address that answers
  now; ones Photostead refused wait a day. The pairing and token are in a Keychain group the app and
  the extension share (`…studio.atomtan.photostead.shared`); the pairing moved there from the app's settings.
  iOS may only send to the address in the extension's `BackgroundUploadURLBase`
  (`PHOTOSTEAD_UPLOAD_URL_BASE`: put your server's in `Config/Local.xcconfig`), and it doesn't run
  these in the Simulator: on an iPhone, turn on Settings › Developer › Resource Upload Test Mode to
  have it run them at once. The app's own backup carries on too, and the two leave each other's
  files alone: Photostead also says which items are on their way (an upload begun and not
  finished), and the extension skips those. The app, before reading an item, skips what Photostead
  has (marked done without reading it) and what's on its way; what iOS has only queued it sends
  itself, and Photostead turns iOS's copy away (409) before reading it, which the extension then
  lets go. A file the app finds Photostead has already (by its hash) is named to it too. iOS counts
  a 202 as unfinished and sends the whole file again, so a finished upload is answered 200.

- **Browsing** (`Library.swift`, `LibraryStore.swift`, `LibraryView.swift`, `Images.swift`): the
  library comes from the sync feed (`GET /api/v1/sync/stream`) into SQLite on the phone: everything
  the first time, then only what changed since the cursor the last read ended with. When everything
  comes again (no cursor, or the server says reset), the library stays as it was until it has all
  come, then what didn't is gone: a read cut short leaves nothing missing. A full library
  of 100,000 items is applied in about 6 seconds and read back in 0.13 (tested in the simulator).
  Three tabs: **Library** (your timeline, by month, as the web app sorts it), **Collections** and
  **Search**. Settings (backing up on or off, videos, cellular, the server, unpairing) are behind your
  picture at the top right, and a pill under the Library's title ("Backing up · 1,204 left", "Backed
  up") opens them too. Thumbnails and previews come from the server as they're shown, cached on the
  phone (up to 1 GB), with each item's ThumbHash drawn as a blurred placeholder until they arrive. It
  catches up when the app opens, after a backup, after a change made on the phone, and on pull to
  refresh.
- **First run** (`PairView.swift`, `Welcome.swift`): the pairing screen says what Photostead is,
  offers **Scan the pairing code** first and typing the address and code second, and links to
  photostead.com for people without a server. After a fresh pairing, once per phone: why Photos
  access is needed (then iOS asks), and how backup looks after itself, with an optional switch for
  backup notices. Nothing is asked at launch.
- **Backup notices** (`Notices.swift`, a switch in Settings): local notifications, no server push.
  "N photos and videos backed up" after a backup in the background (quietly, at most hourly; opening
  the app withdraws it), and "Backup can't reach Photostead" a day after it last could while items
  wait.
- **Collections** (`Collections.swift`, `Places.swift`, `Characters.swift`, `Recent.swift`,
  `API+Browse.swift`): albums (making one, from the empty state too), People, Places (by country and
  town, and an Apple map when the server allows a map: `PHOTOSTEAD_MAP=off` hides it), Characters
  and the Prompt library (when there are any), Recently added, Favorites, Videos and the Archive
  from the phone's copy; Made with AI, Captured by me, the labels (Screenshots, Documents, Receipts,
  IDs, QR codes), a person's items and the Trash from the server a month at a time as they're
  scrolled to (`/api/v1/timeline` with a collection). People come from `/api/v1/people`, the counts
  from `/api/v1/library/counts`.
- **Search** (`SearchView.swift`): one box. **Search for** finds by meaning, by name and by text in
  the picture (`/api/v1/search`); **Ask** answers a question with the items it found
  (`/api/v1/ask`). Before typing: people and pets, places, characters, recent months, things to try
  ("beach", "dogs"), the labels, and recent searches. When search by meaning isn't running, results
  say they looked at names only.
- **Details** (`Details.swift`, `DetailText.swift`, `API+Details.swift`): laid out like Photos', from
  `/api/v1/assets/{id}`: the caption and keywords, when (in the time zone it was taken), the camera,
  lens, file and exposure, where (with a small map), people and pets, what made an AI picture (the
  prompt to copy, model, LoRAs, settings), text in the picture, albums and characters.
- **Live Photos** (`LivePhoto.swift`): press and hold to play, or tap the LIVE badge; muted unless
  its Sound is turned on (remembered). Tiles carry a small Live Photo mark.
- **Adult content** (`Library.swift`, `Images.swift`, `Viewer.swift`): the person's own setting, as
  on the web (Settings › Browsing › Adult content changes it for both). The sync feed marks adult
  items; **Blur** blurs their tiles and covers (with a mark) and veils them in the viewer until
  **Show** (until the app is closed; a video doesn't play, a Live Photo doesn't move); **Hide** leaves
  them out of the library, albums and lists (the server leaves them out of search and collections).
  A library on the phone from before the feed marked them is read again from the start, once.
- **Handling it** (`LibraryView.swift`, `Viewer.swift`), as the iPhone's Photos does: pinch the grid
  for more or fewer across (2 to 7, remembered, keeping the month in view); tap the Library tab again
  for the top; a handle on the right edge while scrolling, and dragging along that edge goes straight
  to a month (the years show while dragging). In the viewer: swipe sideways for the next, down to
  put it back (the library shows through as it goes), up for its details; pinch or double-tap to
  zoom and drag around; tap to hide the bars; press and hold a Live Photo to play it; videos stream.
  Its actions are the system's toolbar (so they go where iOS puts toolbars): **share** (the
  original, through the iPhone's share sheet), **favorite**,
  **details** (from the server: the camera, the place, its albums, people and keywords, and for an AI
  picture its prompt, model, LoRAs, seed and settings), **add to an album** (or a new one), and **move
  to the trash** (in the trash: **restore**). Changes go to the server and show on the phone at once.
- **Turned sideways, and the iPhone Duo**: the app turns with the phone. Grids are as many tiles
  across as the window's width takes at the size they are with the phone upright (`across` in
  `LibraryView.swift`): 4 upright, 9 across an iPhone turned sideways or an open iPhone Duo, 13 on
  an iPhone 18 Pro sideways. Turning or folding keeps the month that was at the top. On an iPhone
  Duo (built with the iOS 27.1 SDK, so laid out for it) the tab bar and the viewer's toolbar stand
  down the right edge under the status bar, closed and open; folding while looking at a photo
  carries on with the same one.
- Requests (`Connection.swift`, `API.swift`): a path's query stays a query (`Server.url`), with its
  values encoded by `query(_:)`; `appending(path:)` alone would turn the "?" into %3F.

- **Choosing several** (`Selection.swift`): Select (or press and hold a tile), tap tiles, or drag
  sideways across them to choose a run (up and down still scrolls; scrolling stops while a drag
  chooses). The bar: share (the originals), favorite, add to an album (or a new one), the trash, and
  More: archive (or bring back), share with the Household (or take out, when you're in one with
  others), stars, flags and color labels (when Ratings & flags is on), and the vault (a PIN set
  there the first time). In the Library, albums, Favorites, Videos, the Archive, search results and
  the server's lists; in the trash the bar restores, in the vault it takes out.
- **The Library's filter**: all items, photos, videos or favorites; and, in a Household with
  others, everyone's, yours or the Household's. A pill under the title says what's on and clears it.
  **Memories** run across the top (`GET /api/v1/memories`, unless they're turned off); a tap shows
  their items. Stars, flags and labels show on tiles and are set in an item's details, when Ratings
  & flags is on. The phone keeps them from the sync feed (flag and label came later: a library from
  before is read again once).
- **People** (`Collections.swift`): the named, then "Not named yet" (face recognition's groups). A
  person's page names them, or renames them; a name someone has already asks whether it's them,
  and if so the two become one (`POST /api/v1/people/{id}/merge`).
- **The vault** (`Vault.swift`): under Collections, behind its PIN (set there the first time). The
  PIN can be kept in the Keychain behind Face ID, so it opens with a glance. It locks when you leave
  it, and its pictures aren't kept on the phone (Photostead sends them no-store).

- **Sharing an album** (`Sharing.swift`, from an album you own): its people (household members,
  and anyone by name, who gets a personal link with no account to make; send them it, change what
  they can do, or stop sharing) and its share links (copy, send, the QR code to show across the
  table, downloads and uploads on or off, delete). **Adding to an album** from this iPhone's Photos
  library, in your albums and ones shared with you to add to.
- **Talking about a shared album** (`AlbumTalk.swift`, Activity on an album shared with you or by
  you, as the web's `Talk.tsx`): its activity (who added what, and comments, each item's picture
  opening it) with a box to say something, and its picks (everyone's, with who, for the owner; your
  own for anyone else). In the viewer, opened from that album: **Pick** (for the owner to see) and
  the item's comments. A comment of yours, or any in your album, is deleted with a swipe. It's read
  from the server as it is (a failure says so).
- **Smart albums** open on what their rules pick now, from the server (`collection=album:{id}`),
  like a place's items.
- **Downloading at full size** (`Download.swift`, the viewer's Download menu): **Save to Photos**
  or **Save to Files**. It's the edited photo when it's been edited on the web, otherwise the
  original. Something saved into Photos is noted in the backup ledger, so it isn't sent back.
- **Edited photos**: pictures are cached by a version from their ThumbHash, so a photo edited on
  the web shows its new previews here once the feed brings the change.

Not yet (the plan is `docs/ios-plan.md`): guests in the app, editing on the phone, scrolling on
its own while a drag chooses near the edge, and freeing up space on the phone.

Pairing starts the phone's copy of the library afresh, even from the same server (it may be someone
else's account now); unpairing erases it too.

## Building and testing

The Xcode project is made from `project.yml` with [XcodeGen](https://github.com/yonaskolb/XcodeGen):
run `xcodegen` here after adding or removing files.

- Unit tests: `PhotosteadTests` (pairing links, addresses, queries in requests, the pairing's move
  to the Keychain, the ThumbHash decoder against the reference one, applying the feed, a
  100,000-item library's timing, details' formatting, places by country, and when backup notices
  fire).
- App Store: each target has a privacy manifest (`PrivacyInfo.xcprivacy`: UserDefaults, reason
  CA92.1; no tracking, nothing collected), `ITSAppUsesNonExemptEncryption` is NO, and the launch
  screen is the house mark (`LaunchMark`). Release builds always declare `https://photostead.com` as
  the upload extension's `BackgroundUploadURLBase`, whatever `Local.xcconfig` says.
- More UI tests against a server: `BrowsingTests` (`PAIR_CODE_BROWSING`: search before typing, places,
  collections, a first album; `SMART_ALBUM`: a smart album's title, opening on its items, months of
  them; `TALK_ALBUM`: a shared album's title, its activity and picks, saying something and taking it
  back, Pick and unpick; run it paired as the owner and as someone it's shared with) and `ViewerTests` (`PAIR_CODE_VIEWER`: details, Live Photos; it expects
  a Live Photo, a photo with a camera and a place, and a video as the library's newest items, and
  `AI_NAME`, an AI picture's file name; `testAdultBlurredAndHidden`, `PAIR_CODE_ADULT`, expects the
  second and third newest to be labelled adult, and leaves the setting on Show). Pass these as
  `TEST_RUNNER_…` environment variables to `xcodebuild`. `testFirstRunScreens` needs no server (`-welcome` with
  `-demoLibrary`, debug builds only).
- UI tests: `PhotosteadUITests` pair with a test server and back up the simulator's Photos library,
  by the pairing link (as the Camera app opens it) and by typing, close the app mid-backup to check
  iOS carries on, browse (the library, the viewer, albums), and find and share (favorite, a new
  album, details, the trash and back, Collections, Search, settings), and choose and organize
  (Select, press and hold, favorites, the archive and back, the vault with a new PIN and out, the
  filter: `PAIR_CODE_ORGANIZE`), share an album (`PAIR_CODE_SHARE`), and download (`PAIR_CODE_DOWNLOAD`: Save to Photos, Save to Files). One more runs with no server: a
  made-up 94,000-item library (`-demoLibrary 94000`, debug builds only) for the scrubber, pinching,
  back to the top, and the viewer's zoom, details and swipe down, one drags across a row to choose
  it (`testDragToChoose`), and one turns the phone sideways
  (`testWideScreens`: the tiles keep their size, the viewer's toolbar). Run that one on the iPhone
  Duo simulator too (it needs the iOS 27.1 simulator: `xcodebuild -downloadPlatform iOS`); Device
  Hub's Closed, Partially Open and Open buttons fold it. They need fresh pairing codes (each lasts 10
minutes, so make them just before a run, or run the tests a few at a time) and Photos access granted
beforehand. A code that lasts (`photostead pairing add -days 1 -devices 10`) does a whole run.
The closed-mid-backup test needs something new to send and a slow way to the server
(`PAIR_ADDRESS_CLOSED`, a throttling relay); with everything already backed up it never sees
"Sending":

```bash
xcrun simctl privacy booted grant photos studio.atomtan.photostead
TEST_RUNNER_PAIR_LINK='photostead://pair?code=…&url=http%3A%2F%2F127.0.0.1%3A4810' \
TEST_RUNNER_PAIR_CODE='…' TEST_RUNNER_PAIR_CODE_CLOSED='…' TEST_RUNNER_PAIR_CODE_BROWSE='…' TEST_RUNNER_PAIR_CODE_FIND='…' \
TEST_RUNNER_PAIR_ADDRESS='127.0.0.1:4810' \
xcodebuild test-without-building -project Photostead.xcodeproj -scheme Photostead \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' -only-testing:PhotosteadUITests
```

(Build first with `build-for-testing`: pairing codes last 10 minutes.)

The app icon is `Photostead/AppIcon.icon`, an Icon Composer file: Photostead's mark
(`Assets/mark.svg`, as in the web app's favicon) as one glass layer on near-black. iOS draws its
light, dark, tinted and clear looks from it, and Xcode makes the images for iOS 17 to 25. Open it in
Icon Composer (in Xcode's Applications) to change it; `ictool` (inside Icon Composer) renders it
without opening the app.

## Store builds and screenshots

- `scripts/archive.sh` makes a Release archive in `build/` and checks what App Review looks at
  first: the version, `ITSAppUsesNonExemptEncryption`, the upload extension's address, both privacy
  manifests, and no debug switches. The build number is the UTC time (`20261113.1814`), so each
  upload's is higher than the last whatever happens to the git history; the version people see is
  `MARKETING_VERSION` in `project.yml` (1.0). `scripts/archive.sh --upload` sends it to App Store
  Connect for TestFlight. That needs the app's record there and Xcode signed in to the team's
  Apple account; the first upload makes the Apple Distribution certificate.
- `scripts/screenshots.sh` takes the App Store's screenshots (6.9" and 6.3") from a real library:
  `PAIR_ADDRESS=… PAIR_CODE=… ios/scripts/screenshots.sh`, best with a code that lasts
  (`photostead pairing add -days 1 -devices 4`), into `build/screenshots/`. It makes its own two
  simulators with nothing in Photos (so backing up sends nothing), sets the status bar to 9:41,
  and runs `StoreScreenshotTests`: the library, a photo and its details, Search, Places (the list),
  Collections, and the album named in `SCREENSHOTS_ALBUM` if one is. The library is the website's
  demo household, put into a server of its own with `site/demo-library/load.mjs` (see
  `site/README.md`), paired as Sam; the store's set is `SCREENSHOTS_ALBUM=Lisbon`.
