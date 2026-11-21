import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { postPlan, siteOf, siteMediaUrl } from '../lib/sites.js';
import { uploadItem, forgetReachable } from '../lib/photostead.js';

// A site module, run with a stand-in PhotosteadSite that keeps what it was started with.
function loadSite(name) {
  let config;
  const P = {
    start: c => { config = c; },
    largest: (list, size) => [...(list || [])].sort((a, b) => size(b) - size(a))[0] || null,
  };
  vm.runInContext(readFileSync(new URL(`../content/sites/${name}.js`, import.meta.url), 'utf8'), vm.createContext({ window: { PhotosteadSite: P } }));
  return config;
}

test('pages and media servers are matched by host, not by look-alikes', () => {
  assert.equal(siteOf('https://civitai.com/images/1'), 'civitai');
  assert.equal(siteOf('https://www.instagram.com/p/abc/'), 'instagram');
  assert.equal(siteOf('https://www.tiktok.com/@a/video/1'), 'tiktok');
  assert.equal(siteOf('https://uk.pinterest.co.uk/pin/1/'), 'pinterest');
  assert.equal(siteOf('https://instagram.com.evil.test/'), '');
  assert.equal(siteOf('http://www.tiktok.com/'), '');
  assert.ok(siteMediaUrl('instagram', 'https://scontent-sea5-1.cdninstagram.com/v/a.jpg'));
  assert.ok(siteMediaUrl('civitai', 'https://blobs-b2.civitai.com/file/x'));
  for (const [site, url] of [['instagram', 'https://evilcdninstagram.com/a.jpg'], ['pinterest', 'http://i.pinimg.com/a.jpg'], ['tiktok', 'https://pinimg.com/a.jpg'], ['nope', 'https://i.pinimg.com/a.jpg']]) {
    assert.throws(() => siteMediaUrl(site, url), undefined, url);
  }
});

test('a post becomes one item per file, with its text, author, link and date', () => {
  const plan = postPlan({ site: 'instagram', id: 'DeJ28eTxo4s', url: 'https://www.instagram.com/p/DeJ28eTxo4s/', author: 'nasawebb', authorName: 'NASA Webb',
    caption: 'Protostars', takenAt: '2026-10-06T13:10:11Z', media: [
      { url: 'https://scontent.cdninstagram.com/v/1.jpg?x=1', key: '1' },
      { url: 'https://scontent.cdninstagram.com/v/2.mp4', video: true, key: '2' },
      { url: 'https://scontent.cdninstagram.com/v/1.jpg?x=2', key: '1' },
    ] });
  assert.equal(plan.tweetId, 'instagram:DeJ28eTxo4s');
  assert.deepEqual(plan.items.map(i => i.filename), ['instagram-DeJ28eTxo4s-1.jpg', 'instagram-DeJ28eTxo4s-2.mp4']);
  assert.equal(plan.items[0].description, 'Protostars\n\nNASA Webb · @nasawebb\n\nhttps://www.instagram.com/p/DeJ28eTxo4s/');
  assert.equal(plan.items[1].createdAt, '2026-10-06T13:10:11.000Z');
  assert.equal(plan.items[1].source, 'https://www.instagram.com/p/DeJ28eTxo4s/');
  assert.throws(() => postPlan({ site: 'instagram', id: 'x', url: 'https://www.tiktok.com/x', media: [{ url: 'https://scontent.cdninstagram.com/a.jpg' }] }), /link/);
  assert.throws(() => postPlan({ site: 'instagram', id: 'x', url: 'https://www.instagram.com/p/x/', media: [] }), /No images/);
  assert.throws(() => postPlan({ site: 'instagram', id: 'x', url: 'https://www.instagram.com/p/x/', media: [{ url: 'https://evil.test/a.jpg' }] }), /servers/);
});

test('CivitAI: the original file, the prompt and the generation settings', () => {
  const { toPost } = loadSite('civitai');
  const post = toPost({ id: 147394171,
    image: { url: '4851377b-uuid', mimeType: 'image/jpeg', type: 'image', createdAt: '2026-10-31T02:01:37.681Z', postId: 32034928, user: { username: 'LumaRift' } },
    generation: { meta: { prompt: 'A small figure on a swing', negativePrompt: 'blurry', seed: 958743388481746, steps: 10, sampler: 'er_sde_simple', cfgScale: 1 },
      resources: [{ modelName: 'riftGATE', versionName: 'Krea 2', modelType: 'LORA', strength: 0.5 }, { modelName: 'Kroma (Krea-2)', versionName: 'OPD', modelType: 'Checkpoint', strength: null }] },
    post: { title: 'Swings' } });
  const plan = postPlan(post);
  assert.equal(plan.items[0].url, 'https://image.civitai.com/xG1nkqKTMzGDvpLrqFT7WA/4851377b-uuid/original=true/4851377b-uuid.jpg');
  assert.equal(plan.items[0].filename, 'civitai-147394171-1.jpg');
  assert.equal(plan.items[0].prompt, 'A small figure on a swing');
  assert.equal(plan.items[0].source, 'https://civitai.com/images/147394171');
  const d = plan.description;
  for (const part of ['Swings', 'Prompt: A small figure on a swing', 'Negative prompt: blurry', 'Model: Kroma (Krea-2) · OPD', 'riftGATE (Krea 2) [LORA] 0.5', 'Seed: 958743388481746', 'Sampler: er_sde_simple', 'CFG: 1', '@LumaRift']) assert.ok(d.includes(part), part);
  const video = postPlan(toPost({ id: 5, image: { url: 'v-uuid', mimeType: 'video/mp4', type: 'video', user: { username: 'u' } } }));
  assert.equal(video.items[0].video, true);
  assert.match(video.items[0].url, /original=true\/v-uuid\.mp4$/);
});

test('Instagram: every carousel slide at its largest, videos as video, the caption and date', () => {
  const { toPost, mediaId } = loadSite('instagram');
  assert.equal(mediaId('DeJ28eTxo4s'), '4001971394119634476');
  const big = (n) => ({ candidates: [{ url: `https://scontent.cdninstagram.com/${n}-small.jpg`, width: 320, height: 320 }, { url: `https://scontent.cdninstagram.com/${n}-big.jpg`, width: 1440, height: 1440 }] });
  const post = toPost({ code: 'ABC123', taken_at: 1791292211, user: { username: 'nasa', full_name: 'NASA' }, caption: { text: 'Hello' }, carousel_media: [
    { pk: 1, image_versions2: big('a') },
    { pk: 2, image_versions2: big('b'), video_versions: [{ url: 'https://scontent.cdninstagram.com/v-low.mp4', width: 480, height: 852 }, { url: 'https://scontent.cdninstagram.com/v-high.mp4', width: 720, height: 1280 }] },
  ] });
  const plan = postPlan(post);
  assert.deepEqual(plan.items.map(i => [i.url.split('/').pop(), i.video]), [['a-big.jpg', false], ['v-high.mp4', true]]);
  assert.equal(plan.items[0].createdAt, new Date(1791292211000).toISOString());
  assert.match(plan.description, /^Hello\n\nNASA · @nasa\n\nhttps:\/\/www\.instagram\.com\/p\/ABC123\/$/);
  const reel = toPost({ code: 'R1', product_type: 'clips', video_versions: [{ url: 'https://scontent.cdninstagram.com/r.mp4', width: 1, height: 1 }], user: { username: 'u' } });
  assert.equal(reel.url, 'https://www.instagram.com/reel/R1/');
});

test('TikTok: the largest video (H.264 when tied), its other addresses, or every photo', () => {
  const { toPost } = loadSite('tiktok');
  const addr = (w, h, url) => ({ Width: w, Height: h, UrlList: [`https://v16-webapp-prime.us.tiktok.com/${url}`, `https://v19-webapp-prime.us.tiktok.com/${url}`] });
  const post = toPost({ id: '7681450201478892807', desc: 'Why do hens eat stones?', createTime: '1788477008', author: { uniqueId: 'homosapienstv', nickname: 'homosapiens' },
    video: { bitrateInfo: [
      { CodecType: 'h264', Bitrate: 1329247, PlayAddr: addr(576, 1024, 'h264-576') },
      { CodecType: 'h265_hvc1', Bitrate: 1330691, PlayAddr: addr(1080, 1920, 'h265-1080') },
      { CodecType: 'h264', Bitrate: 900000, PlayAddr: addr(1080, 1920, 'h264-1080') },
    ] } });
  const plan = postPlan(post);
  assert.equal(plan.items.length, 1);
  assert.equal(plan.items[0].url, 'https://v16-webapp-prime.us.tiktok.com/h264-1080');
  assert.deepEqual(plan.items[0].fallbacks, ['https://v19-webapp-prime.us.tiktok.com/h264-1080']);
  assert.equal(plan.items[0].filename, 'tiktok-7681450201478892807-1.mp4');
  assert.equal(plan.items[0].source, 'https://www.tiktok.com/@homosapienstv/video/7681450201478892807');
  assert.match(plan.description, /Why do hens eat stones\?\n\nhomosapiens · @homosapienstv/);
  const photos = postPlan(toPost({ id: '1', author: { uniqueId: 'a' }, imagePost: { images: [
    { imageURL: { urlList: ['https://p16-sign.tiktokcdn-us.com/a.jpeg'] } }, { imageURL: { urlList: ['https://p16-sign.tiktokcdn-us.com/b.jpeg'] } }] } }));
  assert.deepEqual(photos.items.map(i => [i.video, i.filename]), [[false, 'tiktok-1-1.jpg'], [false, 'tiktok-1-2.jpg']]);
  assert.equal(photos.items[0].source, 'https://www.tiktok.com/@a/photo/1');
});

test('Pinterest: the original image, an MP4 over the HLS playlist, the pinner and date', () => {
  const { toPost } = loadSite('pinterest');
  const pin = { id: '690598924084496178', description: 'simple mountain landscape', created_at: 'Sun, 13 May 2018 08:27:34 +0000', pinner: { username: 'troendle', full_name: 'Troendle Schlatter' },
    images: { '236x': { url: 'https://i.pinimg.com/236x/9f/a.jpg', width: 236 }, orig: { url: 'https://i.pinimg.com/originals/9f/a.jpg', width: 236 } } };
  const plan = postPlan(toPost(pin));
  assert.equal(plan.items[0].url, 'https://i.pinimg.com/originals/9f/a.jpg');
  assert.equal(plan.items[0].createdAt, '2018-05-13T08:27:34.000Z');
  assert.equal(plan.items[0].source, 'https://www.pinterest.com/pin/690598924084496178/');
  assert.match(plan.description, /simple mountain landscape\n\nTroendle Schlatter · @troendle/);
  const video = postPlan(toPost({ ...pin, videos: { video_list: { V_HLSV4: { url: 'https://v1.pinimg.com/videos/x.m3u8', width: 1080 }, V_720P: { url: 'https://v1.pinimg.com/videos/720.mp4', width: 720 } } } }));
  assert.equal(video.items[0].url, 'https://v1.pinimg.com/videos/720.mp4');
  assert.equal(video.items[0].video, true);
});

// Shapes Pinterest sent on 2026-11-07: most videos uploaded lately (TikToks pinned among them) are
// an idea pin's video block, or a pin's videos, with only an HLS stream.
test('Pinterest: a video kept only as a stream is saved as video; MP4s first when there are some', () => {
  const { toPost } = loadSite('pinterest');
  const base = { id: '1266706141792890', created_at: 'Thu, 05 Feb 2026 21:09:24 +0000', pinner: { username: 'smcganty', full_name: 'Roseee' },
    images: { orig: { url: 'https://i.pinimg.com/originals/8c/16/72/a.jpg', width: 720 } } };
  const hls = 'https://v1.pinimg.com/videos/iht/hls/e4/d6/bf/e4d6bf4d83f85500b245d7e504c7c409.m3u8';
  const block = postPlan(toPost({ ...base, story_pin_data: { pages: [{ blocks: [{ block_type: 3, video: { video_list: { V_HLSV3_MOBILE: { url: hls, width: 720, height: 1280,
    thumbnail: 'https://i.pinimg.com/videos/thumbnails/originals/e4/d6/bf/x.0000000.jpg' } } } }] }] } }));
  assert.deepEqual(Array.from(block.items, i => [i.video, i.filename]), [[true, 'pinterest-1266706141792890-1.mp4']]);
  assert.equal(block.items[0].url, 'https://v1.pinimg.com/videos/iht/720p/e4/d6/bf/e4d6bf4d83f85500b245d7e504c7c409.mp4'); // older videos have one
  assert.deepEqual([...block.items[0].fallbacks], [hls]);
  const pin = postPlan(toPost({ ...base, videos: { video_list: { V_HLSV4: { url: hls, width: 576 } } } }));
  assert.equal(pin.items[0].video, true);
  assert.deepEqual([...pin.items[0].fallbacks], [hls]);
  // Tiers that all claim the source's size: the 720p copy, then the larger tiers, then the stream.
  const tiers = Object.fromEntries([1, 2, 3, 4].map(t => [`V_EXP${t + 2}`, { url: `https://v1.pinimg.com/videos/mc/expMp4/22/3f/eb/x_t${t}.mp4`, width: 1080 }]));
  const story = postPlan(toPost({ ...base, story_pin_data: { pages: [{ blocks: [{ type: 'story_pin_video_block', video: { video_list: {
    ...tiers, V_EXP7: { url: 'https://v1.pinimg.com/videos/mc/720p/22/3f/eb/x.mp4', width: 1080 }, V_HLSV3_MOBILE: { url: 'https://v1.pinimg.com/videos/mc/hls/22/3f/eb/x.m3u8', width: 1080 } } } }] }] } }));
  assert.equal(story.items[0].url, 'https://v1.pinimg.com/videos/mc/720p/22/3f/eb/x.mp4');
  assert.deepEqual([...story.items[0].fallbacks], ['https://v1.pinimg.com/videos/mc/expMp4/22/3f/eb/x_t4.mp4', 'https://v1.pinimg.com/videos/mc/expMp4/22/3f/eb/x_t3.mp4',
    'https://v1.pinimg.com/videos/mc/hls/22/3f/eb/x.m3u8']);
  // A video block with nothing to play: its still, rather than nothing.
  const still = postPlan(toPost({ ...base, story_pin_data: { pages: [{ blocks: [{ video: { video_list: { V_X: { thumbnail: 'https://i.pinimg.com/videos/thumbnails/originals/y.jpg' } } } }] }] } }));
  assert.deepEqual(Array.from(still.items, i => [i.video, i.url]), [[false, 'https://i.pinimg.com/videos/thumbnails/originals/y.jpg']]);
});

test('site media may move to another of the site’s servers, never elsewhere; other addresses are tried', async () => {
  const originalFetch = globalThis.fetch;
  const settings = { serverUrl: 'http://photostead.test:4800', apiKey: 'psk_test-only-key' };
  const item = postPlan({ site: 'civitai', id: '9', url: 'https://civitai.com/images/9', media: [{ url: 'https://image.civitai.com/k/u/original=true/u.jpeg' }] }).items[0];
  let redirectTo = 'https://blobs-b2.civitai.com/file/u';
  let sent;
  globalThis.fetch = async (url, init = {}) => {
    if (String(url).endsWith('/api/v1/server')) return Response.json({ id: 'srv-1' });
    if (String(url).startsWith('https://image.civitai.com')) {
      assert.equal(init.credentials, 'omit'); assert.equal(init.redirect, 'follow');
      const r = new Response(new Blob(['jpeg'], { type: 'image/jpeg' })); Object.defineProperty(r, 'url', { value: redirectTo }); return r;
    }
    if (String(url).startsWith('https://v16')) return new Response('nope', { status: 403 });
    if (String(url).startsWith('https://v19')) return new Response(new Blob(['mp4'], { type: 'video/mp4' }));
    sent = JSON.parse(await init.body.get('meta').text());
    return Response.json({ status: 'accepted', sha256: 'abc' }, { status: 202 });
  };
  forgetReachable();
  try {
    await uploadItem(settings, item, {});
    assert.equal(sent.sourceUrl, 'https://civitai.com/images/9');
    redirectTo = 'https://evil.test/u';
    await assert.rejects(uploadItem(settings, item, {}), /evil\.test/);
    const tiktok = postPlan({ site: 'tiktok', id: '1', url: 'https://www.tiktok.com/@a/video/1', media: [{ url: 'https://v16-webapp-prime.us.tiktok.com/x', fallbacks: ['https://v19-webapp-prime.us.tiktok.com/x'], video: true }] }).items[0];
    const checkpoint = await uploadItem(settings, tiktok, {});
    assert.equal(checkpoint.assetId, 'abc');
  } finally { globalThis.fetch = originalFetch; forgetReachable(); }
});

test('a page hands its downloaded files over in pieces, and they arrive and upload unchanged', async () => {
  const { FileReceiver } = await import('../lib/transfer.js');
  // 7 MB of varied bytes: three pieces of the page's 3 MB.
  const video = new Uint8Array(7 * 1024 * 1024 + 123).map((_, i) => (i * 31 + (i >> 9)) & 255);
  const sent = [];
  let answer;
  const port = {
    postMessage: m => { sent.push(structuredClone(m)); if (m.type === 'end') setTimeout(() => answer({ photostead: { ok: true, assetCount: 1 } })); },
    onMessage: { addListener: fn => { answer = fn; } },
    onDisconnect: { addListener: () => {} },
    disconnect: () => {},
  };
  const window = {};
  const context = vm.createContext({ window, chrome: { runtime: { connect: () => port } }, btoa, Uint8Array, Error, Promise, setTimeout,
    fetch: async url => { assert.equal(url, 'https://v16-webapp-prime.us.tiktok.com/v'); return new Response(video, { headers: { 'content-type': 'video/mp4' } }); } });
  vm.runInContext(readFileSync(new URL('../content/sites/common.js', import.meta.url), 'utf8'), context);
  const post = { site: 'tiktok', id: '1', url: 'https://www.tiktok.com/@a/video/1', media: [{ url: 'https://v16-webapp-prime.us.tiktok.com/v', video: true }] };
  const result = await window.PhotosteadSite.saveWithFiles(post);
  assert.equal(result.photostead.ok, true);
  assert.deepEqual(sent.map(m => m.type), ['post', 'file', 'chunk', 'chunk', 'chunk', 'end']);

  const receiver = new FileReceiver();
  for (const m of sent) assert.ok(receiver.add(m));
  const plan = postPlan(sent[0].post);
  const file = receiver.files(plan.items).get(0);
  assert.equal(file.type, 'video/mp4');
  assert.deepEqual(new Uint8Array(await file.arrayBuffer()), video);

  // The upload sends those bytes without downloading anything.
  const originalFetch = globalThis.fetch;
  let uploaded;
  globalThis.fetch = async (url, init = {}) => {
    if (String(url).endsWith('/api/v1/server')) return Response.json({ id: 'srv-1' });
    assert.ok(String(url).startsWith('http://photostead.test:4800/api/v1/uploads'), `no download: ${url}`);
    uploaded = new Uint8Array(await init.body.get('file').arrayBuffer());
    return Response.json({ status: 'accepted', sha256: 'abc' }, { status: 202 });
  };
  forgetReachable();
  try {
    await uploadItem({ serverUrl: 'http://photostead.test:4800', apiKey: 'psk_test-only-key' }, plan.items[0], {}, async () => {}, file);
    assert.deepEqual(uploaded, video);
  } finally { globalThis.fetch = originalFetch; forgetReachable(); }
});

test('on TikTok, what the page can’t fetch (photo posts’ pictures) is left to the service worker', async () => {
  const { FileReceiver } = await import('../lib/transfer.js');
  const sent = [];
  let answer;
  const port = {
    postMessage: m => { sent.push(structuredClone(m)); if (m.type === 'end') setTimeout(() => answer({ photostead: { ok: true, assetCount: 2 } })); },
    onMessage: { addListener: fn => { answer = fn; } }, onDisconnect: { addListener: () => {} }, disconnect: () => {},
  };
  const window = {};
  const context = vm.createContext({ window, chrome: { runtime: { connect: () => port } }, btoa, Uint8Array, Error, Promise, setTimeout,
    fetch: async () => { throw new TypeError('Failed to fetch'); } }); // what a page gets from TikTok's picture servers
  vm.runInContext(readFileSync(new URL('../content/sites/common.js', import.meta.url), 'utf8'), context);
  const post = { site: 'tiktok', id: '7636153929700576543', url: 'https://www.tiktok.com/@drillyty/photo/7636153929700576543', media: [
    { url: 'https://p16-common-sign.tiktokcdn-us.com/a~tplv-photomode-image.jpeg?x=1', video: false },
    { url: 'https://p16-common-sign.tiktokcdn-us.com/b~tplv-photomode-image.jpeg?x=1', video: false },
  ] };
  assert.equal((await window.PhotosteadSite.saveWithFiles(post)).photostead.ok, true);
  assert.deepEqual(sent.map(m => m.type), ['post', 'end']);
  const receiver = new FileReceiver();
  for (const m of sent) receiver.add(m);
  const plan = postPlan(post);
  assert.equal(receiver.files(plan.items).size, 0); // so uploadItem downloads each itself
  assert.deepEqual(plan.items.map(i => i.filename), ['tiktok-7636153929700576543-1.jpg', 'tiktok-7636153929700576543-2.jpg']);
});

test('Pinterest: an idea pin page whose named original 403s falls back to the original’s real type, and is named by it', async () => {
  const { toPost } = loadSite('pinterest');
  const h = '36/a9/4c/36a94c127b659cf330c8941ff9783dbc';
  const pin = { id: '1027242996268606699', created_at: 'Mon, 03 Nov 2026 10:00:00 +0000', pinner: { username: 'p' },
    images: { orig: { url: `https://i.pinimg.com/originals/${h}.webp`, width: 1080 } },
    story_pin_data: { pages: [{ blocks: [{ image: { images: {
      originals: { url: `https://i.pinimg.com/originals/${h}.jpg`, width: 1080 },
      '736x': { url: `https://i.pinimg.com/736x/${h}.jpg`, width: 736 }, '474x': { url: `https://i.pinimg.com/474x/${h}.jpg`, width: 474 } } } }] }] } };
  const item = postPlan(toPost(pin)).items[0];
  assert.equal(item.url, `https://i.pinimg.com/originals/${h}.jpg`);
  assert.deepEqual([...item.fallbacks], [`https://i.pinimg.com/originals/${h}.webp`, `https://i.pinimg.com/originals/${h}.png`, `https://i.pinimg.com/originals/${h}.gif`, `https://i.pinimg.com/736x/${h}.jpg`]);

  const originalFetch = globalThis.fetch;
  let sentName;
  globalThis.fetch = async (url, init = {}) => {
    url = String(url);
    if (url.endsWith('/api/v1/server')) return Response.json({ id: 'srv-1' });
    if (url.includes('pinimg.com')) { // what Pinterest answers for that pin
      return url.endsWith('.webp') ? new Response(new Blob(['RIFF....WEBP'], { type: 'image/webp' })) : new Response('<Error>AccessDenied</Error>', { status: 403, headers: { 'content-type': 'application/xml' } });
    }
    sentName = init.body.get('file').name;
    assert.equal(JSON.parse(await init.body.get('meta').text()).fileName, sentName);
    return Response.json({ status: 'accepted', sha256: 'abc' }, { status: 202 });
  };
  forgetReachable();
  try {
    await uploadItem({ serverUrl: 'http://photostead.test:4800', apiKey: 'psk_test-only-key' }, item, {});
    assert.equal(sentName, 'pinterest-1027242996268606699-1.webp');
  } finally { globalThis.fetch = originalFetch; forgetReachable(); }
});
