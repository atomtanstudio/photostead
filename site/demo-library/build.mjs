// Builds the demo's sample library: downloads every picture in shots.json once (into .originals/),
// makes the sizes Photostead's server makes (640px thumbnails, 1600px previews, ThumbHash
// placeholders, square face crops) plus 320px ones for the site's photo wall, and writes
// public/demo/library.json for the demo's in-browser server (web/src/demo). Run: npm run library.
// Pictures already made aren't downloaded or made again.

import { createHash } from 'node:crypto';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import sharp from 'sharp';
import { rgbaToThumbHash } from 'thumbhash';
import { ALBUMS, ARCHIVED, CAMERAS, COVERS, EXCLUDE, FAVORITES, HOUSEHOLD, MEMBERS, MODELS, PEOPLE, PLACES, SHARED, TIME_ZONE, owner } from './config.mjs';
import { CACHE, cached, original } from './download.mjs';
import { uuid } from './ids.mjs';
import { nearest } from './towns-near.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const OUT = process.env.DEMO_OUT ?? join(HERE, '../public/demo');

// A steady pseudo-random number in [0, 1) for a seed: file sizes and the like.
const unit = (seed) => parseInt(createHash('sha1').update(seed).digest('hex').slice(0, 8), 16) / 2 ** 32;

/** The UTC offset of a wall-clock time in a time zone, as +HH:MM, and in minutes. */
function offset(local, zone) {
  const asUTC = Date.parse(local + 'Z');
  const at = (t) => {
    const part = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'longOffset' }).formatToParts(t).find((p) => p.type === 'timeZoneName').value;
    const m = part.match(/GMT([+-])(\d\d):(\d\d)/);
    return m ? (m[1] === '-' ? -1 : 1) * (Number(m[2]) * 60 + Number(m[3])) : 0;
  };
  const min = at(asUTC - at(asUTC) * 60000);
  const abs = Math.abs(min);
  return { min, text: `${min < 0 ? '-' : '+'}${String(Math.floor(abs / 60)).padStart(2, '0')}:${String(abs % 60).padStart(2, '0')}` };
}

/** A plausible exposure for the light the scene was in. */
function exposure(shot, hour) {
  const night = hour >= 21 || hour < 6, dusk = hour >= 18 || hour < 8;
  const indoor = /\b(kitchen|table|bed|living room|sofa|studio|cabin|inside|window seat|counter)\b/i.test(shot.scene);
  if (shot.cam === 'a7c-night') return { exposureS: 15, iso: 3200 };
  if (night) return { exposureS: shot.cam.startsWith('phone') ? 1 / 15 : 1 / 30, iso: shot.cam.startsWith('phone') ? 1600 : 3200 };
  if (indoor) return { exposureS: 1 / 60, iso: shot.cam.startsWith('phone') ? 640 : 1600 };
  if (dusk) return { exposureS: 1 / 125, iso: 400 };
  return { exposureS: shot.cam === 'a7c-tele' ? 1 / 1000 : 1 / 500, iso: 100 };
}

const LABELS = { receipts: 'receipt', documents: 'document', qr: 'qr_code' };

async function main() {
  const { shots, people: refs } = JSON.parse(await readFile(join(HERE, 'shots.json'), 'utf8'));
  const { towns } = JSON.parse(await readFile(join(HERE, 'towns.json'), 'utf8'));
  const places = new Map(); // the towns pictures were taken near, by GeoNames id
  for (const d of ['', 'media/t', 'media/p', 'media/m', 'media/f']) await mkdir(join(OUT, d), { recursive: true });
  await mkdir(CACHE, { recursive: true });

  const members = MEMBERS.map((m) => ({ ...m, id: uuid(`member:${m.key}`) }));
  const memberId = Object.fromEntries(members.map((m) => [m.key, m.id]));
  const personId = Object.fromEntries(PEOPLE.map((p) => [p.key, uuid(`person:${p.key}`)]));
  const albumId = Object.fromEntries(ALBUMS.map((a) => [a.key, uuid(`album:${a.key}`)]));
  const nova = uuid('character:Nova');
  const counters = {};
  const items = [];
  // What an earlier build already made: a picture whose files are all there isn't downloaded again,
  // so albums, covers, people and the like can be changed without the originals.
  const before = existsSync(join(OUT, 'library.json')) ? new Map(JSON.parse(await readFile(join(OUT, 'library.json'), 'utf8')).items.map((i) => [i.id, i])) : new Map();

  for (const shot of shots) {
    for (const [n, img] of shot.images.entries()) {
      const id = uuid(`${shot.id}#${n + 1}`);
      const ref = `${shot.id}#${n + 1}`;
      if (EXCLUDE.includes(ref)) {
        for (const kind of ['t', 'p', 'm']) await rm(join(OUT, 'media', kind, `${id}.webp`), { force: true });
        continue;
      }
      const thumbFile = join(OUT, 'media/t', `${id}.webp`), previewFile = join(OUT, 'media/p', `${id}.webp`), miniFile = join(OUT, 'media/m', `${id}.webp`);
      let width, height, thumbhash, bytes;
      const old = before.get(id);
      if (old && [thumbFile, previewFile, miniFile].every((f) => existsSync(f))) {
        ({ width, height, thumbhash } = old);
        bytes = old.fileSize;
      } else {
        const src = await original(shot, n);
        const base = sharp(src).rotate();
        ({ width, height } = await base.metadata());
        await base.clone().resize(640, 640, { fit: 'inside' }).webp({ quality: 74, effort: 5 }).toFile(thumbFile);
        await base.clone().resize(320, 320, { fit: 'inside' }).webp({ quality: 62, effort: 5 }).toFile(miniFile);
        await base.clone().resize(1600, 1600, { fit: 'inside', withoutEnlargement: true }).webp({ quality: 80, effort: 5 }).toFile(previewFile);
        const small = await base.clone().resize(100, 100, { fit: 'inside' }).ensureAlpha().raw().toBuffer({ resolveWithObject: true });
        thumbhash = Buffer.from(rgbaToThumbHash(small.info.width, small.info.height, small.data)).toString('base64');
        bytes = (await stat(src)).size;
      }

      // When: the shot's time, a little later for each further picture of it.
      const [lat, lon, zone] = PLACES[shot.place] ?? [undefined, undefined, TIME_ZONE];
      const t = new Date(Date.parse(shot.date + ':00Z') + n * (shot.ai ? 9000 : 41000));
      const local = t.toISOString().slice(0, 19);
      const off = offset(local, zone);
      const who = owner(shot);
      const day = local.slice(0, 10);
      const item = {
        id, type: 'image', takenAt: local + off.text, offsetMin: off.min, width, height, thumbhash, owner: memberId[who],
        folderPath: `library/${who}/photos/${day.slice(0, 4)}/${day}`, keywords: shot.kw ?? [], labels: shot.label ? [LABELS[shot.label]] : [],
        people: shot.ai ? [] : shot.refs.map((r) => personId[r]), albums: shot.album ? [albumId[shot.album]] : [],
        sees: [shot.scene, shot.place].filter(Boolean).join(' '),
      };
      if (shot.place) Object.assign(item, { place: shot.place, location: { lat: +(lat + (unit(id + 'la') - 0.5) * 0.002).toFixed(5), lon: +(lon + (unit(id + 'lo') - 0.5) * 0.002).toFixed(5) } });
      // Its place, as the server names it: the nearest town within 60 km (none past that).
      const town = item.location && nearest(towns, item.location.lat, item.location.lon);
      if (town) {
        const [gid, name, region, country, countryCode, tlat, tlon] = town;
        item.placeId = uuid(`place:${gid}`);
        places.set(gid, { id: item.placeId, name, region, country, countryCode, lat: tlat, lon: tlon });
      }
      // When it came into Photostead: an AI picture minutes after it was made, a phone's photo when
      // the phone next backed up, a camera's when its card was emptied a day or few later.
      const after = shot.ai ? 60 + unit(id + 'added') * 300 : shot.cam.startsWith('phone') ? 120 + unit(id + 'added') * 1800 : 86400 * (1 + unit(id + 'added') * 3);
      item.addedAt = new Date(Date.parse(local + off.text) + Math.round(after) * 1000).toISOString().replace('.000Z', 'Z');
      if (shot.text) item.text = shot.text;
      if (shot.images.filter((_, k) => !EXCLUDE.includes(`${shot.id}#${k + 1}`)).length > 1) item.group = shot.id;
      if (FAVORITES.includes(ref) || (n === 0 && FAVORITES.includes(shot.id))) item.favorite = true;
      if (ARCHIVED.includes(`${shot.id}#${n + 1}`)) item.archived = true;
      if (!shot.ai && !shot.label) item.shared = true;

      if (shot.ai) {
        const m = MODELS[shot.node];
        item.fileName = `${shot.node}-${img.asset}.jpg`;
        item.mime = 'image/jpeg';
        item.fileSize = bytes;
        item.generation = {
          kind: 'generated', source: 'iptc', evidence: 'xmp:digitalsourcetype', model: m.model, loras: [], prompt: shot.prompt,
          ...(m.steps ? { steps: m.steps, cfg: m.cfg } : {}), width, height, hasWorkflow: false,
          settings: { aspect_ratio: shot.ar, ...(shot.ref_url ? { reference_image: 'the first picture of Nova' } : {}) },
        };
        if (shot.character) item.characters = [nova];
      } else {
        const cam = CAMERAS[shot.cam];
        counters[cam.file] = (counters[cam.file] ?? 2140) + 1 + Math.floor(unit(id) * 6);
        item.fileName = `${cam.file}${String(counters[cam.file]).padStart(4, '0')}.${cam.ext}`;
        item.mime = cam.mime;
        item.fileSize = Math.round((cam.mb[0] + unit(id + 'size') * (cam.mb[1] - cam.mb[0])) * 1e6);
        item.camera = { make: cam.make, model: cam.model, lens: cam.lens, focalMm: cam.focalMm, fNumber: cam.fNumber, ...exposure(shot, t.getUTCHours()) };
      }
      items.push(item);
    }
    process.stdout.write('.');
  }
  console.log(`\n${items.length} pictures`);

  // Faces: a square around each face in the person's reference portrait. A 1200px copy of each
  // portrait is kept in refs/, so the crops can be changed without downloading them again.
  const people = [];
  await mkdir(join(HERE, 'refs'), { recursive: true });
  for (const p of PEOPLE) {
    const id = personId[p.key];
    const ref = join(HERE, 'refs', `${p.key}.webp`);
    if (!existsSync(ref)) await sharp(await cached(refs[p.key], `ref-${p.key}.jpg`)).resize(1200, 1200, { fit: 'inside' }).webp({ quality: 86 }).toFile(ref);
    const meta = await sharp(ref).metadata();
    const side = Math.round(Math.min(p.face.w * meta.width, p.face.h * meta.height));
    const left = Math.round(p.face.x * meta.width + (p.face.w * meta.width - side) / 2), top = Math.round(p.face.y * meta.height + (p.face.h * meta.height - side) / 2);
    await sharp(ref).extract({ left, top, width: side, height: side }).resize(256, 256).webp({ quality: 82 }).toFile(join(OUT, 'media/f', `${id}.webp`));
    people.push({ id, name: p.key, kind: p.kind, species: p.species, birthDate: p.birthDate, face: id });
  }

  const pictureId = (ref) => {
    const [shot, n] = ref.split('#');
    return uuid(`${shot}#${n ?? 1}`);
  };
  const cover = (key) => (COVERS[key] ? pictureId(COVERS[key]) : undefined);
  // Nothing in it changes from one build to the next unless its sources do, so a build with
  // nothing new leaves the committed files as they are.
  const library = {
    timeZone: TIME_ZONE,
    household: { id: uuid('household'), name: HOUSEHOLD },
    members: members.map(({ key, ...m }) => m),
    items,
    albums: ALBUMS.map((a) => ({ id: albumId[a.key], title: a.key, description: a.description, parent: a.parent && albumId[a.parent], cover: cover(a.key) })),
    people,
    characters: [{ id: nova, name: 'Nova', patterns: ['Nova'] }],
    places: [...places.values()].sort((a, b) => a.id.localeCompare(b.id)),
    shared: {
      album: albumId[SHARED.album],
      addedAt: SHARED.addedAt,
      members: SHARED.members.map((m) => ({ member: memberId[m.member], role: m.role })),
      comments: SHARED.comments.map((c, n) => ({ id: uuid(`comment:${n}`), member: memberId[c.member], assetId: c.shot && pictureId(c.shot), body: c.body, at: c.at })),
      picks: SHARED.picks.map((p) => ({ member: memberId[p.member], assetId: pictureId(p.shot), at: p.at })),
    },
  };
  for (const id of [...library.shared.comments.map((c) => c.assetId), ...library.shared.picks.map((p) => p.assetId)]) {
    if (id && !items.some((i) => i.id === id && i.albums.includes(library.shared.album))) throw new Error(`a comment or pick in SHARED is about ${id}, which isn't in ${SHARED.album}`);
  }
  await writeFile(join(OUT, 'library.json'), JSON.stringify(library));
  console.log(`wrote ${join(OUT, 'library.json')}`);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
