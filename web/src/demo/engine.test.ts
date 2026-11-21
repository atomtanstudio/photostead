import { describe, expect, it } from 'vitest';
import raw from '../../../site/public/demo/library.json?raw';
import type { Album, AlbumPerson, AskResult, AssetDetail, Household, LastingCode, MapPoint, Person, PhotoEdit, PlacesResult, RecentAsset, ShareLink, TimelineAsset } from '../api';
import { DemoServer } from './engine';
import type { DemoLibrary } from './library';

// Expected values are the server's: server/internal/api/ask.go run against the same library in Postgres.

const lib = JSON.parse(raw) as DemoLibrary;
const person = (name: string) => lib.people.find((p) => p.name === name)!.id;
const call = (s: DemoServer, method: string, path: string, body: Record<string, unknown> = {}) =>
  s.handle({ method, path, query: new URLSearchParams(), body: JSON.parse(JSON.stringify(body)) });
const ask = (s: DemoServer, body: Record<string, unknown>) => call(s, 'POST', '/api/v1/ask', body);

describe('Ask, as the server answers it', () => {
  it("finds the viewer's people in the viewer's own items", () => {
    const s = new DemoServer(lib);
    const leo = ask(s, { q: 'photos of Leo' }).body as AskResult;
    expect(leo.total).toBe(22);
    expect((ask(s, { q: 'when was the last time I took a picture of Sam' }).body as AskResult).answer)
      .toBe('The most recent was on Saturday 18 July 2026. There are 5 photos with Sam in all.');
    // The People page counts the same items.
    expect((call(s, 'GET', `/api/v1/people/${person('Maya')}`).body as Person).count).toBe((ask(s, { q: 'Maya' }).body as AskResult).total);
  });

  it('ranks every item for a description alone, whatever dates a plan sent back has', () => {
    const r = ask(new DemoServer(lib), { q: '', prev: { intent: 'list', text: 'beach', after: '2026-01-01' } }).body as AskResult;
    expect([r.total, r.order]).toEqual([130, 'relevance']);
  });

  it('reads a plan sent back as the server does', () => {
    const s = new DemoServer(lib);
    expect(ask(s, { q: 'x', prev: { intent: 'list', type: 'gif' } })).toEqual({ status: 422, body: { title: 'Unprocessable Entity', status: 422, detail: 'validation failed' } });
    const r = ask(s, { q: '', prev: { intent: 'list', people: [{ id: person('Sam').toUpperCase(), name: 'Sam' }] } }).body as AskResult;
    expect([(r.plan.people as { id: string }[])[0].id, r.total]).toEqual([person('Sam'), 5]);
  });
});

describe("Another member's items, as the server keeps them", () => {
  const me = lib.members[0].id;
  const theirs = lib.items.filter((i) => i.owner !== me && i.shared && !i.archived);
  const count = (s: DemoServer) => (ask(s, { q: 'how many photos' }).body as AskResult).total;

  it('are not the viewer\'s to trash, date, label or put in the vault', () => {
    const s = new DemoServer(lib);
    const before = count(s);
    const one = { assetIds: [theirs[0].id] };
    expect(call(s, 'POST', '/api/v1/assets/trash', one).body).toEqual({ changed: 0 });
    expect(call(s, 'POST', '/api/v1/labels/receipt/add', one).body).toEqual({ changed: 0 });
    expect(call(s, 'POST', '/api/v1/assets/date', { ...one, shiftSeconds: 86400 * 400 }).body).toEqual({ changed: 0, shiftSeconds: 86400 * 400 });
    expect(call(s, 'POST', '/api/v1/vault/add', one).status).toBe(409); // no PIN yet
    call(s, 'POST', '/api/v1/vault/pin', { pin: '123456' });
    expect(call(s, 'POST', '/api/v1/vault/add', one).body).toEqual({ changed: 0 });
    expect(count(s)).toBe(before);
    expect((ask(s, { q: 'receipts' }).body as AskResult).assets.some((a) => a.id === theirs[0].id)).toBe(false);
    // The viewer's own still are.
    const mine = lib.items.find((i) => i.owner === me && !i.archived)!;
    expect(call(s, 'POST', '/api/v1/assets/trash', { assetIds: [mine.id, mine.id] }).body).toEqual({ changed: 1 });
    expect(count(s)).toBe(before - 1);
  });

  it('leave the viewer\'s view when taken out of the household', () => {
    const s = new DemoServer(lib);
    const before = count(s);
    expect(call(s, 'POST', '/api/v1/household/unshare', { assetIds: [theirs[0].id] }).body).toEqual({ changed: 1 });
    expect(count(s)).toBe(before - 1);
    expect(call(s, 'GET', `/api/v1/assets/${theirs[0].id}`).status).toBe(404);
    // and they can't share it back: it's theirs.
    expect(call(s, 'POST', '/api/v1/household/share', { assetIds: [theirs[0].id] }).body).toEqual({ changed: 0 });
  });
});

describe('What main added since the demo was made', () => {
  const get = (s: DemoServer, path: string, q: Record<string, string> = {}) =>
    s.handle({ method: 'GET', path, query: new URLSearchParams(q), body: {} });
  const album = (title: string) => lib.albums.find((a) => a.title === title)!.id;
  const mine = lib.items.find((i) => i.owner === lib.members[0].id && i.type === 'image' && i.placeId)!;

  it('lists places by country, most items first, with no map', () => {
    const s = new DemoServer(lib);
    const r = get(s, '/api/v1/places').body as PlacesResult;
    expect(r.map).toBeUndefined();
    expect(r.attribution).toBe('Places from GeoNames (CC BY 4.0)');
    expect(r.places.map((p) => p.country)).toEqual([...r.places.map((p) => p.country)].sort());
    const kyoto = r.places.find((p) => p.name === 'Kyoto')!;
    const there = get(s, '/api/v1/timeline/buckets', { collection: `place:${kyoto.id}` }).body as { total: number };
    expect(there.total).toBe(kyoto.count);
    // An item's place reads as the server writes it, and links to the place.
    const d = get(s, `/api/v1/assets/${mine.id}`).body as AssetDetail;
    expect(d.location?.placeId).toBe(mine.placeId);
    expect(d.location?.place).toMatch(/, (United States|Portugal|Japan|Iceland)$/);
    expect((get(s, '/api/v1/map').body as { points: MapPoint[] }).points.length).toBe(lib.items.filter((i) => i.location && !i.archived).length);
  });

  it('pages Recently added newest-arrived first', () => {
    const s = new DemoServer(lib);
    const first = get(s, '/api/v1/timeline/recent', { limit: '50' }).body as { assets: RecentAsset[]; next?: string };
    expect(first.assets).toHaveLength(50);
    const rest = get(s, '/api/v1/timeline/recent', { limit: '500', cursor: first.next! }).body as { assets: RecentAsset[]; next?: string };
    const all = [...first.assets, ...rest.assets];
    expect(rest.next).toBeUndefined();
    expect(new Set(all.map((a) => a.id)).size).toBe(all.length);
    expect(all.map((a) => a.addedAt)).toEqual([...all.map((a) => a.addedAt)].sort().reverse());
  });

  it('keeps an edit, waits for its previews, and undoes it', () => {
    const s = new DemoServer(lib);
    const path = `/api/v1/assets/${mine.id}/edit`;
    expect((get(s, path).body as PhotoEdit).recipe).toBeNull();
    expect(call(s, 'PUT', path, { rotate: 45 }).status).toBe(422);
    const e = call(s, 'PUT', path, { rotate: 90, crop: { x: 0, y: 0, w: 0.5, h: 1 } }).body as PhotoEdit;
    expect([e.width, e.height]).toEqual([Math.round(mine.height * 0.5), mine.width]);
    expect((get(s, `/api/v1/assets/${mine.id}`).body as AssetDetail).render).toBe('pending');
    expect(s.rendered(mine.id, e.recipe!, 'NEW')).toBe(true);
    const d = get(s, `/api/v1/assets/${mine.id}`).body as AssetDetail;
    expect([d.render, d.thumbhash, d.edited]).toEqual(['done', 'NEW', true]);
    expect((call(s, 'DELETE', path).body as PhotoEdit).recipe).toBeNull();
    expect((get(s, `/api/v1/assets/${mine.id}`).body as AssetDetail).thumbhash).toBe(mine.thumbhash);
    const theirs = lib.items.find((i) => i.owner !== lib.members[0].id && i.shared)!;
    expect(call(s, 'PUT', `/api/v1/assets/${theirs.id}/edit`, { rotate: 90 }).status).toBe(403);
  });

  it('hides adult content everywhere but its own page, when chosen', () => {
    const s = new DemoServer(lib);
    call(s, 'POST', '/api/v1/labels/adult/add', { assetIds: [mine.id] });
    call(s, 'PATCH', '/api/v1/auth/me/settings', { adult: 'hide' });
    const ids = (q: Record<string, string>) => (get(s, '/api/v1/timeline', q).body as { assets: TimelineAsset[] }).assets.map((a) => a.id);
    expect(ids({ month: mine.takenAt.slice(0, 7) })).not.toContain(mine.id);
    expect(ids({ collection: 'adult', month: mine.takenAt.slice(0, 7) })).toContain(mine.id);
    expect((get(s, '/api/v1/map').body as { points: MapPoint[] }).points.map((p) => p.id)).not.toContain(mine.id);
  });

  it('makes share links with a QR code', () => {
    const s = new DemoServer(lib);
    const made = call(s, 'POST', `/api/v1/albums/${album('Kyoto')}/links`, { allowDownload: true });
    const l = made.body as ShareLink;
    expect(made.status).toBe(201);
    expect(l.url).toMatch(/^https:\/\/photostead\.your-tailnet\.ts\.net\/s\/[A-Za-z0-9_-]{24}$/);
    expect(l.qr).toMatch(/^data:image\/svg\+xml;base64,/);
    expect([l.public, l.allowDownload, l.hasPassword]).toEqual([false, true, false]);
    expect((get(s, `/api/v1/albums/${album('Kyoto')}/links`).body as { links: ShareLink[] }).links).toHaveLength(1);
    expect(call(s, 'DELETE', `/api/v1/links/${l.id}`).status).toBe(204);
  });

  it('makes, lists and revokes pairing codes that last', () => {
    const s = new DemoServer(lib);
    expect((get(s, '/api/v1/devices/pairing/lasting').body as { codes: LastingCode[] }).codes).toEqual([]);
    const made = call(s, 'POST', '/api/v1/devices/pairing/lasting', { userId: lib.members[1].id, days: 3, note: ' Mum’s\n iPad ' });
    const p = made.body as { code: string; link: string; qr: string; lasting: LastingCode };
    expect(made.status).toBe(200);
    expect(p.code).toMatch(/^[A-HJKMNP-Z2-9]{4}-[A-HJKMNP-Z2-9]{4}$/);
    expect(p.link).toContain(`code=${p.code}`);
    expect(p.qr).toMatch(/^data:image\/svg\+xml;base64,/);
    expect([p.lasting.name, p.lasting.devices, p.lasting.maxDevices, p.lasting.note]).toEqual([lib.members[1].name, 0, 5, 'Mum’s iPad']);
    const listed = (get(s, '/api/v1/devices/pairing/lasting').body as { codes: LastingCode[] }).codes;
    expect(listed.map((c) => c.note)).toEqual(['Mum’s iPad']);
    expect((call(s, 'POST', '/api/v1/devices/pairing/lasting', {}).body as { lasting: LastingCode }).lasting.note).toBe('');
    expect(call(s, 'POST', '/api/v1/devices/pairing/lasting', { days: 91 }).status).toBe(422);
    expect(call(s, 'POST', '/api/v1/devices/pairing/lasting', { note: 'é'.repeat(61) }).status).toBe(422);
    expect(call(s, 'DELETE', `/api/v1/devices/pairing/lasting/${p.lasting.id}`).status).toBe(204);
    expect(call(s, 'DELETE', `/api/v1/devices/pairing/lasting/${p.lasting.id}`).status).toBe(404);
  });

  it('answers the shared album’s activity, comments and picks', () => {
    const s = new DemoServer(lib);
    const base = `/api/v1/albums/${lib.shared.album}`;
    expect((get(s, `/api/v1/albums/${lib.shared.album}`).body as Album).members).toBe(2);
    const activity = (get(s, `${base}/activity`).body as { activity: { kind: string; items?: number }[] }).activity;
    expect(activity.filter((a) => a.kind === 'comment')).toHaveLength(lib.shared.comments.length);
    expect(activity.find((a) => a.kind === 'added')!.items).toBe(lib.items.filter((i) => i.albums.includes(lib.shared.album)).length);
    const picks = (get(s, `${base}/picks`).body as { picks: { assetId: string; people: string[] }[] }).picks;
    expect(picks.find((p) => p.people.length === 2)!.people).toEqual(['Rosa', 'Maya']);
    const asset = picks[0].assetId;
    expect(call(s, 'POST', `${base}/comments`, { body: '  ' }).status).toBe(422);
    expect(call(s, 'POST', `${base}/comments`, { body: 'Lovely', assetId: asset }).status).toBe(201);
    const about = s.handle({ method: 'GET', path: `${base}/comments`, query: new URLSearchParams({ asset }), body: {} }).body as { comments: { body: string }[] };
    expect(about.comments.at(-1)!.body).toBe('Lovely');
    call(s, 'PUT', `${base}/picks/${asset}`);
    expect((get(s, `${base}/picks`).body as { picks: { assetId: string; mine: boolean }[] }).picks.find((p) => p.assetId === asset)!.mine).toBe(true);
  });
});

describe('Removing a guest, as the server does', () => {
  // The demo library has no guests yet: one here, in the shared album, with a comment and a pick.
  const guest = { id: '0a6f1c2e-5b7d-4e8f-9a1b-2c3d4e5f6a7b', name: 'Ana', addedBy: lib.members[0].id, addedAt: '2025-04-18T20:12:00Z' };
  const asset = lib.shared.picks[0].assetId;
  const withGuest: DemoLibrary = {
    ...lib, guests: [guest], shared: {
      ...lib.shared, members: [...lib.shared.members, { member: guest.id, role: 'viewer' }],
      comments: [...lib.shared.comments, { id: '1b7e2d3f-6c8e-4f9a-8b2c-3d4e5f6a7b8c', member: guest.id, assetId: asset, body: 'Could I have a print?', at: '2025-04-20T10:00:00Z' }],
      picks: [...lib.shared.picks, { member: guest.id, assetId: asset, at: '2025-04-20T10:01:00Z' }],
    },
  };
  const base = `/api/v1/albums/${lib.shared.album}`;

  it('takes their comments and picks with them, and leaves the album and its photos', () => {
    const s = new DemoServer(withGuest);
    const comments = () => (call(s, 'GET', `${base}/comments`).body as { comments: { author: string; guest?: boolean }[] }).comments;
    const picked = () => (call(s, 'GET', `${base}/picks`).body as { picks: { people: string[] }[] }).picks.flatMap((p) => p.people);
    const activity = () => (call(s, 'GET', `${base}/activity`).body as { activity: { kind: string; who: string }[] }).activity;
    expect((call(s, 'GET', '/api/v1/household').body as Household).guests)
      .toEqual([{ id: guest.id, name: 'Ana', addedBy: lib.members[0].name, addedAt: guest.addedAt, albums: 1, devices: 0 }]);
    expect((call(s, 'GET', `${base}/people`).body as { members: AlbumPerson[] }).members.find((p) => p.id === guest.id))
      .toMatchObject({ name: 'Ana', guest: true, role: 'viewer' });
    expect(comments().filter((c) => c.guest).map((c) => c.author)).toEqual(['Ana']);
    expect(picked()).toContain('Ana');
    const album = call(s, 'GET', base).body as Album;
    expect(album.members).toBe(3);

    // Only a guest: a household member is refused.
    expect(call(s, 'DELETE', `/api/v1/household/guests/${lib.members[1].id}`).status).toBe(409);
    expect(call(s, 'DELETE', `/api/v1/household/guests/${guest.id}`).status).toBe(204);
    expect(call(s, 'DELETE', `/api/v1/household/guests/${guest.id}`).status).toBe(404);

    expect((call(s, 'GET', '/api/v1/household').body as Household).guests).toEqual([]);
    expect((call(s, 'GET', `${base}/people`).body as { members: AlbumPerson[] }).members.map((p) => p.id)).toEqual(lib.shared.members.map((m) => m.member));
    expect(comments()).toHaveLength(lib.shared.comments.length);
    expect(picked()).not.toContain('Ana');
    expect(activity().filter((a) => a.kind === 'comment')).toHaveLength(lib.shared.comments.length);
    expect(activity().some((a) => a.who === 'Ana')).toBe(false);
    const after = call(s, 'GET', base).body as Album;
    expect([after.count, after.members]).toEqual([album.count, 2]);
  });
});
