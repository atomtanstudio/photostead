import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { capturePlan, mediaUrl, serverUrl, uploadItem, parsePairing, reachable, forgetReachable, addressesOf } from '../lib/photostead.js';
const settings = { serverUrl: 'http://photostead.test:4800', apiKey: 'psk_test-only-key' };
const record = { focalTweetId: '123', capturedAt: '2026-09-06T00:00:00Z', tweets: [
  { rest_id: '999', full_text: 'reply', extended_entities: { media: [{ type: 'photo', media_url_https: 'https://pbs.twimg.com/media/reply.jpg' }] } },
  { rest_id: '123', full_text: 'short', note_tweet: { note_tweet_results: { result: { text: 'Full tweet <text> & context' } } }, author: { name: 'Author', screen_name: 'writer' }, extended_entities: { media: [
    { id_str: 'photo', type: 'photo', media_url_https: 'https://pbs.twimg.com/media/photo.jpg?name=small' },
    { id_str: 'video', type: 'video', media_url_https: 'https://pbs.twimg.com/poster.jpg', video_info: { variants: [
      { content_type: 'application/x-mpegURL', url: 'https://video.twimg.com/v/list.m3u8' },
      { content_type: 'video/mp4', bitrate: 128, url: 'https://video.twimg.com/v/low.mp4' },
      { content_type: 'video/mp4', bitrate: 1024, url: 'https://video.twimg.com/v/high.mp4' },
    ] } },
  ] } },
] };
test('selected tweet only, all attachments, original photo and highest bitrate MP4', () => {
  const plan = capturePlan(record);
  assert.equal(plan.items.length, 2);
  assert.equal(new URL(plan.items[0].url).searchParams.get('name'), 'orig');
  assert.match(plan.items[1].url, /high\.mp4$/);
  assert.match(plan.description, /Full tweet <text> & context/);
  assert.match(plan.description, /https:\/\/x.com\/writer\/status\/123/);
  assert.ok(!JSON.stringify(plan).includes('reply.jpg'));
});
test('missing media, missing focal tweet, HLS-only video and unsafe URLs fail clearly', () => {
  assert.throws(() => capturePlan({ ...record, focalTweetId: '456' }), /selected tweet/);
  assert.throws(() => capturePlan({ ...record, tweets: [{ rest_id:'123' }] }), /No image/);
  for (const url of ['https://twimg.com.evil.test/a.jpg','file:///tmp/a','http://pbs.twimg.com/a.jpg','https://u:p@pbs.twimg.com/a']) assert.throws(() => mediaUrl(url));
  assert.throws(() => mediaUrl('https://video.twimg.com/a.m3u8', true), /MP4/);
  assert.equal(serverUrl('http://photostead.test:4800/'), settings.serverUrl);
  assert.throws(() => serverUrl('https://user:secret@photostead.test'));
});
test('upload sends the file with the tweet text, link and date; a retry never sends twice', async () => {
  const originalFetch = globalThis.fetch;
  let uploads = 0, downloads = 0, fail = true;
  const checkpoint = {};
  const snapshots = [];
  const item = capturePlan(record).items[0];
  globalThis.fetch = async (url, init = {}) => {
    if (String(url).includes('twimg.com')) {
      downloads++; assert.equal(init.headers, undefined); assert.equal(init.credentials, 'omit');
      return new Response(new Blob(['image bytes'], { type: 'image/jpeg' }));
    }
    if (String(url).endsWith('/api/v1/server')) return Response.json({ id: 'srv-1', name: 'Photostead' });
    assert.equal(String(url), 'http://photostead.test:4800/api/v1/uploads');
    assert.equal(init.headers.Authorization, `Bearer ${settings.apiKey}`);
    assert.equal(init.redirect, 'error');
    uploads++;
    if (fail) { fail = false; return Response.json({ status: 503, detail: 'restarting' }, { status: 503 }); }
    assert.equal(init.body.get('file').name, 'x-123-1.jpg');
    const meta = JSON.parse(await init.body.get('meta').text());
    assert.equal(meta.fileName, 'x-123-1.jpg');
    assert.equal(meta.sourceUrl, 'https://x.com/writer/status/123');
    assert.ok(meta.description.includes('Full tweet <text> & context'));
    return Response.json({ status: 'accepted', sha256: 'abc123', path: 'photos/2026/2026-09-06/x-123-1.jpg' }, { status: 202 });
  };
  forgetReachable();
  try {
    const persist = async c => snapshots.push(structuredClone(c));
    await assert.rejects(uploadItem(settings, item, checkpoint, persist), /503: restarting/);
    assert.equal(checkpoint.assetId, undefined);
    await uploadItem(settings, item, checkpoint, persist);
    await uploadItem(settings, item, checkpoint, persist);
    assert.equal(uploads, 2); assert.equal(downloads, 2);
    assert.equal(checkpoint.assetId, 'abc123'); assert.equal(checkpoint.described, true); assert.equal(snapshots.length, 1);
  } finally { globalThis.fetch = originalFetch; }
});
test('manifest has no hosted pairing or bulk capture and all referenced assets exist', () => {
  const root = new URL('../', import.meta.url);
  const m = JSON.parse(readFileSync(new URL('manifest.json', root)));
  assert.ok(!JSON.stringify(m).includes('feedsilo'));
  assert.ok(!JSON.stringify(m).includes('bulk-capture'));
  for (const file of [m.background.service_worker, m.action.default_popup, ...Object.values(m.icons), ...m.content_scripts.flatMap(c => [...c.js,...(c.css || [])]), ...m.web_accessible_resources.flatMap(r => r.resources)]) {
    assert.ok(readFileSync(new URL(file, root)).length);
  }
  for (const name of readdirSync(new URL('content/', root)).filter(n => n.endsWith('.js'))) {
    const code = readFileSync(new URL(`content/${name}`, root), 'utf8');
    assert.ok(!code.includes('"x-forensic-capture"'), name);
    assert.ok(!code.includes('__xfc') && !code.includes('data-xfc'), name);
  }
  const worker = readFileSync(new URL('background/service-worker.js', root), 'utf8');
  assert.ok(!worker.includes('/api/capture') && !worker.includes('feedsilo.app'));
});

test('actual GraphQL normalization retains selected tweet text and video variants', async () => {
  const { buildForensicBundleRecord } = await import('../lib/forensic-bundle.js');
  const id = '1964000000000000000';
  const raw = { __typename:'Tweet', rest_id:id, legacy:{ id_str:id, full_text:'Short text', created_at:'Sat Sep 05 12:00:00 +0000 2026', extended_entities:record.tweets[1].extended_entities }, note_tweet:record.tweets[1].note_tweet, core:{ user_results:{ result:{ __typename:'User', rest_id:'42', legacy:{ name:'Author', screen_name:'writer' } } } } };
  const built = await buildForensicBundleRecord({ captureId:'fixture', focalTweetId:id, capturedAt:record.capturedAt, pageUrl:`https://x.com/writer/status/${id}`, apiResponses:[{ operationName:'TweetResultByRestId', ok:true, data:{ data:{ tweetResult:{ result:raw } } } }], bulkCapture:true });
  const plan = capturePlan(built);
  assert.equal(plan.items.length,2);
  assert.match(plan.description,/Full tweet <text>/);
  assert.match(plan.items[1].url,/high\.mp4$/);
});

// FeedSilo's own extension, when its source is at hand (the Legion's coding folder, mounted).
const feedsilo = process.env.FEEDSILO_CAPTURE ?? ''; // FeedSilo's content/forensic-capture.js
test('both capture scripts can coexist without responding to each other’s page messages', { skip: !existsSync(feedsilo) && 'FeedSilo’s source isn’t mounted' }, async () => {
  const vm = await import('node:vm');
  const handlers = [], posts = [];
  const window = { addEventListener:(type, fn) => handlers.push(fn), postMessage:data => posts.push(data) };
  const context = vm.createContext({ window });
  const original = readFileSync(feedsilo,'utf8');
  const clone = readFileSync(new URL('../content/forensic-capture.js',import.meta.url),'utf8');
  vm.runInContext(original,context); vm.runInContext(clone,context);
  assert.equal(handlers.length,2);
  for (const source of ['x-forensic-capture','photostead-capture']) {
    posts.length=0;
    for (const fn of handlers) fn({ source:window, data:{ source, type:'forensic-capture-tweet', payload:{} } });
    assert.equal(posts.length,1); assert.equal(posts[0].source,source);
  }
});

test('a pairing link carries the code, the server and its addresses; a typed code is just the code', () => {
  const p = parsePairing(' photostead://pair?code=ABCD-2345&server=srv-1&url=https%3A%2F%2Fphotostead.tail.ts.net&url=http%3A%2F%2F192.168.1.10%3A4800 ');
  assert.deepEqual(p, { code: 'ABCD-2345', serverId: 'srv-1', urls: ['https://photostead.tail.ts.net', 'http://192.168.1.10:4800'] });
  assert.deepEqual(parsePairing('abcd 2345'), { code: 'abcd 2345', serverId: '', urls: [] });
  assert.deepEqual(addressesOf({ serverUrl: 'http://192.168.1.10:4800/', addresses: ['https://photostead.tail.ts.net', 'http://192.168.1.10:4800'] }),
    ['http://192.168.1.10:4800', 'https://photostead.tail.ts.net']);
});
test('the address that answers, as the same server, is used; another server at an address is not', async () => {
  forgetReachable();
  const settings = { serverUrl: 'http://192.168.1.10:4800', addresses: ['https://photostead.tail.ts.net'], serverId: 'srv-1', token: 'psd_test' };
  const asked = [];
  const away = async url => { asked.push(String(url)); if (String(url).startsWith('http://192.168')) throw new TypeError('fetch failed'); return Response.json({ id: 'srv-1' }); };
  assert.equal(await reachable(settings, away), 'https://photostead.tail.ts.net');
  assert.deepEqual(asked, ['http://192.168.1.10:4800/api/v1/server', 'https://photostead.tail.ts.net/api/v1/server']);
  forgetReachable();
  const impostor = async () => Response.json({ id: 'someone-else' });
  await assert.rejects(reachable(settings, impostor), /can't be reached/);
  forgetReachable();
});
