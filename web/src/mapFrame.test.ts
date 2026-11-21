import { describe, expect, it } from 'vitest';
import { homeBounds } from './mapFrame';

const many = (n: number, lat: number, lon: number) => Array.from({ length: n }, (_, i) => ({ lat: lat + (i % 7) * 0.01, lon: lon + (i % 5) * 0.01 }));

describe('where the Places map opens', () => {
  it('opens on home and what is near it, not on every trip', () => {
    const home = [...many(51, 45.52, -122.68), ...many(9, 45.89, -123.96), ...many(6, 45.35, -121.98), ...many(4, 45.63, -121.54)];
    const trips = [...many(5, 35.01, 135.77), ...many(4, 38.71, -9.13), ...many(6, 64.25, -15.21)];
    const [[west, south], [east, north]] = homeBounds([...trips, ...home])!;
    expect(west).toBeCloseTo(-123.96, 5);
    expect(east).toBeCloseTo(-121.54 + 0.03, 5); // four items: the fourth is 0.03 east
    expect(south).toBeCloseTo(45.35, 5);
    expect(north).toBeCloseTo(45.89 + 0.06, 5);
  });

  it('frames everything when it is all near home', () => {
    const home = [...many(3, 45.52, -122.68), ...many(2, 45.89, -123.96)];
    const [[west, south], [east, north]] = homeBounds(home)!;
    expect([west, south, east, north].map((x) => +x.toFixed(5))).toEqual([-123.96, 45.52, -122.66, 45.9]);
  });

  it('takes home to be where the items are, not where the most places are', () => {
    const quiet = [{ lat: 10, lon: 10 }, { lat: 10.6, lon: 10.6 }, { lat: 11.2, lon: 10 }];
    const [[west, south]] = homeBounds([...quiet, ...many(40, -30, 150)])!;
    expect([west, south]).toEqual([150, -30]);
  });

  it('keeps islands either side of the date line together', () => {
    const [[west], [east]] = homeBounds([...many(5, -16.8, 179.9), ...many(4, -17.2, -178.9)])!;
    expect(west).toBeCloseTo(179.9, 5);
    expect(east).toBeCloseTo(181.1 + 0.03, 5);
  });

  it('has nothing to frame without items', () => {
    expect(homeBounds([])).toBeNull();
  });
});
