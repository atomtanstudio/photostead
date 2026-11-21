// The sites besides X: which pages the capture button runs on, which servers their media may be
// downloaded from, and the save plan for a post a site module read (content/sites/*.js).
// The service worker checks every post against this before downloading anything.

const host = (name, list) => list.some(h => name === h || name.endsWith(`.${h}`));

export const SITES = {
  civitai: {
    name: 'CivitAI',
    pages: ['civitai.com', 'civitai.red', 'civitai.green'],
    // image.civitai.com, which sends originals on to its blob store (blobs-b2.civitai.com).
    media: ['civitai.com', 'civitai.red', 'civitai.green'],
  },
  instagram: {
    name: 'Instagram',
    pages: ['instagram.com'],
    media: ['cdninstagram.com', 'fbcdn.net'],
  },
  tiktok: {
    name: 'TikTok',
    pages: ['tiktok.com'],
    media: ['tiktok.com', 'tiktokcdn.com', 'tiktokcdn-us.com', 'tiktokv.com', 'tiktokv.us', 'ibyteimg.com'],
    // Its video servers (Akamai) turn away requests that don't come from its pages, so the page
    // downloads the files and hands them over (content/sites/common.js). Its picture servers don't
    // let pages read them, so photo posts' pictures are downloaded by the service worker.
    pageDownload: true,
  },
  pinterest: {
    name: 'Pinterest',
    pages: ['pinterest.com', 'pinterest.ca', 'pinterest.co.uk', 'pinterest.com.au', 'pinterest.de', 'pinterest.fr',
      'pinterest.es', 'pinterest.it', 'pinterest.jp', 'pinterest.com.mx', 'pinterest.nz', 'pinterest.ie'],
    media: ['pinimg.com'],
  },
};

const safeUrl = value => {
  const url = new URL(value);
  if (url.protocol !== 'https:' || url.username || url.password) throw new Error('Only HTTPS media can be downloaded.');
  return url;
};

// Which site a page belongs to (its key in SITES), or '' when it's none of them.
export function siteOf(pageUrl) {
  try {
    const url = new URL(pageUrl);
    if (url.protocol !== 'https:') return '';
    return Object.keys(SITES).find(key => host(url.hostname, SITES[key].pages)) || '';
  } catch { return ''; }
}

// A media address on the site's own servers, or an error.
export function siteMediaUrl(site, value) {
  const url = safeUrl(value);
  if (!SITES[site] || !host(url.hostname, SITES[site].media)) throw new Error(`Media from ${url.hostname} isn’t one of ${SITES[site]?.name || 'the site'}’s servers.`);
  return url.href;
}

const EXT = /^(jpe?g|png|webp|gif|avif|heic|mp4|mov|webm)$/i;
function extension(m, url, video) {
  if (m.ext && EXT.test(m.ext)) return m.ext.toLowerCase().replace('jpeg', 'jpg');
  const last = new URL(url).pathname.split('/').pop() || '';
  const ext = last.includes('.') ? last.split('.').pop() : '';
  if (EXT.test(ext) && /^(mp4|mov|webm)$/i.test(ext) === video) return ext.toLowerCase().replace('jpeg', 'jpg');
  return video ? 'mp4' : 'jpg';
}
const text = value => String(value ?? '').replace(/\r\n/g, '\n').trim();

// The save plan for one post: every file, each with the post's description, link and date.
// post: { site, id, url, author, authorName, caption, details, takenAt, prompt,
//         media: [{ url, video, ext, key, fallbacks }] }
export function postPlan(post) {
  const site = String(post?.site || '');
  if (!SITES[site]) throw new Error('This site isn’t one Photostead Capture saves from.');
  const id = String(post.id || '');
  if (!/^[A-Za-z0-9_-]{1,80}$/.test(id)) throw new Error('The post couldn’t be identified. Open it and try again.');
  const source = new URL(post.url);
  if (source.protocol !== 'https:' || siteOf(source.href) !== site) throw new Error('The post’s link isn’t on the site it came from.');
  const byline = [text(post.authorName), text(post.author) && `@${text(post.author).replace(/^@/, '')}`].filter(Boolean).join(' · ');
  const description = [text(post.caption), text(post.details), byline, source.href].filter(Boolean).join('\n\n');
  const date = new Date(post.takenAt || Date.now());
  const createdAt = Number.isNaN(date.getTime()) ? new Date().toISOString() : date.toISOString();
  const media = (post.media || []).filter(m => m?.url).slice(0, 60);
  if (!media.length) throw new Error('No images or videos were found in this post.');
  const seen = new Set();
  const items = media.map((m, index) => {
    const video = Boolean(m.video);
    const url = siteMediaUrl(site, m.url);
    const fallbacks = (m.fallbacks || []).flatMap(u => { try { return [siteMediaUrl(site, u)]; } catch { return []; } }).filter(u => u !== url).slice(0, 6);
    const key = String(m.key || new URL(url).pathname);
    const filename = `${site}-${id}-${index + 1}.${extension(m, url, video)}`;
    const item = { key, index, url, video, site, filename, description, source: source.href, createdAt };
    if (fallbacks.length) item.fallbacks = fallbacks;
    if (text(post.prompt)) item.prompt = text(post.prompt);
    return item;
  }).filter(m => { if (seen.has(m.key)) return false; seen.add(m.key); return true; });
  return { tweetId: `${site}:${id}`, site, description, items };
}
