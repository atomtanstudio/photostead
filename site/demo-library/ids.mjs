// The demo library's ids: steady across builds, and shaped like the server's uuids (the app checks).
import { createHash } from 'node:crypto';

export const uuid = (seed) => {
  const h = createHash('sha1').update(`photostead-demo:${seed}`).digest('hex');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-5${h.slice(13, 16)}-${((parseInt(h[16], 16) & 3) | 8).toString(16)}${h.slice(17, 20)}-${h.slice(20, 32)}`;
};

/** A picture's id from its shot and number in shots.json: "b08" is b08's first, "b08#2" its second. */
export const pictureId = (ref) => {
  const [shot, n] = ref.split('#');
  return uuid(`${shot}#${n ?? 1}`);
};
