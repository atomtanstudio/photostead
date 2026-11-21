# photostead.com

The website: the landing page, the pages the App Store needs (support, privacy policy, terms), and a live
demo of Photostead. It's all static files, served by Cloudflare Workers from its edge; no Worker code runs
and there's no server behind it.

| Path | What it is |
|---|---|
| `/` | The landing page (`src/index.html`) |
| `/support/` | Support: contact, getting started, questions and answers. The App Store's **Support URL** |
| `/privacy/` | The Privacy Policy. The App Store's **Privacy Policy URL** |
| `/terms/` | Terms of Use (the app itself is under Apple's Standard EULA) |
| `/demo/` | The real web app (`web/`, built with `--mode demo`), answered in the browser from a sample library |

## How the demo works

`npm run build:demo` in `web/` builds the web app with `src/demo` switched on. It loads
`/demo/library.json` and answers the app's `/api/v1` requests in the browser (`web/src/demo/engine.ts`):
the timeline, Recently added, albums, people, memories, Places, search, Ask, the prompt library and model
catalog, favorites, the trash, the vault, share links with their QR codes, a shared album's comments and
picks, the adult-content setting and so on. A saved photo edit's previews are made in the browser
(`web/src/demo/render.ts`), the way the server makes them. Changes last until the page is reloaded.
Anything that needs a real server (pairing a phone, sign-in methods, uploads and imports, checking files)
says so. Pictures are plain files under `/demo/media/`: 640px thumbnails, 1600px previews and face crops,
the sizes the server makes.

Places shows the list, not the map: the map's tiles come from a map service (OpenFreeMap), and visiting
photostead.com contacts no one else. Share links point at the household's tailnet address, which nobody
can open.

The sample library is a fictional household (Sam, Maya, their kids Leo and June, Rosa, Pepper the dog and
Miso the cat) over three years. Every picture was generated on Mitte for the demo:

- `demo-library/shots.json`: each picture's exact prompt, model and Mitte run, and what it shows, when and where.
- `demo-library/config.mjs`: the household, people, albums, places, cameras, favorites, album covers, and
  the album shared with the rest of the household, with what was said and picked there.
- `demo-library/towns.json`: the towns of GeoNames' list (CC BY 4.0) near those places, so each
  picture's place is named as the server names it: the nearest town within 60 km. `npm run towns`
  remakes it from a Photostead server's `geodata` folder (`GEONAMES=…`) when the places change.
- `demo-library/build.mjs` (`npm run library`): downloads each picture once into `demo-library/.originals/`,
  makes the sizes and ThumbHash placeholders, and writes `public/demo/library.json`. Re-running it makes
  only what's missing.

The household photos carry illustrative camera details; the AI pictures carry their real prompt and model
(Nano Banana 2, FLUX.2 [dev], Grok Imagine 2.0) under an IPTC "made by a generative model" label, the way
Photostead reads such files. The demo's badge says that every picture was made with AI.

The landing page shows the demo's own results: the search showcase runs each query through the demo engine
when the site is built (and the build stops if Ask reads a question without the person it names), the
memory cards are titled the way the server titles them, and `npm run screens` takes the app screenshots
from the built demo.

### The same household on a real server

For the iPhone app's App Store screenshots and App Review's demo server, `demo-library/load.mjs` puts the
household into a real Photostead server:

```bash
SERVER=http://127.0.0.1:4810 PASSWORD=… npm run load          # a new server, nobody signed up yet
SERVER=http://127.0.0.1:4810 PASSWORD=… npm run load people   # once its AI worker has looked at them all
```

It makes real files of the pictures (in `demo-library/.server/`, with exiftool): the dates, time zones,
places and illustrative camera details written into the household photos, and IPTC's "made by a generative
model" label with the prompt into the AI pictures. It makes Sam the admin, with Maya and Rosa in the
household (all with `PASSWORD`), uploads each picture as its owner (favorites and the archived one with
it), shares the household photos with the household, makes the albums, and shares Lisbon with Maya and
Rosa with their comments and picks. The second run names the people face recognition found, by which
pictures each group is in, and makes Nova's group an AI character. Run again, it does only what's
missing. It needs `public/demo/library.json` and the originals (`npm run library` makes both).

## Building and deploying

Once, after cloning:

```bash
cd web && npm install && cd ../site && npm install
npx playwright install chromium   # for npm run screens
npx wrangler login                # or set CLOUDFLARE_API_TOKEN
```

Everything, start to finish (download the library, build, screenshot, deploy):

```bash
cd site && npm run release
```

Publishing runs on GitHub (`.github/workflows/site.yml`) once two repository secrets are set,
`CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`: every push to `main` that changes `site/` or `web/`
publishes a preview at `photostead-site-preview.<your subdomain>.workers.dev`, and Actions → Website →
Run workflow with target `photostead.com` publishes the real site.

Day to day:

| Command | Does |
|---|---|
| `npm run build` | Builds `dist/`: the pages, `public/`, and the demo (runs `npm run build:demo` in `web/`). `SKIP_DEMO=1` keeps the demo already built |
| `npm run screens` | Screenshots the built demo for the landing page (`public/img/screens`) and the link preview (`public/img/og.jpg`) |
| `npm run preview` | Serves `dist/` locally as Cloudflare will, headers and 404 page included (`wrangler dev`) |
| `npm run deploy` | Builds and deploys |

Cloudflare settings that live in the dashboard, not in this folder:

- **The domain**: `wrangler deploy` attaches `photostead.com` to the Worker (`routes` in `wrangler.jsonc`).
  `www.photostead.com` is a second, tiny Worker (`www/`) that redirects each visit to the same path on
  photostead.com (301); deploying it attaches www and makes its DNS record. Publishing to photostead.com
  deploys both.
- **Email**: the site lists `hello@photostead.com`, a mailbox at MXroute. Its records are in
  photostead.com's DNS on Cloudflare: two MX (`fusion.mxrouting.net` at 10, `fusion-relay.mxrouting.net`
  at 20), SPF (`v=spf1 include:mxroute.com -all`), DKIM at `x._domainkey`, and MXroute's `_da-verify-…`
  ownership record. Don't turn on Cloudflare Email Routing as well: it would replace the MX records.
  Apple checks that the support contact works.
- **DNS records** can be added from GitHub: Actions → DNS record → Run workflow
  (`.github/workflows/dns.yml`, with the same Cloudflare token). Type LIST shows what's there.
- **Analytics**: keep Cloudflare Web Analytics off. The Privacy Policy says the site has no analytics.

Headers (security policy, caching) are in `public/_headers`. The fonts are served from the site itself
(`public/fonts`, under the SIL Open Font License), so visiting photostead.com contacts no one else.

## For App Store Connect

| Field | Value |
|---|---|
| Marketing URL | `https://photostead.com/` |
| Support URL | `https://photostead.com/support/` |
| Privacy Policy URL | `https://photostead.com/privacy/` |
| App Privacy | **Data Not Collected**: the app sends photos only to the user's own server, which the developer never receives, and has no analytics or tracking |
| Copyright | `2026 Atom Tan Studio` |

Also link the Privacy Policy inside the app (guideline 5.1.1(i) asks for both); the settings sheet is a good place.

What Apple's rules mean for this site:

- **No device frames.** Apple's marketing guidelines don't allow drawing or simulating an iPhone. The
  landing page shows the web app's screens in plain windows, and the iPhone section is an illustration
  of a backup, not a phone. Real screenshots of Photostead for iPhone can go there once they're taken in
  the simulator: plain, with no drawn device or status bar around them.
- **The App Store badge:** the official artwork from Apple's marketing tools, unaltered, in
  `public/img/app-store-badge.svg`, linked to the app's App Store page (`APP_STORE_URL` in
  `build.mjs`, live since 2026-11-20). Every `{{appStore:…}}` placeholder draws it; with
  `APP_STORE_URL=` empty they say "Coming soon", in text, instead.
- **Names:** "Photostead for iPhone", never "iPhone app"; "iPhone" never in capitals, so the iPhone
  section's label uses the `plain` eyebrow. The trademark lines are in the footer and the Terms.
- **No placeholders** (guideline 2.1): every link and address on the site must work when the app is submitted.

## Keeping the copy true

The landing page describes what Photostead does today. Before adding a claim, check it against the code or
`docs/`. For example, the scale shown is what's been tested (100,000+ items), and cloud AI isn't mentioned
because it isn't built: the only AI outside the server is an assistant a person connects over MCP, and the
copy says so wherever it says nothing leaves the house. The code is public under the AGPL, so the site
calls Photostead open source and links to it. `/install/` links to files on GitHub that must exist when
it's published: the latest release's `Photostead.dmg` and `Photostead-windows.zip`, and on `main` the
Unraid template and `deploy/compose/compose.yaml`. Its Unraid, Docker and "reach it from anywhere"
steps follow `docs/guide.md`; the Mac and Windows ones follow `desktop/` (the menu's own words).

The copy was last checked against `main` at 7492ad7 (2026-11-08), and the demo is the web app on `main`.
What the landing page describes that the demo can’t show: uploads and imports, Photostead for iPhone, Capture,
an AI assistant over MCP and the nightly file check (each needs a server), the Places map (see above),
and video trims and frame grabs (the sample library has no videos).
