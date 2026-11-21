// Naming a place as the server does (server/internal/places): the nearest town within 60 km, by
// great-circle distance.

export const MAX_KM = 60;

export function haversine(lat1, lon1, lat2, lon2) {
  const r = 6371.0;
  const p1 = (lat1 * Math.PI) / 180, p2 = (lat2 * Math.PI) / 180;
  const dp = ((lat2 - lat1) * Math.PI) / 180, dl = ((lon2 - lon1) * Math.PI) / 180;
  const a = Math.sin(dp / 2) ** 2 + Math.cos(p1) * Math.cos(p2) * Math.sin(dl / 2) ** 2;
  return 2 * r * Math.asin(Math.min(1, Math.sqrt(a)));
}

/** The town nearest to lat, lon among towns ([id, name, region, country, cc, lat, lon]), or null past MAX_KM. */
export function nearest(towns, lat, lon) {
  let best = Infinity, town = null;
  for (const t of towns) {
    const d = haversine(lat, lon, t[5], t[6]);
    if (d < best) [best, town] = [d, t];
  }
  return best <= MAX_KM ? town : null;
}
