// Videos a site offers only as an HLS stream (Pinterest's newer ones): the stream's best picture
// and its sound, each a fragmented MP4 (CMAF), joined into one ordinary MP4 with its index at the
// front, so it plays anywhere (Safari and the iPhone app included) without being converted.
//
// hlsToMp4(masterUrl, fetchBytes): fetchBytes(url, range?) → Uint8Array checks and downloads one
// address (the caller keeps it to the site's own servers). Streams in MPEG-TS (no EXT-X-MAP) or
// encrypted ones aren't handled: an error says so and the caller tries its next address.

const MAX_SEGMENTS = 20000;

// ---- Playlists ----

function attributes(line) {
  const out = {};
  for (const m of line.slice(line.indexOf(':') + 1).matchAll(/([A-Z0-9-]+)=("[^"]*"|[^,]*)/g)) out[m[1]] = m[2].replace(/^"|"$/g, '');
  return out;
}

function byteRange(value, next) {
  const [length, offset] = String(value).split('@');
  const start = offset === undefined ? next : Number(offset);
  return { start, end: start + Number(length) };
}

// The master playlist → the best rendition (most pixels, then bandwidth) and its audio, as addresses.
export function pickRendition(text, base) {
  const lines = text.split(/\r?\n/).map(l => l.trim());
  if (!lines.some(l => l.startsWith('#EXT-X-STREAM-INF'))) return { video: base, audio: '' }; // a media playlist itself
  const variants = [], audio = [];
  lines.forEach((line, i) => {
    if (line.startsWith('#EXT-X-STREAM-INF')) {
      const a = attributes(line);
      const uri = lines.slice(i + 1).find(l => l && !l.startsWith('#'));
      const [w, h] = (a.RESOLUTION || '0x0').split('x').map(Number);
      if (uri) variants.push({ uri: new URL(uri, base).href, pixels: w * h, bandwidth: Number(a.BANDWIDTH) || 0, audio: a.AUDIO || '' });
    } else if (line.startsWith('#EXT-X-MEDIA') && /TYPE=AUDIO/.test(line)) {
      const a = attributes(line);
      if (a.URI) audio.push({ group: a['GROUP-ID'], uri: new URL(a.URI, base).href, preferred: a.DEFAULT === 'YES' });
    }
  });
  if (!variants.length) throw new Error('The video’s stream lists no versions of it.');
  variants.sort((a, b) => b.pixels - a.pixels || b.bandwidth - a.bandwidth);
  const best = variants[0];
  const sound = audio.filter(a => a.group === best.audio).sort((a, b) => b.preferred - a.preferred)[0];
  return { video: best.uri, audio: sound?.uri || '' };
}

// A media playlist → its init section and segments, each { url, start?, end? } (no range: all of it).
export function mediaParts(text, base) {
  const lines = text.split(/\r?\n/).map(l => l.trim());
  if (lines.some(l => l.startsWith('#EXT-X-KEY') && !/METHOD=NONE/.test(l))) throw new Error('The video’s stream is encrypted.');
  let init = null, range = null;
  const segments = [];
  const next = new Map(); // where an unstated range starts: after the last one in the same file
  for (const line of lines) {
    if (line.startsWith('#EXT-X-MAP')) {
      const a = attributes(line);
      const url = new URL(a.URI, base).href;
      init = { url };
      if (a.BYTERANGE) Object.assign(init, byteRange(a.BYTERANGE, 0));
    } else if (line.startsWith('#EXT-X-BYTERANGE')) {
      range = line.slice(line.indexOf(':') + 1);
    } else if (line && !line.startsWith('#')) {
      const url = new URL(line, base).href;
      const part = { url };
      if (range !== null) {
        Object.assign(part, byteRange(range, next.get(url) || 0));
        next.set(url, part.end);
      }
      range = null;
      segments.push(part);
      if (segments.length > MAX_SEGMENTS) throw new Error('The video’s stream is too long.');
    }
  }
  if (!init) throw new Error('The video’s stream isn’t in a format Photostead Capture can save.');
  if (!segments.length) throw new Error('The video’s stream has no parts.');
  return { init, segments };
}

// The track's whole fragmented MP4: its init section, then every segment, in order. Parts that are
// ranges of one file (CMAF) are read from that file, downloaded once.
async function trackBytes(playlistUrl, fetchBytes) {
  const { init, segments } = mediaParts(new TextDecoder().decode(await fetchBytes(playlistUrl)), playlistUrl);
  const files = new Map();
  const whole = url => { if (!files.has(url)) files.set(url, fetchBytes(url)); return files.get(url); };
  const pieces = [];
  for (const part of [init, ...segments]) {
    const bytes = await whole(part.url);
    const piece = part.start === undefined ? bytes : bytes.subarray(part.start, part.end);
    if (part.end !== undefined && piece.length !== part.end - part.start) throw new Error('The video’s stream was cut short.');
    pieces.push(piece);
  }
  return concat(pieces);
}

export async function hlsToMp4(masterUrl, fetchBytes) {
  const { video, audio } = pickRendition(new TextDecoder().decode(await fetchBytes(masterUrl)), masterUrl);
  const tracks = [await trackBytes(video, fetchBytes)];
  if (audio) tracks.push(await trackBytes(audio, fetchBytes));
  return joinFragmented(tracks);
}

// ---- MP4 boxes ----

function concat(parts) {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) { out.set(p, at); at += p.length; }
  return out;
}

const view = b => new DataView(b.buffer, b.byteOffset, b.byteLength);
const fourcc = (b, at) => String.fromCharCode(b[at], b[at + 1], b[at + 2], b[at + 3]);

// The boxes directly inside bytes[start, end): { type, start, end, body } (body: after the header).
function boxes(bytes, start = 0, end = bytes.length) {
  const dv = view(bytes), out = [];
  for (let at = start; at + 8 <= end;) {
    let size = dv.getUint32(at), header = 8;
    const type = fourcc(bytes, at + 4);
    if (size === 1) { size = Number(dv.getBigUint64(at + 8)); header = 16; } else if (size === 0) size = end - at;
    if (size < header || at + size > end) throw new Error(`The video has a broken ${type} box.`);
    out.push({ type, start: at, end: at + size, body: at + header });
    at += size;
  }
  return out;
}
const child = (bytes, box, type) => boxes(bytes, box.body, box.end).find(b => b.type === type);
const path = (bytes, box, ...types) => types.reduce((b, t) => b && child(bytes, b, t), box);
const raw = (bytes, box) => bytes.slice(box.start, box.end);

function box(type, ...parts) {
  const body = concat(parts);
  const out = new Uint8Array(8 + body.length);
  view(out).setUint32(0, out.length);
  for (let i = 0; i < 4; i++) out[4 + i] = type.charCodeAt(i);
  out.set(body, 8);
  return out;
}
function u32s(...values) {
  const out = new Uint8Array(values.length * 4), dv = view(out);
  values.forEach((v, i) => dv.setUint32(i * 4, v >>> 0));
  return out;
}
const fullBox = (type, version, flags, ...parts) => box(type, u32s(((version & 0xff) << 24) | (flags & 0xffffff)), ...parts);

// One track's fragmented MP4 → what's needed to write it out plainly: its trak parts, timescale,
// and samples { offset, size, duration, cto, sync } with their bytes in `bytes`.
function readTrack(bytes) {
  const top = boxes(bytes);
  const moov = top.find(b => b.type === 'moov');
  if (!moov) throw new Error('The video’s stream has no header.');
  const traks = boxes(bytes, moov.body, moov.end).filter(b => b.type === 'trak');
  // Picture and sound in one file: not the separate tracks this joins (it would lose the sound).
  if (traks.length !== 1) throw new Error('The video’s stream isn’t in a format Photostead Capture can save.');
  const trak = traks[0];
  const mdia = path(bytes, trak, 'mdia');
  const mdhd = child(bytes, mdia, 'mdhd');
  const hdlr = child(bytes, mdia, 'hdlr');
  const minf = child(bytes, mdia, 'minf');
  const stbl = child(bytes, minf, 'stbl');
  const stsd = child(bytes, stbl, 'stsd');
  if (!trak || !mdhd || !hdlr || !minf || !stsd) throw new Error('The video’s stream has an incomplete header.');
  const dv = view(bytes);
  const mdhdVersion = bytes[mdhd.body];
  const timescale = dv.getUint32(mdhd.body + (mdhdVersion === 1 ? 20 : 12));
  const trackId = dv.getUint32(child(bytes, trak, 'tkhd').body + (bytes[child(bytes, trak, 'tkhd').body] === 1 ? 20 : 12));
  const trex = boxes(bytes, moov.body, moov.end).filter(b => b.type === 'mvex').flatMap(m => boxes(bytes, m.body, m.end))
    .filter(b => b.type === 'trex').find(b => dv.getUint32(b.body + 4) === trackId);
  const defaults = trex ? { duration: dv.getUint32(trex.body + 12), size: dv.getUint32(trex.body + 16), flags: dv.getUint32(trex.body + 20) }
    : { duration: 0, size: 0, flags: 0 };
  const elst = path(bytes, trak, 'edts', 'elst');

  const chunks = []; // one per run of samples: { time, samples: [...] }
  let time = 0;
  for (const moof of top.filter(b => b.type === 'moof')) {
    for (const traf of boxes(bytes, moof.body, moof.end).filter(b => b.type === 'traf')) {
      const tfhd = child(bytes, traf, 'tfhd');
      const tf = dv.getUint32(tfhd.body) & 0xffffff;
      if (dv.getUint32(tfhd.body + 4) !== trackId) continue;
      let at = tfhd.body + 8;
      let base = moof.start;
      if (tf & 0x1) { base = Number(dv.getBigUint64(at)); at += 8; }
      if (tf & 0x2) at += 4;
      const dflt = { ...defaults };
      if (tf & 0x8) { dflt.duration = dv.getUint32(at); at += 4; }
      if (tf & 0x10) { dflt.size = dv.getUint32(at); at += 4; }
      if (tf & 0x20) { dflt.flags = dv.getUint32(at); at += 4; }
      const tfdt = child(bytes, traf, 'tfdt');
      if (tfdt) time = bytes[tfdt.body] === 1 ? Number(dv.getBigUint64(tfdt.body + 4)) : dv.getUint32(tfdt.body + 4);
      let dataAt = base;
      for (const trun of boxes(bytes, traf.body, traf.end).filter(b => b.type === 'trun')) {
        const version = bytes[trun.body];
        const rf = dv.getUint32(trun.body) & 0xffffff;
        const count = dv.getUint32(trun.body + 4);
        let p = trun.body + 8;
        if (rf & 0x1) { dataAt = base + dv.getInt32(p); p += 4; }
        let firstFlags = null;
        if (rf & 0x4) { firstFlags = dv.getUint32(p); p += 4; }
        const chunk = { time, samples: [] };
        for (let i = 0; i < count; i++) {
          const duration = rf & 0x100 ? dv.getUint32((p += 4) - 4) : dflt.duration;
          const size = rf & 0x200 ? dv.getUint32((p += 4) - 4) : dflt.size;
          let flags = rf & 0x400 ? dv.getUint32((p += 4) - 4) : dflt.flags;
          if (i === 0 && firstFlags !== null) flags = firstFlags;
          const cto = rf & 0x800 ? (version === 1 ? dv.getInt32((p += 4) - 4) : dv.getUint32((p += 4) - 4)) : 0;
          if (dataAt + size > bytes.length) throw new Error('The video’s stream was cut short.');
          chunk.samples.push({ offset: dataAt, size, duration, cto, sync: !(flags & 0x10000) });
          dataAt += size;
          time += duration;
        }
        if (chunk.samples.length) chunks.push(chunk);
      }
    }
  }
  if (!chunks.length) throw new Error('The video’s stream has no frames.');
  return { bytes, trak, mdia, mdhd, hdlr, minf, stsd, elst, timescale, chunks,
    handler: fourcc(bytes, hdlr.body + 8) };
}

// Sample tables for one track; chunkOffsets are where each chunk lands in the file.
function sampleTable(t, chunkOffsets, shift) {
  const samples = t.chunks.flatMap(c => c.samples);
  const runs = (values) => values.reduce((out, v) => {
    const last = out[out.length - 1];
    if (last && last[1] === v) last[0]++; else out.push([1, v]);
    return out;
  }, []);
  const stts = runs(samples.map(s => s.duration));
  const parts = [raw(t.bytes, t.stsd),
    fullBox('stts', 0, 0, u32s(stts.length, ...stts.flat()))];
  if (samples.some(s => s.cto + shift !== 0)) {
    const ctts = runs(samples.map(s => s.cto + shift));
    parts.push(fullBox('ctts', 0, 0, u32s(ctts.length, ...ctts.flat())));
  }
  if (samples.some(s => !s.sync)) {
    const sync = samples.flatMap((s, i) => s.sync ? [i + 1] : []);
    parts.push(fullBox('stss', 0, 0, u32s(sync.length, ...sync)));
  }
  const stsc = [];
  t.chunks.forEach((c, i) => { if (!stsc.length || stsc[stsc.length - 1][1] !== c.samples.length) stsc.push([i + 1, c.samples.length, 1]); });
  parts.push(fullBox('stsc', 0, 0, u32s(stsc.length, ...stsc.flat())));
  parts.push(fullBox('stsz', 0, 0, u32s(0, samples.length, ...samples.map(s => s.size))));
  if (chunkOffsets.some(o => o > 0xffffffff)) {
    const co = new Uint8Array(4 + 4 + chunkOffsets.length * 8), dv = view(co);
    dv.setUint32(4, chunkOffsets.length);
    chunkOffsets.forEach((o, i) => dv.setBigUint64(8 + i * 8, BigInt(o)));
    parts.push(box('co64', co));
  } else {
    parts.push(fullBox('stco', 0, 0, u32s(chunkOffsets.length, ...chunkOffsets)));
  }
  return box('stbl', ...parts);
}

// A copy of a full box with one duration field set (mdhd: at 16/24 after the version; tkhd: 20/28).
function withDuration(bytes, b, v0At, v1At, duration) {
  const out = raw(bytes, b), dv = view(out);
  const headerSize = b.body - b.start;
  if (out[headerSize] === 1) dv.setBigUint64(headerSize + v1At, BigInt(duration));
  else dv.setUint32(headerSize + v0At, Math.min(duration, 0xffffffff));
  return out;
}
function withTrackId(tkhd, id) {
  const dv = view(tkhd);
  dv.setUint32(8 + (tkhd[8] === 1 ? 20 : 12), id);
  tkhd[11] |= 0x3; // enabled, in the movie
  return tkhd;
}

const MOVIE_TIMESCALE = 1000;

// Single-track fragmented MP4s (a stream's picture, its sound) → one plain MP4, the moov first and
// the tracks' samples interleaved in time.
export function joinFragmented(files) {
  const tracks = files.map(readTrack);
  // Negative composition offsets (B-frames written the CMAF way) become positive, with an edit
  // list that starts the picture where it should.
  const shifts = tracks.map(t => Math.max(0, -Math.min(...t.chunks.flatMap(c => c.samples.map(s => s.cto)))));
  // What each track shows, in its own time from its first sample: from its edit list's start, else
  // from its first frame on screen (a picture whose frames start a moment in starts at once), to
  // its last frame's end. The edit list written says just that.
  const edits = tracks.map((t, i) => {
    const first = t.chunks[0].time;
    let at = 0, earliest = Infinity, latest = 0;
    for (const s of t.chunks.flatMap(c => c.samples)) {
      const shown = at + s.cto + shifts[i];
      earliest = Math.min(earliest, shown);
      latest = Math.max(latest, shown + s.duration);
      at += s.duration;
    }
    const start = t.elst ? Math.max(0, editStart(t.bytes, t.elst) - first + shifts[i]) : earliest;
    return { start, length: Math.max(0, latest - start) };
  });
  const order = tracks.flatMap((t, ti) => t.chunks.map((c, ci) => ({ ti, ci, at: c.time / t.timescale })))
    .sort((a, b) => a.at - b.at || a.ti - b.ti);
  const mediaDuration = tracks.map(t => t.chunks.reduce((n, c) => n + c.samples.reduce((m, s) => m + s.duration, 0), 0));
  const movieDuration = tracks.map((t, i) => Math.round(edits[i].length * MOVIE_TIMESCALE / t.timescale));

  const ftyp = box('ftyp', new TextEncoder().encode('isom'), u32s(0x200), new TextEncoder().encode('isomiso2avc1mp41'));
  const moovFor = (offsets) => {
    const mvhd = fullBox('mvhd', 0, 0, u32s(0, 0, MOVIE_TIMESCALE, Math.max(...movieDuration), 0x00010000),
      new Uint8Array([0x01, 0x00, 0, 0]), new Uint8Array(8),
      u32s(0x00010000, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000), new Uint8Array(24), u32s(tracks.length + 1));
    const traks = tracks.map((t, i) => {
      const { bytes } = t;
      const tkhd = withTrackId(withDuration(bytes, child(bytes, t.trak, 'tkhd'), 20, 28, movieDuration[i]), i + 1);
      const mdhd = withDuration(bytes, t.mdhd, 16, 24, mediaDuration[i]);
      const minfParts = boxes(bytes, t.minf.body, t.minf.end).filter(b => b.type !== 'stbl').map(b => raw(bytes, b));
      const minf = box('minf', ...minfParts, sampleTable(t, offsets[i], shifts[i]));
      const mdia = box('mdia', mdhd, raw(bytes, t.hdlr), minf);
      let edts = new Uint8Array(0);
      if (edits[i].start || edits[i].length !== mediaDuration[i]) {
        const shown = Math.round(edits[i].length * MOVIE_TIMESCALE / t.timescale);
        edts = box('edts', fullBox('elst', 0, 0, u32s(1, shown, edits[i].start, 0x00010000)));
      }
      return box('trak', tkhd, edts, mdia);
    });
    return box('moov', mvhd, ...traks);
  };

  // Where each chunk goes: the moov's size doesn't depend on the offsets' values (only on whether
  // any needs 64 bits), so lay out with zeros, then again with the real ones.
  const layout = (moovSize) => {
    let at = ftyp.length + moovSize + 16; // a 16-byte mdat header (64-bit size), always
    const offsets = tracks.map(t => new Array(t.chunks.length));
    for (const { ti, ci } of order) {
      offsets[ti][ci] = at;
      at += tracks[ti].chunks[ci].samples.reduce((n, s) => n + s.size, 0);
    }
    return { offsets, end: at };
  };
  let moov = moovFor(tracks.map(t => t.chunks.map(() => 0)));
  let placed = layout(moov.length);
  moov = moovFor(placed.offsets);
  if (layout(moov.length).end !== placed.end) { placed = layout(moov.length); moov = moovFor(placed.offsets); } // went to co64

  const out = new Uint8Array(placed.end);
  out.set(ftyp, 0);
  out.set(moov, ftyp.length);
  const mdatAt = ftyp.length + moov.length, dv = view(out);
  dv.setUint32(mdatAt, 1);
  out.set(new TextEncoder().encode('mdat'), mdatAt + 4);
  dv.setBigUint64(mdatAt + 8, BigInt(placed.end - mdatAt));
  for (const { ti, ci } of order) {
    let at = placed.offsets[ti][ci];
    for (const s of tracks[ti].chunks[ci].samples) { out.set(tracks[ti].bytes.subarray(s.offset, s.offset + s.size), at); at += s.size; }
  }
  return out;
}

// The media time a track's own edit list starts at (an audio track's priming), if it has one.
function editStart(bytes, elst) {
  const dv = view(bytes), version = bytes[elst.body], count = dv.getUint32(elst.body + 4);
  let at = elst.body + 8;
  for (let i = 0; i < count; i++) {
    const mediaTime = version === 1 ? Number(dv.getBigInt64(at + 8)) : dv.getInt32(at + 4);
    if (mediaTime >= 0) return mediaTime;
    at += version === 1 ? 20 : 12;
  }
  return 0;
}
