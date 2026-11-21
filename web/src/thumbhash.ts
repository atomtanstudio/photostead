import { thumbHashToDataURL } from 'thumbhash';

// Decoded placeholders, by asset id. ThumbHashes are ~25 bytes; the data URLs are ~1–2 KB.
const cache = new Map<string, string>();

/** A blurred placeholder image (data URL) for an asset, or undefined if it has no ThumbHash yet. */
export function placeholder(id: string, hash?: string): string | undefined {
  if (!hash) return undefined;
  let url = cache.get(id);
  if (!url) {
    const bytes = Uint8Array.from(atob(hash), (c) => c.charCodeAt(0));
    url = thumbHashToDataURL(bytes);
    if (cache.size > 20000) cache.clear();
    cache.set(id, url);
  }
  return url;
}
