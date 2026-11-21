// Where the Places map opens: around home, not around everything. Fitting every item at once, a
// few trips abroad left a map of ocean with home's pins crowded together; the iPhone's map
// opens the same way (placesMapFrame in ios/Photostead/Places.swift).

type Point = { lat: number; lon: number };

/** West, south, east and north. East may pass 180 when the area crosses the date line, which
 *  MapLibre's fitBounds takes as it is. */
export type Bounds = [[number, number], [number, number]];

const wrap = (lon: number) => ((((lon + 540) % 360) + 360) % 360) - 180;

/** Kilometres between two points: near enough for a few hundred, and cheap. */
function km(a: Point, b: Point) {
  const x = Math.abs(wrap(a.lon - b.lon)) * Math.cos(((a.lat + b.lat) / 2) * (Math.PI / 180)) * 111.32;
  const y = (a.lat - b.lat) * 110.57;
  return Math.hypot(x, y);
}

/** The items around the busiest spot: where the most of them lie within `reach` km of one
 *  another (home, for most people), as the bounds to fit; null with none. Items are counted in
 *  half-degree squares first, so a library of 100,000 stays quick. */
export function homeBounds(points: Point[], reach = 300): Bounds | null {
  type Cell = Point & { n: number; south: number; north: number; west: number; east: number };
  const cells = new Map<string, Cell>();
  for (const p of points) {
    const key = `${Math.floor(p.lat * 2)},${Math.floor(p.lon * 2)}`;
    const c = cells.get(key);
    if (c) {
      c.n++;
      c.south = Math.min(c.south, p.lat);
      c.north = Math.max(c.north, p.lat);
      c.west = Math.min(c.west, p.lon);
      c.east = Math.max(c.east, p.lon);
    } else {
      cells.set(key, { lat: p.lat, lon: p.lon, n: 1, south: p.lat, north: p.lat, west: p.lon, east: p.lon });
    }
  }
  const all = [...cells.values()];
  // The busiest squares are the candidates for home.
  let best: { home: Cell; near: Cell[]; n: number } | null = null;
  for (const home of [...all].sort((a, b) => b.n - a.n).slice(0, 200)) {
    const near = all.filter((c) => km(c, home) <= reach);
    const n = near.reduce((sum, c) => sum + c.n, 0);
    if (!best || n > best.n) best = { home, near, n };
  }
  if (!best) return null;
  // Longitudes as seen from home, so items across the date line from it stay beside it.
  const from = best.home.lon;
  const seen = (lon: number) => from + wrap(lon - from);
  const west = Math.min(...best.near.map((c) => seen(c.west)));
  const east = Math.max(...best.near.map((c) => seen(c.east)));
  const south = Math.min(...best.near.map((c) => c.south));
  const north = Math.max(...best.near.map((c) => c.north));
  return [[west, south], [east, north]];
}
