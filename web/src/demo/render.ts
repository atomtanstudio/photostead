// The demo's stand-in for the server's previews of an edited photo (server/internal/media/edit.go):
// made in the browser from the demo's 1600 px picture, in the server's order (turned, mirrored,
// cropped, then light and color), at the sizes the server makes (1600 px and 640 px), with their
// ThumbHash. Saturation is done as the editor shows it.

import { rgbaToThumbHash } from 'thumbhash';
import type { Recipe } from '../api';

export type Rendered = { preview: string; thumbnail: string; thumbhash: string };

const canvas = (w: number, h: number) => {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  return c;
};

/** The picture scaled down to fit in side × side (never up). */
function fit(src: HTMLCanvasElement, side: number) {
  const s = Math.min(1, side / Math.max(src.width, src.height));
  if (s === 1) return src;
  const c = canvas(Math.max(1, Math.round(src.width * s)), Math.max(1, Math.round(src.height * s)));
  const g = c.getContext('2d')!;
  g.imageSmoothingQuality = 'high';
  g.drawImage(src, 0, 0, c.width, c.height);
  return c;
}

const blobURL = (c: HTMLCanvasElement, quality: number) =>
  new Promise<string>((done, fail) => c.toBlob((b) => (b ? done(URL.createObjectURL(b)) : fail(new Error('no picture'))), 'image/jpeg', quality));

/** Light and color, in place: per channel out = a·in + b (exposure, contrast about mid grey, warmth), then saturation. */
function look(px: Uint8ClampedArray, r: Recipe) {
  const k = 1 + (r.contrast ?? 0);
  const gain = 2 ** (r.exposure ?? 0) * k;
  const off = 128 * (1 - k);
  const warm = r.warmth ?? 0;
  const [ar, ag, ab] = [gain * (1 + 0.12 * warm), gain, gain * (1 - 0.12 * warm)];
  const linear = Boolean(r.exposure || r.contrast || r.warmth);
  const s = 1 + (r.saturation ?? 0);
  for (let i = 0; i < px.length; i += 4) {
    let R = px[i], G = px[i + 1], B = px[i + 2];
    if (linear) {
      R = Math.min(255, Math.max(0, Math.round(ar * R + off)));
      G = Math.min(255, Math.max(0, Math.round(ag * G + off)));
      B = Math.min(255, Math.max(0, Math.round(ab * B + off)));
    }
    if (s !== 1) {
      // feColorMatrix type="saturate", as the editor's preview
      const r2 = (0.213 + 0.787 * s) * R + (0.715 - 0.715 * s) * G + (0.072 - 0.072 * s) * B;
      const g2 = (0.213 - 0.213 * s) * R + (0.715 + 0.285 * s) * G + (0.072 - 0.072 * s) * B;
      const b2 = (0.213 - 0.213 * s) * R + (0.715 - 0.715 * s) * G + (0.072 + 0.928 * s) * B;
      [R, G, B] = [r2, g2, b2];
    }
    px[i] = R;
    px[i + 1] = G;
    px[i + 2] = B;
  }
}

/** The previews of the picture at src with recipe r applied, as blob: URLs, and their ThumbHash. */
export async function renderEdit(src: string, r: Recipe): Promise<Rendered> {
  const img = new Image();
  img.src = src;
  await img.decode();
  const [w, h] = [img.naturalWidth, img.naturalHeight];
  const sideways = r.rotate === 90 || r.rotate === 270;
  const [pw, ph] = sideways ? [h, w] : [w, h];
  const turned = canvas(pw, ph);
  const g = turned.getContext('2d')!;
  g.translate(pw / 2, ph / 2);
  if (r.flip) g.scale(-1, 1);
  g.rotate((r.rotate * Math.PI) / 180);
  g.drawImage(img, -w / 2, -h / 2);

  const c = r.crop ?? { x: 0, y: 0, w: 1, h: 1 };
  const left = Math.round(c.x * pw), top = Math.round(c.y * ph);
  const cw = Math.max(1, Math.min(pw - left, Math.round(c.w * pw))), ch = Math.max(1, Math.min(ph - top, Math.round(c.h * ph)));
  const out = canvas(cw, ch);
  const o = out.getContext('2d', { willReadFrequently: true })!;
  o.drawImage(turned, left, top, cw, ch, 0, 0, cw, ch);
  if (r.exposure || r.contrast || r.warmth || r.saturation) {
    const d = o.getImageData(0, 0, cw, ch);
    look(d.data, r);
    o.putImageData(d, 0, 0);
  }

  const small = fit(out, 100);
  const sd = small.getContext('2d')!.getImageData(0, 0, small.width, small.height);
  const thumbhash = btoa(String.fromCharCode(...rgbaToThumbHash(small.width, small.height, sd.data)));
  const [preview, thumbnail] = await Promise.all([blobURL(fit(out, 1600), 0.88), blobURL(fit(out, 640), 0.8)]);
  return { preview, thumbnail, thumbhash };
}
