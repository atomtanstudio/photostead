// Screenshots of the demo for the landing page (public/img/screens) and the link preview image
// (public/img/og.jpg), copied into dist/img too. Serves dist/ itself, so build first: npm run build,
// then npm run screens.
// Needs Playwright's Chromium (npx playwright install chromium), or CHROMIUM=<the path of a Chromium
// already on this computer>, such as another Playwright version's headless shell.

import { createReadStream, existsSync, statSync } from 'node:fs';
import { cp, mkdir, readFile, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { dirname, extname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import sharp from 'sharp';
import { pictureId } from '../demo-library/ids.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const DIST = join(HERE, '../dist'), OUT = join(HERE, '../public/img');
const TYPES = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.webp': 'image/webp', '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg' };

const server = createServer((req, res) => {
  let p = join(DIST, decodeURIComponent(new URL(req.url, 'http://x').pathname));
  if (existsSync(p) && statSync(p).isDirectory()) p = join(p, 'index.html');
  if (!existsSync(p)) return res.writeHead(404).end();
  res.writeHead(200, { 'Content-Type': TYPES[extname(p)] ?? 'application/octet-stream' });
  createReadStream(p).pipe(res);
}).listen(0);
const base = `http://127.0.0.1:${server.address().port}`;

const lib = JSON.parse(await readFile(join(DIST, 'demo/library.json'), 'utf8'));
const album = (title) => lib.albums.find((a) => a.title === title).id;

const browser = await chromium.launch({ executablePath: process.env.CHROMIUM || undefined });
async function shot(name, { path, width, height, scheme, steps = [], scale = 2, quality = 82, zoom = 3 }) {
  const page = await browser.newPage({ viewport: { width, height }, colorScheme: scheme, deviceScaleFactor: scale });
  await page.addInitScript((z) => localStorage.setItem('ps.zoom', z), String(zoom));
  await page.goto(base + path, { waitUntil: 'networkidle' });
  for (const s of steps) await s(page);
  await page.addStyleTag({ content: '.demo-badge { display: none !important; }' }); // the badge is the demo's, not the app's
  await page.waitForTimeout(900);
  const png = await page.screenshot();
  await page.close();
  await sharp(png).webp({ quality, effort: 6 }).toFile(join(OUT, 'screens', `${name}.webp`));
  // and at one pixel per point, for the srcset of smaller screens
  await sharp(png).resize({ width }).webp({ quality, effort: 6 }).toFile(join(OUT, 'screens', `${name}-1x.webp`));
  console.log(`  ${name}.webp, ${name}-1x.webp`);
  return png;
}

// Scrolls the timeline to a month ("April 2025") the way a person would: along the scrubber.
const jumpTo = (month) => async (page) => {
  const ruler = await page.locator('.ruler').boundingBox();
  for (let y = ruler.y + 2; y < ruler.y + ruler.height; y += 2) {
    await page.mouse.move(ruler.x + ruler.width / 2, y);
    const tip = await page.locator('.ruler-tip').textContent().catch(() => '');
    if (tip?.startsWith(month)) {
      await page.mouse.click(ruler.x + ruler.width / 2, y);
      await page.mouse.move(12, 12); // off the scrubber and the grid, so nothing shows a hover
      await page.waitForTimeout(1200);
      return;
    }
  }
  throw new Error(`no ${month} on the scrubber`);
};

await mkdir(join(OUT, 'screens'), { recursive: true });
await shot('timeline-dark', { path: '/demo/', width: 1440, height: 900, scheme: 'dark', zoom: 2, steps: [jumpTo(process.env.HERO_MONTH ?? 'July 2025')] });
await shot('album-light', { path: `/demo/#/albums/${album('Lisbon')}`, width: 1200, height: 800, scheme: 'light' });
// The link preview (1200×630): the mark and the headline beside a few of the library's photos.
const og = await browser.newPage({ viewport: { width: 1200, height: 630 }, deviceScaleFactor: 1 });
const photo = (ref) => {
  const [shot, n = '1'] = ref.split('#');
  return `${base}/demo/media/t/${lib.items.find((i) => i.fileName && i.id === pictureId(`${shot}#${n}`)).id}.webp`;
};
const tiles = ['b14#2', 'c07', 'b13', 'a08', 'b17#2', 'c18', 'b05', 'c02#2', 'b24#2'].map(photo);
await og.goto(`${base}/404.html`); // the card's own origin, so its fonts load from the site
await og.setContent(`<!doctype html><html><head><base href="${base}/"><link rel="stylesheet" href="/fonts/fonts.css"><style>
  body { margin: 0; width: 1200px; height: 630px; background: #000; color: #f4f4f2; font-family: 'Hanken Grotesk', sans-serif; overflow: hidden; position: relative; }
  .copy { position: absolute; left: 72px; top: 0; bottom: 0; width: 560px; display: flex; flex-direction: column; justify-content: center; }
  .brand { display: flex; align-items: center; gap: 12px; font-family: 'Fragment Mono', monospace; font-size: 15px; letter-spacing: .24em; }
  .brand svg { width: 30px; height: 30px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
  h1 { margin: 34px 0 0; font-weight: 300; font-size: 92px; line-height: .95; letter-spacing: -.05em; }
  h1 em { font-family: 'Instrument Serif', serif; font-style: italic; font-weight: 400; letter-spacing: -.01em; }
  p { margin: 26px 0 0; font-size: 24px; line-height: 1.4; color: #9a9a95; }
  .grid { position: absolute; right: -60px; top: -40px; width: 560px; display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; transform: rotate(-4deg); }
  .grid img { width: 100%; aspect-ratio: 3 / 4; object-fit: cover; border-radius: 14px; }
  .fade { position: absolute; inset: 0; background: linear-gradient(90deg, #000 44%, rgba(0,0,0,.25) 62%, rgba(0,0,0,0) 80%); }
</style></head><body>
  <div class="grid">${[...tiles, ...tiles].slice(0, 12).map((t) => `<img src="${t}">`).join('')}</div>
  <div class="fade"></div>
  <div class="copy">
    <div class="brand"><svg viewBox="0 0 24 24"><path d="M3.5 10.6 12 3.6l8.5 7v9.1a.8.8 0 0 1-.8.8H4.3a.8.8 0 0 1-.8-.8z"/><circle cx="12" cy="14.2" r="3.3"/></svg>PHOTOSTEAD</div>
    <h1>A <em>home</em> for every photo.</h1>
    <p>Your family’s photos, on your own server.</p>
  </div>
</body></html>`, { waitUntil: 'networkidle' });
await og.evaluate(() => document.fonts.ready);
await og.screenshot({ path: join(OUT, 'og.png') });
await og.close();
await sharp(join(OUT, 'og.png')).jpeg({ quality: 86, mozjpeg: true }).toFile(join(OUT, 'og.jpg'));
await rm(join(OUT, 'og.png'));
console.log('  og.jpg');
await browser.close();
server.close();
await cp(OUT, join(DIST, 'img'), { recursive: true });
