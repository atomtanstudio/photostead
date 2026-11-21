// Builds photostead.com into dist/: the pages in src/ (with their shared parts and the pictures
// they show from the demo library), the files in public/, and the demo itself (the web app built
// with `npm run build:demo`, served at /demo/). Cloudflare Workers serves dist/ as static files.
//
//   node build.mjs              everything
//   SKIP_DEMO=1 node build.mjs  the pages only (keeps the demo already in dist/demo)
//
// The demo library (public/demo) must be built first: `npm run library`.

import { execFileSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { cp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import sharp from 'sharp';
import { thumbHashToAverageRGBA, thumbHashToRGBA } from 'thumbhash';
import { pictureId } from './demo-library/ids.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const SRC = join(HERE, 'src'), PUBLIC = join(HERE, 'public'), DIST = join(HERE, 'dist'), WEB = join(HERE, '../web');
const VERSION = Date.now().toString(36);

const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

// ---- the demo library, and the demo's own server to ask it things ----

// Where the library is: public/demo, unless DEMO_OUT points `npm run library` somewhere else.
const LIBRARY = process.env.DEMO_OUT ?? join(PUBLIC, 'demo');
const libFile = join(LIBRARY, 'library.json');
if (!existsSync(libFile)) {
  console.error(`The demo library is missing (${libFile}). Run \`npm run library\` first.`);
  process.exit(1);
}
const lib = JSON.parse(await readFile(libFile, 'utf8'));
const { shots } = JSON.parse(await readFile(join(HERE, 'demo-library/shots.json'), 'utf8'));
const items = new Map(lib.items.map((i) => [i.id, i]));
const shot = Object.fromEntries(shots.map((s) => [s.id, s]));
const item = (ref) => {
  const i = items.get(pictureId(ref));
  if (!i) throw new Error(`no picture ${ref} in the demo library`);
  return i;
};

// The demo server (web/src/demo/engine.ts), bundled for Node, answers the search showcase exactly
// as the demo would.
async function demoServer() {
  const { build } = await import(pathToFileURL(join(WEB, 'node_modules/esbuild/lib/main.js')).href);
  const out = join(HERE, '.engine.mjs');
  await build({ entryPoints: [join(WEB, 'src/demo/engine.ts')], bundle: true, format: 'esm', platform: 'neutral', mainFields: ['module', 'main'], outfile: out, logLevel: 'warning' });
  globalThis.navigator ??= { userAgent: 'node' };
  const { DemoServer } = await import(pathToFileURL(out).href + `?v=${VERSION}`);
  await rm(out);
  return new DemoServer(lib);
}
const server = await demoServer();
const ask = (method, path, body = {}) => {
  const u = new URL(path, 'https://photostead.com');
  const r = server.handle({ method, path: u.pathname, query: u.searchParams, body });
  if (r.status >= 400) throw new Error(`${method} ${path}: ${r.status}`);
  return r.body;
};

// ---- pieces of the pages ----

const media = (kind, id) => `/demo/media/${kind}/${id}.webp`;

// Placeholders while a picture loads: its ThumbHash as a tiny WebP (about 150 bytes, where the
// PNG thumbHashToDataURL makes is 4 KB), or just its average colour.
const placeholders = new Map(
  await Promise.all(
    lib.items.filter((i) => i.thumbhash).map(async (i) => {
      const { w, h, rgba } = thumbHashToRGBA(Buffer.from(i.thumbhash, 'base64'));
      const webp = await sharp(Buffer.from(rgba), { raw: { width: w, height: h, channels: 4 } }).webp({ quality: 60 }).toBuffer();
      return [i.id, `data:image/webp;base64,${webp.toString('base64')}`];
    }),
  ),
);
const ph = (i) => (placeholders.has(i?.id) ? `background-image:url(${placeholders.get(i.id)})` : '');
const average = (i) => {
  if (!i?.thumbhash) return '';
  const { r, g, b } = thumbHashToAverageRGBA(Buffer.from(i.thumbhash, 'base64'));
  return '#' + [r, g, b].map((v) => Math.round(v * 255).toString(16).padStart(2, '0')).join('');
};

// The app's icons (web/src/icons.tsx), so the site draws the same ones.
const iconSrc = await readFile(join(WEB, 'src/icons.tsx'), 'utf8');
const ICONS = Object.fromEntries([...iconSrc.matchAll(/^\s+(\w+): icon\((.+)\),$/gm)].map(([, name, body]) => [name, body.replace(/^<>|<\/>$/g, '')]));
ICONS.arrow = '<path d="M5 12h14M13 6l6 6-6 6" />';
ICONS.pause = '<path d="M9 6v12M15 6v12" />';
const icon = (name) => {
  if (!ICONS[name]) throw new Error(`no icon ${name}`);
  return `<svg viewBox="0 0 24 24" aria-hidden="true">${ICONS[name]}</svg>`;
};

// Photostead for iPhone's App Store page (live since 2026-11-20). The pages' {{appStore:…}} places
// show Apple's App Store badge linking to it; with APP_STORE_URL= (empty) they say "Coming soon …"
// instead. The badge is Apple's own artwork, unaltered, from Apple's marketing tools
// (public/img/app-store-badge.svg, black, US-UK).
const APP_STORE_URL = process.env.APP_STORE_URL ?? 'https://apps.apple.com/app/photostead/id6835919619';
const appStore = (soon) => {
  if (!APP_STORE_URL) return `<span class="btn btn-soon" role="note">${icon('phone')} ${esc(soon)}</span>`;
  if (!existsSync(join(PUBLIC, 'img/app-store-badge.svg'))) throw new Error('APP_STORE_URL is set, but Apple’s badge (public/img/app-store-badge.svg) is missing');
  return `<a class="store" href="${esc(APP_STORE_URL)}"><img src="/img/app-store-badge.svg" alt="Download on the App Store" width="162" height="54" /></a>`;
};

/** The hero's wall: columns of the library's photos, each column three times over so it can loop
 *  (it drifts by one copy, and two copies are always taller than the hero). The pictures are lazy,
 *  so the columns a narrow screen hides aren't fetched. */
function wall() {
  const photos = lib.items.filter((i) => !i.labels.length && !i.generation?.prompt?.includes('Character design'));
  const pick = photos.filter((_, n) => n % 2 === 0).slice(0, 42);
  const cols = 7;
  return Array.from({ length: cols }, (_, c) => {
    const col = pick.filter((_, n) => n % cols === c);
    const imgs = col.map((i) => `<img src="${media('m', i.id)}" alt="" width="320" height="400" loading="lazy" decoding="async" />`).join('');
    return `<div class="wall-col" style="--t:${70 + c * 9}s">${imgs.repeat(3)}</div>`;
  }).join('');
}

function faces() {
  const { people } = ask('GET', '/api/v1/people');
  return people
    .map((p) => `<div class="face"><img src="${media('f', p.coverFace)}" alt="" loading="lazy" width="256" height="256" /><b>${esc(p.name)}</b><span>${p.count} ${p.count === 1 ? 'item' : 'items'}</span></div>`)
    .join('');
}

// Memories as the server makes them (server/internal/api/memories.go): a day from years ago, and a
// person over the years. Each one's title and dates come from the library, worked out as the server
// would; the days are fixed, so the strip doesn't change with the day the site is built. A person is
// someone in the viewer's own photos (the server keeps faces per owner), so their cards count those.
function memories() {
  const monthYear = (d) => d.toLocaleDateString('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' });
  const day = (ref) => {
    const d = new Date(`${item(ref).takenAt.slice(0, 10)}T12:00:00Z`);
    const n = new Date().getUTCFullYear() - d.getUTCFullYear();
    if (n < 1) throw new Error(`the memory for ${ref} is from this year`);
    return [ref, n === 1 ? 'A year ago' : `${n} years ago`, d.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric', timeZone: 'UTC' })];
  };
  const me = lib.members[0].id; // the demo's viewer
  const person = (ref, name) => {
    const p = lib.people.find((x) => x.name === name);
    const mine = lib.items.filter((i) => i.owner === me && i.people.includes(p.id) && !i.generation);
    const days = mine.map((i) => new Date(`${i.takenAt.slice(0, 10)}T12:00:00Z`)).sort((a, b) => a - b);
    if (!mine.includes(item(ref))) throw new Error(`${name} isn’t in ${ref}, or it isn’t the viewer’s`);
    const [first, last] = [days[0], days.at(-1)];
    return first.getUTCFullYear() === last.getUTCFullYear()
      ? [ref, `With ${name}`, `${first.toLocaleDateString('en-US', { month: 'long', timeZone: 'UTC' })} – ${monthYear(last)}`]
      : [ref, `${name} over the years`, `${first.getUTCFullYear()} – ${last.getUTCFullYear()}`];
  };
  const cards = [day('a14#2'), person('b15', 'Pepper'), person('b05', 'June'), day('b24#2'), person('b09', 'Leo')];
  return cards.map(([ref, t, s]) => {
    const i = item(ref);
    return `<a class="mem" href="/demo/" style="${ph(i)}"><img src="${media('t', i.id)}" alt="" loading="lazy" /><p><b>${esc(t)}</b><small>${esc(s)}</small></p></a>`;
  }).join('');
}

function aiStrip() {
  return ['d03', 'd09', 'd08', 'd11', 'd14', 'd13'].map((ref) => {
    const i = item(ref);
    return `<figure style="${ph(i)}"><img src="${media('t', i.id)}" alt="" loading="lazy" /></figure>`;
  }).join('');
}

function house() {
  const who = Object.fromEntries(lib.members.map((m) => [m.id, m.name]));
  return ['b17#2', 'b13', 'c10', 'b16#2', 'c12', 'b31'].map((ref) => {
    const i = item(ref);
    const name = who[i.owner];
    return `<figure style="${ph(i)}"><img src="${media('t', i.id)}" alt="" loading="lazy" /><figcaption><i>${name[0]}</i>${esc(name)}</figcaption></figure>`;
  }).join('');
}

// The search showcase: each query run through the demo's own search or Ask.
function search() {
  const queries = [
    { kind: 'search', label: 'beach at sunset' },
    { kind: 'ask', label: 'when did I last take pictures of Miso?' },
    { kind: 'search', label: 'birthday cake' },
    { kind: 'ask', label: 'favorites from 2025' },
    { kind: 'search', label: 'pastel de nata' },
    { kind: 'ask', label: 'how many photos of Pepper?' },
  ];
  const data = queries.map((q) => {
    let assets, answer;
    if (q.kind === 'ask') {
      const r = ask('POST', '/api/v1/ask', { q: q.label });
      // A question that names someone must be answered about them ("June" can be read as the month).
      for (const p of lib.people) {
        if (new RegExp(`\\b${p.name}\\b`).test(q.label) && !r.chips.some((c) => c.kind === 'Person' && c.label === p.name)) {
          throw new Error(`Ask reads “${q.label}” without ${p.name}: ${r.answer}`);
        }
      }
      assets = r.assets;
      answer = `<b>${esc(r.answer)}</b>${r.chips.length ? ' · ' + r.chips.map((c) => `${esc(c.kind)}: ${esc(c.label)}`).join(' · ') : ''}`;
    } else {
      const r = ask('GET', `/api/v1/search?q=${encodeURIComponent(q.label)}`);
      assets = r.assets;
      const by = [r.found?.text && `${r.found.text} by the words in the picture`, r.found?.prompts && `${r.found.prompts} by the prompt that made it`].filter(Boolean);
      answer = `<b>The ${assets.length} closest matches</b>, best first${by.length ? ' · ' + by.join(', ') : ''}`;
    }
    if (!assets.length) throw new Error(`the showcase query “${q.label}” finds nothing`);
    return { kind: q.kind, q: q.label, answer, items: assets.slice(0, 12).map((a) => ({ t: media('t', a.id), c: average(a) })) };
  });
  const chips = queries.map((q, n) => `<button type="button" class="s-chip" data-q="${n}" aria-pressed="false">${q.kind === 'ask' ? '<span class="k">Ask</span>' : ''}${esc(q.label)}</button>`).join('');
  return { chips, data: JSON.stringify(data).replace(/</g, '\\u003c') };
}

// Photostead for iPhone, without drawing an iPhone (Apple's marketing guidelines don't allow it):
// the newest photos in a camera roll, each marked as it reaches the server, and what the app does.
function iphone() {
  const roll = lib.items.filter((i) => i.type === 'image' && !i.generation && !i.labels.length && !i.archived && i.owner === lib.members[0].id).sort((a, b) => b.takenAt.localeCompare(a.takenAt)).slice(0, 8);
  const check = '<svg viewBox="0 0 24 24"><path d="m6.5 12.5 3.5 3.5 7.5-8" /></svg>';
  const tiles = roll.map((i, n) => `<figure${n === 0 ? ' class="going"' : ''} style="${ph(i)}"><img src="${media('t', i.id)}" alt="" loading="lazy" /><i>${n === 0 ? '' : check}</i></figure>`).join('');
  const points = [
    ['upload', 'Originals, as they are', 'Photos, videos and Live Photos, the photo and its clip. Edits made in Photos stay on the phone.'],
    ['clock', 'Carries on without you', 'iOS keeps sending after you leave the app, and gives Photostead time to back up more, often overnight.'],
    ['shield', 'Wi-Fi unless you say so', 'Backups wait for Wi-Fi, unless you turn on cellular data in Settings.'],
    ['search', 'The whole library, with you', 'Browse the timeline, albums and people, and search or ask, all from your server.'],
  ];
  return `<div class="sync" data-reveal>
    <div class="sync-roll" role="img" aria-label="The newest photos on an iPhone, all backed up to Photostead but the latest, which is on its way.">
      <p class="sync-label">${icon('phone')} This iPhone <span>newest first</span></p>
      <div class="sync-grid">${tiles}</div>
    </div>
    <div class="sync-wire" aria-hidden="true"><span></span></div>
    <div class="sync-home">
      <p class="sync-label">${icon('mark')} Your Photostead</p>
      <b>Backed up</b>
      <p>Byte for byte, to your own server: at home on your Wi-Fi, away through Tailscale.</p>
    </div>
  </div>
  <ul class="sync-points">${points.map(([ic, t, d], n) => `<li data-reveal style="--d: ${(n * 0.05).toFixed(2)}s">${icon(ic)}<span><b>${t}</b>${d}</span></li>`).join('')}</ul>`;
}

// ---- pages ----

const partials = {};
for (const f of await readdir(join(SRC, 'partials'))) partials[f.replace(/\.html$/, '')] = await readFile(join(SRC, 'partials', f), 'utf8');
const s = search();
const blocks = { wall: wall(), faces: faces(), memories: memories(), aiStrip: aiStrip(), house: house(), iphone: iphone(), searchChips: s.chips, searchData: s.data };

function render(html, file) {
  const m = html.match(/^<!--\s*(\{.*?\})\s*-->\s*/s);
  if (!m) throw new Error(`${file} has no front matter`);
  const meta = { scheme: 'light dark', ...JSON.parse(m[1]) };
  html = html.slice(m[0].length);
  html = html.replace(/<!-- include:(\w+) -->/g, (_, n) => {
    if (!(n in partials)) throw new Error(`${file}: no partial ${n}`);
    return partials[n];
  });
  html = html.replace(/\{\{(\w+)(?::([^}]+))?\}\}/g, (all, key, arg) => {
    switch (key) {
      case 'title': case 'description': case 'path': case 'scheme':
        return esc(meta[key]);
      case 'version': return VERSION;
      case 'year': return String(new Date().getFullYear());
      case 'icon': return icon(arg);
      case 'appStore': return appStore(arg);
      case 'p': return media('p', pictureId(arg));
      case 't': return media('t', pictureId(arg));
      case 'ph': return ph(item(arg));
      case 'prompt': return esc(shot[arg.split('#')[0]].prompt);
      case 'size': { const i = item(arg); return `${i.width.toLocaleString('en-US')} × ${i.height.toLocaleString('en-US')}`; }
      default:
        if (key in blocks) return blocks[key];
        throw new Error(`${file}: unknown placeholder ${all}`);
    }
  });
  return html;
}

async function pages(dir) {
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, e.name);
    if (e.isDirectory()) {
      if (!['partials', 'assets'].includes(e.name)) await pages(path);
    } else if (e.name.endsWith('.html')) {
      const out = join(DIST, relative(SRC, path));
      await mkdir(dirname(out), { recursive: true });
      await writeFile(out, render(await readFile(path, 'utf8'), relative(SRC, path)));
    }
  }
}

// Keep a demo build that's already there when only the pages are being built.
const keepDemo = process.env.SKIP_DEMO === '1' && existsSync(join(DIST, 'demo/index.html'));
if (keepDemo) {
  for (const e of await readdir(DIST)) if (e !== 'demo') await rm(join(DIST, e), { recursive: true, force: true });
} else {
  await rm(DIST, { recursive: true, force: true });
}
await mkdir(DIST, { recursive: true });
await cp(PUBLIC, DIST, { recursive: true });
if (LIBRARY !== join(PUBLIC, 'demo')) await cp(LIBRARY, join(DIST, 'demo'), { recursive: true });
await cp(join(SRC, 'assets'), join(DIST, 'assets'), { recursive: true });
await pages(SRC);

if (!keepDemo) {
  console.log('building the demo (web, --mode demo)…');
  execFileSync('npm', ['run', 'build:demo'], { cwd: WEB, stdio: 'inherit' });
  await cp(join(WEB, 'dist-demo'), join(DIST, 'demo'), { recursive: true });
  // The demo takes its fonts from the site, so nothing loads from anywhere but photostead.com.
  const page = join(DIST, 'demo/index.html');
  const html = (await readFile(page, 'utf8'))
    .replace(/\s*<link rel="preconnect" href="https:\/\/fonts\.[^>]+>/g, '')
    .replace(/<link rel="stylesheet" href="https:\/\/fonts\.googleapis\.com[^>]+>/, '<link rel="stylesheet" href="/fonts/fonts.css" />');
  if (html.includes('googleapis')) throw new Error('the demo still loads fonts from Google');
  await writeFile(page, html);
}
console.log(`built ${relative(process.cwd(), DIST) || 'dist'} (${lib.items.length} demo pictures, version ${VERSION})`);
const missing = ['timeline-dark', 'album-light'].flatMap((n) => [n, `${n}-1x`]).filter((n) => !existsSync(join(PUBLIC, 'img/screens', `${n}.webp`)));
if (missing.length || !existsSync(join(PUBLIC, 'img/og.jpg'))) console.warn('The landing page’s screenshots are missing: run `npm run screens` now.');
