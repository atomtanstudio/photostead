import { getTweetMediaList } from './media.js';
import { siteMediaUrl } from './sites.js';
import { hlsToMp4 } from './hls.js';

export function serverUrl(value) {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('Enter an HTTP or HTTPS Photostead server URL without credentials or query parameters.');
  }
  return url.href.replace(/\/+$/, '');
}

export function mediaUrl(value, video = false) {
  const url = new URL(value);
  if (url.protocol !== 'https:' || !(url.hostname === 'twimg.com' || url.hostname.endsWith('.twimg.com')) || url.username || url.password) {
    throw new Error('Only HTTPS media from X’s twimg.com servers can be downloaded.');
  }
  if (video && !url.pathname.endsWith('.mp4')) throw new Error('No downloadable MP4 was found; open the tweet, play the video, and try again.');
  if (!video && url.hostname === 'pbs.twimg.com') url.searchParams.set('name', 'orig');
  return url.href;
}

export function capturePlan(record) {
  const tweetId = String(record.focalTweetId || '');
  const tweet = record.tweets?.find(t => String(t.rest_id || t.id_str) === tweetId);
  if (!/^\d+$/.test(tweetId) || !tweet) throw new Error('The selected tweet was not captured. Open the tweet and try again.');
  const text = tweet.note_tweet?.note_tweet_results?.result?.text || tweet.full_text || '';
  const author = tweet.author || {};
  const source = `https://x.com/${author.screen_name || 'i'}/status/${tweetId}`;
  const description = [text, [author.name, author.screen_name && `@${author.screen_name}`].filter(Boolean).join(' · '), source].filter(Boolean).join('\n\n');
  const date = new Date(tweet.created_at || record.capturedAt);
  const createdAt = Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
  // API entities only: DOM video posters/quote cards must not become extra attachments.
  const media = getTweetMediaList({ ...tweet, dom_media: [] });
  if (!media.length) throw new Error('No image or video attachments were found on the selected tweet.');
  const seen = new Set();
  const items = media.map((m, index) => {
    const video = m.type === 'video' || m.type === 'animated_gif';
    const variants = (m.video_info?.variants || []).filter(v => v.content_type === 'video/mp4' && v.url).sort((a,b) => (b.bitrate || 0) - (a.bitrate || 0));
    const url = mediaUrl(video ? (variants[0]?.url || m.mp4_url) : m.media_url_https, video);
    const key = String(m.media_key || m.id_str || new URL(url).pathname);
    const format = new URL(url).searchParams.get('format') || new URL(url).pathname.split('.').pop();
    const ext = video ? 'mp4' : /^(jpg|jpeg|png|webp|gif|avif)$/i.test(format) ? format : 'jpg';
    return { key, url, video, filename: `x-${tweetId}-${index + 1}.${ext}`, description, source, createdAt };
  }).filter(m => { if (seen.has(m.key)) return false; seen.add(m.key); return true; });
  return { tweetId, description, items };
}

// Pairing: Photostead (Devices & remote access) shows a code, and a link that carries it with the
// server's id and addresses: photostead://pair?code=…&server=…&url=…&url=…
export function parsePairing(text) {
  const value = String(text || '').trim();
  if (/^photostead:\/\/pair\?/i.test(value)) {
    const q = new URLSearchParams(value.slice(value.indexOf('?') + 1));
    const urls = q.getAll('url').map(u => { try { return serverUrl(u); } catch { return ''; } }).filter(Boolean);
    return { code: q.get('code') || '', serverId: q.get('server') || '', urls };
  }
  return { code: value, serverId: '', urls: [] };
}

// The addresses to try, in order: the one that answered last, then the rest (tailnet, public, home).
export function addressesOf(settings) {
  return [...new Set([settings.serverUrl, ...(settings.addresses || [])].filter(Boolean).map(serverUrl))];
}

// The credential: a paired device's token (psd_…), or an API key (psk_…) from before pairing.
const token = settings => settings.token || settings.apiKey || '';

// Which address answers now, checked quickly (one that isn't reachable, like home when away, would
// otherwise hang for a long time), and that it's the same server. Remembered for a few minutes.
let reached = { key: '', url: '', at: 0 };
export async function reachable(settings, fetchImpl = fetch) {
  const list = addressesOf(settings);
  const key = `${list.join(' ')}|${settings.serverId || ''}`;
  if (reached.key === key && Date.now() - reached.at < 5 * 60000) return reached.url;
  for (const url of list) {
    try {
      const r = await fetchImpl(`${url}/api/v1/server`, { credentials: 'omit', redirect: 'error', signal: AbortSignal.timeout(list.length > 1 ? 4000 : 15000) });
      const info = await r.json();
      if (r.ok && (!settings.serverId || info.id === settings.serverId)) {
        reached = { key, url, at: Date.now() };
        return url;
      }
    } catch { /* not here: try the next */ }
  }
  throw new Error(list.length ? `Photostead can't be reached at ${list.join(' or ')}.` : 'Pair with Photostead in the extension popup first.');
}
export const forgetReachable = () => { reached = { key: '', url: '', at: 0 }; };

export async function api(settings, path, init = {}) {
  const base = await reachable(settings);
  const response = await fetch(`${base}${path}`, {
    ...init, credentials: 'omit', redirect: 'error',
    headers: { ...init.headers, Authorization: `Bearer ${token(settings)}` },
    signal: AbortSignal.timeout(240000),
  }).catch(error => { forgetReachable(); throw error; }); // went away mid-way: look again next time
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`Photostead ${response.status}: ${body.detail || body.title || response.statusText}`);
  settings.serverUrl = base; // the address that answered goes first next time
  return body;
}

// Pairs with a code at the first address that answers: the device's token, and the server's id and
// addresses to keep.
export async function pair(urls, code, name) {
  let lastError = new Error('Enter Photostead’s address, or paste the pairing link.');
  for (const url of urls) {
    try {
      const response = await fetch(`${url}/api/v1/devices/pair`, {
        method: 'POST', credentials: 'omit', redirect: 'error', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code, name, platform: 'extension' }), signal: AbortSignal.timeout(15000),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.detail || body.title || `Photostead ${response.status}`);
      const a = body.addresses || {};
      return { serverUrl: url, token: body.token, serverId: body.server?.id || '', addresses: [a.tailnet, a.public, a.home].filter(Boolean).map(serverUrl) };
    } catch (error) {
      lastError = error instanceof TypeError ? new Error(`Photostead didn't answer at ${url}.`) : error;
      if (!(error instanceof TypeError) && !(error?.name === 'TimeoutError')) break; // it answered: a wrong code isn't an address problem
    }
  }
  throw lastError;
}

// Checks the server and key: the upload check answers only with a valid key.
export async function testConnection(settings) {
  await api(settings, '/api/v1/uploads/check', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sha256: [] }) });
  return { ok: true };
}

// Sends one attachment with the tweet's text, source link and date. Photostead keeps a single copy
// of the same bytes and adds the text to its description, so saving a reposted picture again is
// safe; a checkpoint means a retry never sends twice.
// file: the bytes, when the page downloaded them already (TikTok); else they're downloaded here.
export async function uploadItem(settings, item, checkpoint = {}, persist = async () => {}, file = null) {
  if (!checkpoint.assetId) {
    if (file && (!file.size || /text\/|application\/json/.test(file.type))) throw new Error('The site sent an empty file or an error page instead of media.');
    const blob = file || await download(item);
    const filename = namedFor(item, blob.type);
    const meta = { fileName: filename, takenAt: item.createdAt, description: item.description, sourceUrl: item.source || '' };
    if (item.prompt) meta.prompt = item.prompt; // a CivitAI picture's prompt, used when the file shows it's AI and doesn't say
    const form = new FormData();
    form.append('meta', new Blob([JSON.stringify(meta)], { type: 'application/json' }), 'meta.json');
    form.append('file', blob, filename);
    const result = await api(settings, '/api/v1/uploads', { method: 'POST', body: form });
    if (!result.sha256) throw new Error('Photostead did not confirm the upload.');
    checkpoint.assetId = result.assetId || result.sha256;
    checkpoint.duplicate = result.status === 'duplicate';
    checkpoint.described = true;
    await persist(checkpoint);
  }
  return checkpoint;
}

// The file itself, from X's servers, or another site's (item.site): those may pass it on to another
// of their servers (CivitAI's originals go to its blob store), never anywhere else. A site's other
// addresses for the same file (TikTok gives a few) are tried in turn. A video a site keeps only as an
// HLS stream (Pinterest's newer ones) is read from its stream and joined into one MP4 (hls.js).
async function download(item) {
  const sources = item.site ? [item.url, ...(item.fallbacks || [])] : [item.url];
  let lastError;
  for (const source of sources) {
    try {
      const url = item.site ? siteMediaUrl(item.site, source) : mediaUrl(source, item.video);
      if (item.site && item.video && /\.m3u8$/i.test(new URL(url).pathname)) {
        return new Blob([await hlsToMp4(url, address => fetchBytes(item.site, address))], { type: 'video/mp4' });
      }
      const response = await fetch(url, { credentials: 'omit', redirect: item.site ? 'follow' : 'error', referrerPolicy: 'no-referrer', signal: AbortSignal.timeout(240000) });
      if (item.site && response.url) siteMediaUrl(item.site, response.url);
      if (!response.ok) throw new Error(`Media download failed (${response.status}). Open the post and capture again.`);
      const blob = await response.blob();
      if (!blob.size || /text\/|application\/json/.test(blob.type)) throw new Error('The site returned an empty file or an error page instead of media.');
      return blob;
    } catch (error) { lastError = error; }
  }
  throw lastError;
}

// One part of a stream (a playlist, or a file of its picture or sound), from the site's own servers.
async function fetchBytes(site, address) {
  const url = siteMediaUrl(site, address);
  const response = await fetch(url, { credentials: 'omit', redirect: 'follow', referrerPolicy: 'no-referrer', signal: AbortSignal.timeout(240000) });
  if (response.url) siteMediaUrl(site, response.url);
  if (!response.ok) throw new Error(`Media download failed (${response.status}). Open the post and capture again.`);
  return new Uint8Array(await response.arrayBuffer());
}

// A site's file named by what came: a picture fetched from another of its addresses can be another
// type (Pinterest keeps some originals as .webp where it names them .jpg).
const TYPES = { 'image/jpeg': 'jpg', 'image/png': 'png', 'image/webp': 'webp', 'image/gif': 'gif', 'image/avif': 'avif', 'image/heic': 'heic' };
function namedFor(item, type) {
  const ext = item.site && !item.video && TYPES[String(type).split(';')[0]];
  return ext ? item.filename.replace(/\.[a-z0-9]+$/i, `.${ext}`) : item.filename;
}
