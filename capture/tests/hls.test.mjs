import test from 'node:test';
import assert from 'node:assert/strict';
import { pickRendition, mediaParts, joinFragmented } from '../lib/hls.js';
import { postPlan } from '../lib/sites.js';
import { uploadItem, forgetReachable } from '../lib/photostead.js';

// Playlists in the shape Pinterest sent on 2026-11-07: a master with four sizes and one sound, and
// media playlists that are byte ranges of one CMAF file each.
const BASE = 'https://v1.pinimg.com/videos/iht/hls/e4/d6/bf/';
const MASTER = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-INDEPENDENT-SEGMENTS

#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio1",NAME="1",AUTOSELECT=YES,URI="v_audio.m3u8",CHANNELS="2"
#EXT-X-STREAM-INF:BANDWIDTH=370624,AVERAGE-BANDWIDTH=317196,CODECS="avc1.64080D,mp4a.40.29",RESOLUTION=234x416,FRAME-RATE=30,AUDIO="audio1"
v_240w.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=1448920,AVERAGE-BANDWIDTH=1229322,CODECS="avc1.64081F,mp4a.40.29",RESOLUTION=720x1280,FRAME-RATE=30,AUDIO="audio1"
v_720w.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=911352,AVERAGE-BANDWIDTH=766967,CODECS="avc1.64081F,mp4a.40.29",RESOLUTION=540x960,FRAME-RATE=30,AUDIO="audio1"
v_540w.m3u8
`;
const media = (file, init, ranges) => ['#EXTM3U', '#EXT-X-TARGETDURATION:2', '#EXT-X-VERSION:6', '#EXT-X-PLAYLIST-TYPE:VOD',
  `#EXT-X-MAP:URI="${file}",BYTERANGE="${init}@0"`, ...ranges.flatMap(([length, at]) => ['#EXTINF:2', `#EXT-X-BYTERANGE:${length}${at === undefined ? '' : `@${at}`}`, file]),
  '#EXT-X-ENDLIST'].join('\n');

test('the stream’s largest picture and its sound are picked; ranges follow on in one file', () => {
  assert.deepEqual(pickRendition(MASTER, `${BASE}v.m3u8`), { video: `${BASE}v_720w.m3u8`, audio: `${BASE}v_audio.m3u8` });
  const parts = mediaParts(media('v_720w.cmfv', 989, [[100, 1161], [50]]), `${BASE}v_720w.m3u8`);
  assert.deepEqual(parts.init, { url: `${BASE}v_720w.cmfv`, start: 0, end: 989 });
  assert.deepEqual(parts.segments.map(s => [s.start, s.end]), [[1161, 1261], [1261, 1311]]);
  assert.throws(() => mediaParts('#EXTM3U\n#EXTINF:2\nseg.ts\n', BASE), /format/); // MPEG-TS
  assert.throws(() => mediaParts('#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI="k"\n#EXT-X-MAP:URI="i.mp4"\n#EXTINF:2\ns.m4s\n', BASE), /encrypted/);
});

// ---- A small fragmented MP4 maker, for tracks whose samples are known bytes ----
const cat = (...parts) => { const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0)); let at = 0; for (const p of parts) { out.set(p, at); at += p.length; } return out; };
const u32 = (...v) => { const b = new Uint8Array(v.length * 4); const d = new DataView(b.buffer); v.forEach((x, i) => d.setInt32(i * 4, x)); return b; };
const box = (type, ...parts) => { const body = cat(...parts); const b = cat(u32(8 + body.length), new TextEncoder().encode(type), body); return b; };
const full = (type, version, flags, ...parts) => box(type, u32((version << 24) | flags), ...parts);

function fragmented({ handler, timescale, samples, perFragment, elst }) {
  const stbl = box('stbl', full('stsd', 0, 0, u32(1), box(handler === 'vide' ? 'avc1' : 'mp4a', new Uint8Array(8))),
    full('stts', 0, 0, u32(0)), full('stsc', 0, 0, u32(0)), full('stsz', 0, 0, u32(0, 0)), full('stco', 0, 0, u32(0)));
  const trak = box('trak', full('tkhd', 0, 3, u32(0, 0, 1, 0, 0), new Uint8Array(60)),
    ...(elst ? [box('edts', full('elst', 0, 0, u32(1, 0, elst, 0x10000)))] : []),
    box('mdia', full('mdhd', 0, 0, u32(0, 0, timescale, 0, 0)), full('hdlr', 0, 0, u32(0), new TextEncoder().encode(handler), new Uint8Array(13)),
      box('minf', handler === 'vide' ? full('vmhd', 0, 1, new Uint8Array(8)) : full('smhd', 0, 0, new Uint8Array(4)), stbl)));
  const init = cat(box('ftyp', new TextEncoder().encode('iso6'), u32(0)), box('moov', full('mvhd', 0, 0, new Uint8Array(96)), trak,
    box('mvex', full('trex', 0, 0, u32(1, 1, 0, 0, 0)))));
  const fragments = [];
  let time = 0;
  for (let at = 0; at < samples.length; at += perFragment) {
    const group = samples.slice(at, at + perFragment);
    const trunFor = (dataOffset) => full('trun', 1, 0xf01, u32(group.length, dataOffset),
      ...group.map(s => u32(s.duration, s.data.length, s.sync ? 0 : 0x10000, s.cto || 0)));
    const traf = (dataOffset) => box('traf', full('tfhd', 0, 0x20000, u32(1)), full('tfdt', 1, 0, u32(0, time)), trunFor(dataOffset));
    const moofSize = box('moof', full('mfhd', 0, 0, u32(fragments.length + 1)), traf(0)).length;
    fragments.push(box('moof', full('mfhd', 0, 0, u32(fragments.length + 1)), traf(moofSize + 8)), box('mdat', ...group.map(s => s.data)));
    time += group.reduce((n, s) => n + s.duration, 0);
  }
  return cat(init, ...fragments);
}

// Reads back what a plain MP4 says: per track, its tables and each sample's bytes.
function readPlain(bytes) {
  const d = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const kids = (s, e) => { const out = []; for (let at = s; at < e;) { let size = d.getUint32(at), h = 8; if (size === 1) { size = Number(d.getBigUint64(at + 8)); h = 16; } out.push({ type: String.fromCharCode(...bytes.subarray(at + 4, at + 8)), s: at + h, e: at + size }); at += size; } return out; };
  const find = (list, type) => list.find(b => b.type === type);
  const ints = (b, skip) => { const out = []; for (let at = b.s + skip; at < b.e; at += 4) out.push(d.getUint32(at)); return out; };
  const top = kids(0, bytes.length);
  const tracks = kids(find(top, 'moov').s, find(top, 'moov').e).filter(b => b.type === 'trak').map(trak => {
    const t = kids(trak.s, trak.e);
    const stbl = kids(find(kids(find(kids(find(t, 'mdia').s, find(t, 'mdia').e), 'minf').s, find(kids(find(t, 'mdia').s, find(t, 'mdia').e), 'minf').e), 'stbl').s,
      find(kids(find(kids(find(t, 'mdia').s, find(t, 'mdia').e), 'minf').s, find(kids(find(t, 'mdia').s, find(t, 'mdia').e), 'minf').e), 'stbl').e);
    const table = type => find(stbl, type) && ints(find(stbl, type), 4);
    const sizes = table('stsz').slice(2), offsets = table('stco').slice(1), stsc = table('stsc').slice(1);
    const perChunk = offsets.map((_, i) => { let n = 0; for (let j = 0; j < stsc.length; j += 3) if (stsc[j] <= i + 1) n = stsc[j + 1]; return n; });
    const data = []; let k = 0;
    offsets.forEach((o, i) => { for (let j = 0; j < perChunk[i]; j++) { data.push(Array.from(bytes.subarray(o, o + sizes[k]))); o += sizes[k++]; } });
    const edts = find(t, 'edts');
    return { stts: table('stts'), ctts: table('ctts'), stss: table('stss'), data, elst: edts && ints(find(kids(edts.s, edts.e), 'elst'), 4) };
  });
  return { order: top.map(b => b.type), tracks };
}

const sample = (n, duration, extra = {}) => ({ duration, data: new Uint8Array([n, n, n]), ...extra });
// A picture with B-frames written the CMAF way (negative composition offsets), and its sound.
const picture = [sample(1, 512, { sync: true, cto: 0 }), sample(2, 512, { cto: 1024 }), sample(3, 512, { cto: -512 }), sample(4, 512, { cto: -512 }),
  sample(5, 512, { sync: true, cto: 0 }), sample(6, 512, { cto: 0 })];
const sound = Array.from({ length: 5 }, (_, i) => sample(100 + i, 1024, { sync: true }));

test('a picture and its sound become one plain MP4: every sample once, in time, index first', () => {
  const out = readPlain(joinFragmented([
    fragmented({ handler: 'vide', timescale: 15360, samples: picture, perFragment: 4 }),
    fragmented({ handler: 'soun', timescale: 44100, samples: sound, perFragment: 2, elst: 1024 }),
  ]));
  assert.deepEqual(out.order, ['ftyp', 'moov', 'mdat']);
  const [v, a] = out.tracks;
  assert.deepEqual(v.data, picture.map(s => Array.from(s.data)));
  assert.deepEqual(a.data, sound.map(s => Array.from(s.data)));
  assert.deepEqual(v.stts, [1, 6, 512]);
  assert.deepEqual(v.stss, [2, 1, 5]); // the two keyframes
  // Offsets made positive (+512), and the edit starts the picture at its first frame on screen.
  assert.deepEqual(v.ctts, [4, 1, 512, 1, 1536, 2, 0, 2, 512]);
  assert.equal(v.elst[2], 512);
  assert.equal(a.stss, undefined); // all sound samples are keyframes
  assert.equal(a.elst[2], 1024); // the sound's own edit (its priming) kept
  assert.equal(a.ctts, undefined);
});

test('a Pinterest video kept only as a stream is joined, from Pinterest’s servers only', async () => {
  const originalFetch = globalThis.fetch;
  const settings = { serverUrl: 'http://photostead.test:4800', apiKey: 'psk_test-only-key' };
  const v = fragmented({ handler: 'vide', timescale: 15360, samples: picture, perFragment: 3 });
  const a = fragmented({ handler: 'soun', timescale: 44100, samples: sound, perFragment: 5 });
  const initEnd = f => { const d = new DataView(f.buffer); let at = 0; while (String.fromCharCode(...f.subarray(at + 4, at + 8)) !== 'moof') at += d.getUint32(at); return at; };
  const ranges = f => { const d = new DataView(f.buffer); const out = []; for (let at = initEnd(f); at < f.length;) { const moof = d.getUint32(at), mdat = d.getUint32(at + moof); out.push([moof + mdat, at]); at += moof + mdat; } return out; };
  let files = {
    'v.m3u8': MASTER,
    'v_720w.m3u8': media('v_720w.cmfv', initEnd(v), ranges(v)),
    'v_audio.m3u8': media('v_audio.cmfa', initEnd(a), ranges(a)),
    'v_720w.cmfv': v, 'v_audio.cmfa': a,
  };
  let uploaded, asked = [];
  globalThis.fetch = async (url, init = {}) => {
    url = String(url);
    if (url.endsWith('/api/v1/server')) return Response.json({ id: 'srv-1' });
    if (url.startsWith(BASE)) {
      asked.push(url.slice(BASE.length));
      assert.equal(init.credentials, 'omit');
      const body = files[url.slice(BASE.length)];
      return body === undefined ? new Response('no', { status: 404 }) : new Response(body);
    }
    if (url.includes('/720p/')) return new Response('<Error>AccessDenied</Error>', { status: 403 });
    uploaded = await init.body.get('file').arrayBuffer();
    return Response.json({ status: 'accepted', sha256: 'abc' }, { status: 202 });
  };
  forgetReachable();
  try {
    const item = postPlan({ site: 'pinterest', id: '1', url: 'https://www.pinterest.com/pin/1/', media: [{
      url: 'https://v1.pinimg.com/videos/iht/720p/e4/d6/bf/v.mp4', fallbacks: [`${BASE}v.m3u8`], video: true, ext: 'mp4', key: '1' }] }).items[0];
    assert.equal(item.filename, 'pinterest-1-1.mp4');
    await uploadItem(settings, item, {});
    assert.deepEqual(asked, ['v.m3u8', 'v_720w.m3u8', 'v_720w.cmfv', 'v_audio.m3u8', 'v_audio.cmfa']); // each file downloaded once
    const out = readPlain(new Uint8Array(uploaded));
    assert.deepEqual(out.tracks[0].data, picture.map(s => Array.from(s.data)));
    assert.deepEqual(out.tracks[1].data, sound.map(s => Array.from(s.data)));
    // A playlist that points anywhere but Pinterest's servers isn't followed.
    files = { ...files, 'v.m3u8': MASTER.replace('v_720w.m3u8', 'https://evil.test/v_720w.m3u8') };
    await assert.rejects(uploadItem(settings, item, {}), /evil\.test/);
  } finally { globalThis.fetch = originalFetch; forgetReachable(); }
});
