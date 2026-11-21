// Writes towns.json: the towns of GeoNames' cities1000 list near the demo's places, with their
// region and country names, the way the server's gazetteer (server/internal/places) reads them. The
// library build names each picture's place from it as the server does: the nearest town within
// 60 km. Only needed again when PLACES changes.
//
// Run: GEONAMES=<a folder with cities1000.txt, admin1CodesASCII.txt and countryInfo.txt> npm run towns
// (a Photostead server downloads them into <data>/geodata). GeoNames data is CC BY 4.0.

import { createReadStream } from 'node:fs';
import { readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { createInterface } from 'node:readline';
import { fileURLToPath } from 'node:url';
import { PLACES } from './config.mjs';
import { haversine } from './towns-near.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));
const DIR = process.env.GEONAMES;
if (!DIR) {
  console.error('Set GEONAMES to a folder with GeoNames’ cities1000.txt, admin1CodesASCII.txt and countryInfo.txt.');
  process.exit(1);
}
// A little past the server's 60 km, so every town nearer than that to a picture is here.
const WITHIN_KM = 70;

/** Two tab-separated columns of a GeoNames file, as a map (comment lines skipped). */
async function table(file, key, value) {
  const m = new Map();
  for (const line of (await readFile(join(DIR, file), 'utf8')).split('\n')) {
    if (line.startsWith('#')) continue;
    const col = line.split('\t');
    if (col.length > Math.max(key, value)) m.set(col[key], col[value]);
  }
  return m;
}

const admin1 = await table('admin1CodesASCII.txt', 0, 1);
const countries = await table('countryInfo.txt', 0, 4);
const near = Object.values(PLACES).map(([lat, lon]) => [lat, lon]);
const towns = [];
for await (const line of createInterface({ input: createReadStream(join(DIR, 'cities1000.txt')) })) {
  // geonameid, name, asciiname, alternatenames, latitude, longitude, feature class, feature code,
  // country code, cc2, admin1 code, …
  const col = line.split('\t');
  if (col.length < 11) continue;
  const lat = Number(col[4]), lon = Number(col[5]);
  if (!near.some(([a, b]) => haversine(a, b, lat, lon) <= WITHIN_KM)) continue;
  towns.push([Number(col[0]), col[1], admin1.get(`${col[8]}.${col[10]}`) ?? '', countries.get(col[8]) ?? '', col[8], lat, lon]);
}
towns.sort((a, b) => a[0] - b[0]);
const out = {
  about: 'Towns from GeoNames’ cities1000 (geonames.org, CC BY 4.0) near the demo’s places: id, name, region, country, country code, latitude, longitude.',
  towns,
};
await writeFile(join(HERE, 'towns.json'), JSON.stringify(out).replace(/\],\[/g, '],\n['));
console.log(`wrote ${towns.length} towns`);
