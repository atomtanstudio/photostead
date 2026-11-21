// Loads the demo household into a real Photostead server, for the App Store's screenshots and App
// Review's demo server. The website's demo answers from public/demo/library.json in the browser;
// this makes real files of the same pictures, with the same dates, places, cameras and AI labels
// written into them, uploads them as their owners, and then sets up what the website shows: the
// albums, the pictures shared with the household, the Lisbon album shared with Maya (who may add
// to it) and Rosa, with their comments and picks, and, once faces are found, people's names.
//
//   SERVER=http://127.0.0.1:4810 PASSWORD=… node demo-library/load.mjs
//   SERVER=… PASSWORD=… node demo-library/load.mjs people    (once the AI worker has run)
//
// On a new server it makes Sam the admin, and Maya and Rosa members of the household, all with
// PASSWORD (12 characters or more); run again, it signs them in and does only what's missing.
// Needs exiftool, and public/demo/library.json (npm run library). The files it makes are kept in
// demo-library/.server/ (DEMO_FILES elsewhere).
//
// The household's photos carry illustrative camera details, as on the website; the AI pictures
// carry their prompt under IPTC's "made by a generative model" label.

import { execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync } from 'node:fs';
import { copyFile, mkdir, open, readFile, rename } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { MEMBERS, SHARED } from './config.mjs';
import { original } from './download.mjs';
import { pictureId, uuid } from './ids.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const FILES = process.env.DEMO_FILES ?? join(HERE, '.server');
const SERVER = (process.env.SERVER ?? '').replace(/\/+$/, '');
const PASSWORD = process.env.PASSWORD ?? '';
const run = promisify(execFile);

if (!SERVER || PASSWORD.length < 12) {
  console.error('Set SERVER (e.g. http://127.0.0.1:4810) and PASSWORD (12 characters or more).');
  process.exit(2);
}

// ---- The server's API

async function request(session, method, path, body) {
  const headers = {};
  if (session) headers.cookie = `ps_session=${session}`;
  if (body !== undefined && !(body instanceof FormData)) headers['content-type'] = 'application/json';
  const res = await fetch(SERVER + path, { method, headers, body: body === undefined || body instanceof FormData ? body : JSON.stringify(body) });
  const text = await res.text();
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text.slice(0, 400)}`);
  return { res, data: text ? JSON.parse(text) : null };
}
const api = async (session, method, path, body) => (await request(session, method, path, body)).data;
const sessionOf = (res) => res.headers.getSetCookie().map((c) => c.match(/^ps_session=([^;]+)/)?.[1]).find(Boolean);
const signIn = async (email) => sessionOf((await request(null, 'POST', '/api/v1/auth/login', { email, password: PASSWORD })).res);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- The library, and the files made from it

const library = JSON.parse(await readFile(join(HERE, '../public/demo/library.json'), 'utf8'));
const { shots } = JSON.parse(await readFile(join(HERE, 'shots.json'), 'utf8'));
const shotOf = new Map(shots.flatMap((s) => s.images.map((_, n) => [uuid(`${s.id}#${n + 1}`), [s, n]])));
const memberKey = Object.fromEntries(library.members.map((m) => [m.id, MEMBERS.find((x) => x.email === m.email).key]));
const albumTitle = Object.fromEntries(library.albums.map((a) => [a.id, a.title]));
const personName = Object.fromEntries(library.people.map((p) => [p.id, p.name]));
const when = (t) => Date.parse(t); // the same instant, however it's written

/** The picture as a file a camera or a generator would have made, in FILES. */
async function fileFor(item) {
  const out = join(FILES, `${item.id}.jpg`);
  if (existsSync(out)) return out;
  const [shot, n] = shotOf.get(item.id);
  const src = await original(shot, n);
  const head = Buffer.alloc(3);
  const fh = await open(src);
  await fh.read(head, 0, 3, 0);
  await fh.close();
  const tmp = `${out}.part`;
  if (head[0] === 0xff && head[1] === 0xd8) await copyFile(src, tmp);
  else await run('sips', ['-s', 'format', 'jpeg', '-s', 'formatOptions', '92', src, '--out', tmp]);

  const stamp = item.takenAt.slice(0, 19).replace('T', ' ').replace(/-/g, ':');
  const off = item.takenAt.slice(19);
  const args = ['-q', '-q', '-overwrite_original', '-all=', '-tagsfromfile', '@', '-icc_profile',
    `-DateTimeOriginal=${stamp}`, `-CreateDate=${stamp}`, `-OffsetTimeOriginal=${off}`, `-OffsetTimeDigitized=${off}`, `-OffsetTime=${off}`];
  if (item.location) {
    const { lat, lon } = item.location;
    args.push(`-GPSLatitude=${Math.abs(lat)}`, `-GPSLatitudeRef=${lat < 0 ? 'S' : 'N'}`, `-GPSLongitude=${Math.abs(lon)}`, `-GPSLongitudeRef=${lon < 0 ? 'W' : 'E'}`);
  }
  if (item.camera) {
    const c = item.camera;
    args.push(`-Make=${c.make}`, `-Model=${c.model}`, `-LensModel=${c.lens}`, `-FocalLength=${c.focalMm}`, `-FNumber=${c.fNumber}`,
      `-ExposureTime=${c.exposureS}`, `-ISO=${c.iso}`);
  }
  if (item.generation) {
    args.push('-XMP-iptcExt:DigitalSourceType=http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia',
      `-ImageDescription=${item.generation.prompt}`);
  }
  await run('exiftool', [...args, tmp]);
  await rename(tmp, out);
  return out;
}

const sha256 = async (file) => createHash('sha256').update(await readFile(file)).digest('hex');

/** Everything in the timeline, or in a collection (archive, person:{id}, …), month by month. */
async function listed(session, collection) {
  const c = collection ? `&collection=${collection}` : '';
  const { buckets } = await api(session, 'GET', `/api/v1/timeline/buckets?${c}`);
  const months = await Promise.all((buckets ?? []).map((b) => api(session, 'GET', `/api/v1/timeline?month=${b.month}${c}`)));
  const shown = months.flatMap((m) => m.assets ?? []);
  // A stack (AI pictures made together, say) shows only its cover: the rest come from the stack.
  const stacked = await Promise.all(shown.filter((a) => a.stackSize > 1).map((a) => api(session, 'GET', `/api/v1/assets/${a.id}/stack`)));
  const seen = new Set(shown.map((a) => a.id));
  return [...shown, ...stacked.flatMap((s) => s.members ?? []).filter((a) => !seen.has(a.id) && seen.add(a.id))];
}

// ---- Steps

/** Sam, Maya and Rosa signed in (made first on a new server), by key, with their user ids. */
async function household() {
  const sam = MEMBERS[0];
  const setup = await api(null, 'GET', '/api/v1/setup');
  if (setup.needed) {
    console.log(`Making ${sam.name}, the admin`);
    await api(null, 'POST', '/api/v1/setup', { displayName: sam.name, email: sam.email, password: PASSWORD, timeZone: library.timeZone });
  }
  const sessions = { [sam.key]: await signIn(sam.email) };
  let home = await api(sessions[sam.key], 'GET', '/api/v1/household');
  if (home.name !== library.household.name) await api(sessions[sam.key], 'PATCH', '/api/v1/household', { name: library.household.name });
  for (const m of MEMBERS.slice(1)) {
    if (home.members.some((x) => x.email === m.email)) {
      sessions[m.key] = await signIn(m.email);
      continue;
    }
    console.log(`Inviting ${m.name}`);
    const invite = await api(sessions[sam.key], 'POST', '/api/v1/household/invites', { name: m.name, email: m.email, role: m.role === 'owner' ? 'adult' : m.role });
    sessions[m.key] = sessionOf((await request(null, 'POST', `/api/v1/invites/${invite.token}`, { displayName: m.name, email: m.email, password: PASSWORD })).res);
  }
  home = await api(sessions[sam.key], 'GET', '/api/v1/household');
  const ids = Object.fromEntries(MEMBERS.map((m) => [m.key, home.members.find((x) => x.email === m.email).id]));
  return { sessions, ids };
}

/** Uploads what each member doesn't have yet. */
async function upload(sessions) {
  await mkdir(FILES, { recursive: true });
  const files = new Map();
  process.stdout.write('Making the files ');
  for (const item of library.items) {
    files.set(item.id, await fileFor(item));
    process.stdout.write('.');
  }
  console.log(` ${files.size}`);
  for (const key of Object.keys(sessions)) {
    const mine = library.items.filter((i) => memberKey[i.owner] === key);
    if (!mine.length) continue;
    const sums = new Map(await Promise.all(mine.map(async (i) => [i.id, await sha256(files.get(i.id))])));
    const { known } = await api(sessions[key], 'POST', '/api/v1/uploads/check', { sha256: [...sums.values()] });
    const todo = mine.filter((i) => !(known ?? []).includes(sums.get(i.id)));
    console.log(`Uploading ${todo.length} of ${key}'s ${mine.length} pictures`);
    for (const item of todo) {
      const meta = { fileName: item.fileName.replace(/\.HEIC$/, '.JPG'), takenAt: item.takenAt, keywords: item.keywords, favorite: !!item.favorite,
        archived: !!item.archived, sha256: sums.get(item.id) };
      const form = new FormData();
      form.append('meta', JSON.stringify(meta));
      form.append('file', new Blob([await readFile(files.get(item.id))], { type: 'image/jpeg' }), meta.fileName);
      await api(sessions[key], 'POST', '/api/v1/uploads', form);
    }
  }
}

/** The server's id of every picture, by the library's id, once they've all been indexed. */
async function serverIds(sessions) {
  const ids = new Map();
  const byTime = new Map(library.items.map((i) => [`${memberKey[i.owner]}@${when(i.takenAt)}`, i.id]));
  for (let tries = 0; ; tries++) {
    for (const [key, session] of Object.entries(sessions)) {
      for (const a of [...(await listed(session)), ...(await listed(session, 'archive'))]) {
        if (!a || a.fromOthers) continue;
        const id = byTime.get(`${key}@${when(a.takenAt)}`);
        if (id) ids.set(id, a.id);
      }
    }
    if (ids.size === library.items.length) return ids;
    if (tries === 60) throw new Error(`only ${ids.size} of ${library.items.length} pictures came up in the timeline (missing: ${library.items.filter((i) => !ids.has(i.id)).map((i) => i.id).slice(0, 5).join(', ')}…)`);
    if (tries === 0) console.log('Waiting for the server to index them');
    await sleep(5000);
  }
}

async function share(sessions, ids) {
  for (const [key, session] of Object.entries(sessions)) {
    const mine = library.items.filter((i) => memberKey[i.owner] === key && i.shared && !i.archived).map((i) => ids.get(i.id));
    if (mine.length) {
      const { changed } = await api(session, 'POST', '/api/v1/household/share', { assetIds: mine });
      console.log(`${key} shared ${changed} more of ${mine.length} with the household`);
    }
  }
}

async function albums(sessions, memberIds, ids) {
  const sam = sessions[MEMBERS[0].key];
  const have = new Map(((await api(sam, 'GET', '/api/v1/albums?all=true')).albums ?? []).map((a) => [a.title, a]));
  const made = {};
  for (const a of library.albums) { // parents come first in config.mjs
    let album = have.get(a.title);
    if (!album) {
      album = await api(sam, 'POST', '/api/v1/albums', { title: a.title, ...(a.parent ? { parent: made[albumTitle[a.parent]] } : {}) });
      if (a.description) await api(sam, 'PATCH', `/api/v1/albums/${album.id}`, { description: a.description });
    }
    made[a.title] = album.id;
    const items = library.items.filter((i) => i.albums.includes(a.id)).map((i) => ids.get(i.id));
    if (items.length) await api(sam, 'POST', `/api/v1/albums/${album.id}/add`, { assetIds: items });
  }
  console.log(`${library.albums.length} albums`);

  // The album shared with the rest of the household, and what they said and picked there.
  const shared = made[SHARED.album];
  for (const m of SHARED.members) await api(sam, 'PUT', `/api/v1/albums/${shared}/people/${memberIds[m.member]}`, { role: m.role });
  const said = new Set(((await api(sam, 'GET', `/api/v1/albums/${shared}/comments`)).comments ?? []).map((c) => c.body));
  for (const c of SHARED.comments) {
    if (said.has(c.body)) continue;
    await api(sessions[c.member], 'POST', `/api/v1/albums/${shared}/comments`, { body: c.body, ...(c.shot ? { assetId: ids.get(pictureId(c.shot)) } : {}) });
  }
  for (const p of SHARED.picks) await api(sessions[p.member], 'PUT', `/api/v1/albums/${shared}/picks/${ids.get(pictureId(p.shot))}`);
  console.log(`${SHARED.album} shared with ${SHARED.members.map((m) => m.member).join(' and ')}, with ${SHARED.comments.length} comments and ${SHARED.picks.length} picks`);
}

/** Names the people face recognition found, by which pictures they're in. Run once the AI worker
 *  has looked at every picture. */
async function people(sessions, ids) {
  const back = new Map([...ids].map(([k, v]) => [v, k]));
  for (const [key, session] of Object.entries(sessions)) {
    const list = await api(session, 'GET', '/api/v1/people');
    if (list.faces?.waiting) console.log(`${key}: ${list.faces.waiting} pictures still waiting for faces`);
    const named = new Map((list.people ?? []).filter((p) => p.name).map((p) => [p.name, p.id]));
    for (const p of list.people ?? []) {
      if (p.name) continue;
      const items = (await listed(session, `person:${p.id}`)).map((a) => back.get(a.id)).filter(Boolean);
      // Who's in its pictures, counting only their kind (a dog's group is a dog's). It's named only
      // when one of them is in nearly every picture and nobody else is: a group whose pictures all
      // have both June and Maya in them could be either, so it's left for a person to name.
      const votes = {};
      for (const id of items) {
        const item = library.items.find((i) => i.id === id);
        for (const who of [...item.people, ...(item.characters ?? [])]) {
          const known = library.people.find((x) => x.id === who);
          if (known ? known.kind === p.kind && (known.species ?? null) === (p.species ?? null) : p.kind === 'human') votes[who] = (votes[who] ?? 0) + 1;
        }
      }
      const [[best, n] = [], [, next = 0] = []] = Object.entries(votes).sort((a, b) => b[1] - a[1]);
      const character = library.characters.find((c) => c.id === best);
      const name = character?.name ?? personName[best];
      if (best && items.length >= 2 && n >= items.length * 0.8 && next <= items.length * 0.75) {
        // A second group of someone already named (a dog seen from another side) joins the first.
        if (character) await api(session, 'POST', `/api/v1/people/${p.id}/character`, { name });
        else if (named.has(name)) await api(session, 'POST', `/api/v1/people/${p.id}/merge`, { into: named.get(name) });
        else named.set(name, (await api(session, 'PATCH', `/api/v1/people/${p.id}`, { name })).id);
        console.log(`${key}: ${name}${character ? ' (an AI character)' : named.get(name) !== p.id ? ' (joined)' : ''}, ${n} of ${items.length} pictures`);
      } else {
        const names = Object.fromEntries(Object.entries(votes).map(([k, v]) => [library.characters.find((c) => c.id === k)?.name ?? personName[k], v]));
        console.log(`${key}: left unnamed, a group of ${items.length} ${JSON.stringify(names)}`);
      }
    }
  }
}

const { sessions, ids: memberIds } = await household();
if (process.argv[2] === 'people') {
  await people(sessions, await serverIds(sessions));
} else {
  await upload(sessions);
  const ids = await serverIds(sessions);
  await share(sessions, ids);
  await albums(sessions, memberIds, ids);
  console.log(`Done: ${ids.size} pictures. Once the AI worker has found faces, run it again with "people" to name them.`);
}
