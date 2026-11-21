// The demo pictures' originals, downloaded once into .originals/ (DEMO_ORIGINALS elsewhere): for
// the website's sizes (build.mjs) and for loading the household into a real server (load.mjs).

import { execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, rename, stat } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

export const CACHE = process.env.DEMO_ORIGINALS ?? join(dirname(fileURLToPath(import.meta.url)), '.originals');
const run = promisify(execFile);

/** Downloads a file once; curl follows the system's proxy settings. It downloads to a .part file
 *  and renames it when it's whole, so a download that's cut off is tried again next time. */
export async function cached(url, name, fallback) {
  const file = join(CACHE, name);
  if (existsSync(file) && (await stat(file)).size > 0) return file;
  await mkdir(CACHE, { recursive: true });
  for (const u of [url, fallback].filter(Boolean)) {
    try {
      await run('curl', ['-fsSL', '--retry', '3', '--max-time', '120', '-o', `${file}.part`, u]);
      await rename(`${file}.part`, file);
      return file;
    } catch (e) {
      console.warn(`  couldn't download ${u}: ${e.stderr?.trim() || e.message}`);
    }
  }
  throw new Error(`couldn't download ${name}`);
}

/** A picture's original: shots.json's shot, and which of its pictures (from 0). */
export const original = (shot, n) => cached(shot.images[n].src, `${shot.id}-${n + 1}.jpg`, shot.images[n].fal);
