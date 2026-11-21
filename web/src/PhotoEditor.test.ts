import { describe, expect, it } from 'vitest';
import { fitShape, mirror, tidy, turnLeft } from './PhotoEditor';

describe('photo editor sums', () => {
  it('sends only what changes something', () => {
    expect(tidy({ rotate: 0, flip: false, crop: { x: 0, y: 0, w: 1, h: 1 }, exposure: 0.001, contrast: 0 })).toEqual({ rotate: 0 });
    expect(tidy({ rotate: 90, exposure: 0.6049, crop: { x: 0.123456, y: 0, w: 0.5, h: 1 } })).toEqual({ rotate: 90, exposure: 0.6, crop: { x: 0.1235, y: 0, w: 0.5, h: 1 } });
  });
  it('keeps a crop on the same part of the picture when it turns', () => {
    const c = { x: 0.1, y: 0.2, w: 0.3, h: 0.4 };
    // Four quarter turns are back where they started.
    const back = turnLeft(turnLeft(turnLeft(turnLeft(c))));
    for (const k of ['x', 'y', 'w', 'h'] as const) expect(back[k]).toBeCloseTo(c[k]);
    // The top-left corner goes to the bottom-left.
    expect(turnLeft({ x: 0, y: 0, w: 0.5, h: 0.5 })).toEqual({ x: 0, y: 0.5, w: 0.5, h: 0.5 });
    expect(mirror(c).x).toBeCloseTo(0.6);
  });
  it('fits a shape in the picture', () => {
    // A square in a 400×200 picture: half its width, all its height, centred.
    expect(fitShape(1, 400, 200, { x: 0, y: 0, w: 1, h: 1 })).toEqual({ x: 0.25, y: 0, w: 0.5, h: 1 });
    // 16:9 in a square: all the width.
    const r = fitShape(16 / 9, 300, 300, { x: 0, y: 0, w: 1, h: 1 });
    expect(r.w).toBe(1);
    expect(r.h).toBeCloseTo(9 / 16);
  });
});
