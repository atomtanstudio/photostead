/**
 * What sidecar files say about the photos beside them, read in the browser before uploading:
 *
 * - Google Takeout: a JSON note per photo (when it was taken, its description, where, whether it was
 *   a favorite, archived or in the trash), and an album's folder with its own metadata.json. Takeout
 *   names the notes loosely: IMG_1.JPG.json, IMG_1.JPG.supplemental-metadata.json (often cut short:
 *   .supplemental-me.json, .suppl.json), IMG_1.JPG(1).json for IMG_1(1).JPG, the edited copy
 *   IMG_1-edited.JPG sharing IMG_1.JPG's note, a Live Photo's video sharing its photo's, and long
 *   names cut short. Items Takeout says were in the trash aren't brought over.
 * - XMP sidecars (Apple Photos' "Export IPTC as XMP", Lightroom, darktable): IMG_1.xmp or IMG_1.HEIC.xmp,
 *   with a title or caption, keywords, stars, when and where.
 */

export type Picked = { file: File; path: string };

export type Extra = {
  takenAt?: string;
  description?: string;
  keywords?: string[];
  album?: string;
  favorite?: boolean;
  archived?: boolean;
  rating?: number;
  latitude?: number;
  longitude?: number;
  skip?: boolean; // in Takeout's trash
};

const dirOf = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '');
const nameOf = (p: string) => p.slice(p.lastIndexOf('/') + 1);

/** A Takeout note's key: the media file name it belongs to, with any "(n)" kept at the end. */
export function noteKey(jsonName: string): string {
  let s = jsonName.replace(/\.json$/i, '');
  let n = '';
  const dup = s.match(/(\(\d+\))$/);
  if (dup) {
    n = dup[1];
    s = s.slice(0, -n.length);
  }
  // .supplemental-metadata, or a cut-short piece of it, after the media file's own extension.
  const cut = s.match(/^(.*\.[A-Za-z0-9]{2,5})\.(s[a-z-]*)$/);
  if (cut && 'supplemental-metadata'.startsWith(cut[2].toLowerCase())) s = cut[1];
  return s + n;
}

/** The keys a media file's note may be under, best first. */
export function mediaKeys(name: string): string[] {
  const m = name.match(/^(.*?)(\(\d+\))?(\.[^.]+)$/);
  if (!m) return [name];
  const [, base, n = '', ext] = m;
  const plain = base.replace(/-(edited|bearbeitet|modifié|editado)$/i, '');
  return [...new Set([base + ext + n, plain + ext + n, name, plain + ext])];
}

/** Takeout's note for a media file among a folder's notes (by key). */
export function findNote<T>(name: string, notes: Map<string, T>): T | undefined {
  for (const k of mediaKeys(name)) {
    const hit = notes.get(k);
    if (hit) return hit;
  }
  // A Live Photo's video (IMG_1.MP4) shares its photo's note (IMG_1.HEIC.json).
  const base = name.replace(/(\(\d+\))?\.[^.]+$/, '').replace(/-(edited)$/i, '');
  for (const [k, v] of notes) if (k.startsWith(base + '.') && k.slice(base.length + 1).match(/^[A-Za-z0-9]{2,5}(\(\d+\))?$/)) return v;
  // Long names: Takeout cuts the note's name short.
  let best: [number, T] | undefined;
  for (const [k, v] of notes) if (k.length >= 20 && name.startsWith(k) && (!best || k.length > best[0])) best = [k.length, v];
  return best?.[1];
}

type TakeoutNote = {
  title?: string;
  description?: string;
  photoTakenTime?: { timestamp?: string };
  geoData?: { latitude?: number; longitude?: number };
  geoDataExif?: { latitude?: number; longitude?: number };
  favorited?: boolean;
  archived?: boolean;
  trashed?: boolean;
};

const YEARS = /^(Photos from \d{4}|Google Photos|Google Fotos|Photos|Archive|Trash|Bin|Untitled.*)$/i;

function fromTakeout(n: TakeoutNote): Extra {
  const e: Extra = {};
  const ts = Number(n.photoTakenTime?.timestamp);
  if (ts > 0) e.takenAt = new Date(ts * 1000).toISOString();
  if (n.description?.trim()) e.description = n.description.trim();
  const geo = n.geoData?.latitude || n.geoData?.longitude ? n.geoData : n.geoDataExif;
  if (geo && (geo.latitude || geo.longitude)) {
    e.latitude = geo.latitude;
    e.longitude = geo.longitude;
  }
  if (n.favorited) e.favorite = true;
  if (n.archived) e.archived = true;
  if (n.trashed) e.skip = true;
  return e;
}

/** XMP's values, by namespace and name: an attribute on a Description, or an element (its text,
 *  or the items of its list). */
function xmpValues(doc: Document, ns: string, local: string): string[] {
  const out: string[] = [];
  for (const d of [...doc.getElementsByTagNameNS('http://www.w3.org/1999/02/22-rdf-syntax-ns#', 'Description')]) {
    const a = d.getAttributeNS(ns, local);
    if (a) out.push(a);
  }
  for (const el of [...doc.getElementsByTagNameNS(ns, local)]) {
    const items = [...el.getElementsByTagNameNS('http://www.w3.org/1999/02/22-rdf-syntax-ns#', 'li')];
    if (items.length) items.forEach((li) => li.textContent?.trim() && out.push(li.textContent.trim()));
    else if (el.textContent?.trim()) out.push(el.textContent.trim());
  }
  return out;
}

/** A GPS coordinate as XMP writes it ("48,51.504N", or a number with a separate ref). */
export function gps(v: string | undefined, ref?: string): number | undefined {
  if (!v) return undefined;
  const m = v.trim().match(/^(-?\d+(?:\.\d+)?)(?:,(\d+(?:\.\d+)?))?(?:,(\d+(?:\.\d+)?))?\s*([NSEW])?$/i);
  if (!m) return undefined;
  let x = Number(m[1]) + (m[2] ? Number(m[2]) / 60 : 0) + (m[3] ? Number(m[3]) / 3600 : 0);
  const hemi = (m[4] || ref || '').toUpperCase();
  if (hemi === 'S' || hemi === 'W') x = -Math.abs(x);
  return Number.isFinite(x) ? x : undefined;
}

export function fromXMP(text: string): Extra {
  const doc = new DOMParser().parseFromString(text, 'application/xml');
  if (doc.getElementsByTagName('parsererror').length) return {};
  const DC = 'http://purl.org/dc/elements/1.1/';
  const XMP = 'http://ns.adobe.com/xap/1.0/';
  const PS = 'http://ns.adobe.com/photoshop/1.0/';
  const EXIF = 'http://ns.adobe.com/exif/1.0/';
  const e: Extra = {};
  const caption = xmpValues(doc, DC, 'description')[0] || xmpValues(doc, DC, 'title')[0];
  if (caption) e.description = caption;
  const words = xmpValues(doc, DC, 'subject');
  if (words.length) e.keywords = [...new Set(words)];
  const date = xmpValues(doc, PS, 'DateCreated')[0] || xmpValues(doc, EXIF, 'DateTimeOriginal')[0] || xmpValues(doc, XMP, 'CreateDate')[0];
  const t = date ? Date.parse(date) : NaN;
  if (Number.isFinite(t)) e.takenAt = new Date(t).toISOString();
  const stars = Number(xmpValues(doc, XMP, 'Rating')[0]);
  if (stars >= 1 && stars <= 5) e.rating = Math.round(stars);
  const lat = gps(xmpValues(doc, EXIF, 'GPSLatitude')[0], xmpValues(doc, EXIF, 'GPSLatitudeRef')[0]);
  const lon = gps(xmpValues(doc, EXIF, 'GPSLongitude')[0], xmpValues(doc, EXIF, 'GPSLongitudeRef')[0]);
  if (lat !== undefined && lon !== undefined) {
    e.latitude = lat;
    e.longitude = lon;
  }
  return e;
}

/** What the sidecars among what was picked say, by the media file's path. */
export async function readSidecars(picked: Picked[]): Promise<{ extras: Map<string, Extra>; from: string }> {
  const extras = new Map<string, Extra>();
  const notes = new Map<string, Map<string, TakeoutNote>>(); // folder -> key -> note
  const albums = new Map<string, string>(); // folder -> album title (Takeout's metadata.json)
  const xmps = new Map<string, File>(); // path without extension, lower case -> sidecar
  let takeout = false;
  for (const { file, path } of picked) {
    const name = nameOf(path);
    if (/\.xmp$/i.test(name)) {
      xmps.set(path.replace(/\.xmp$/i, '').toLowerCase(), file);
    } else if (/\.json$/i.test(name) && file.size < 1 << 20) {
      let note: TakeoutNote & { albumData?: unknown };
      try {
        note = JSON.parse(await file.text());
      } catch {
        continue;
      }
      const dir = dirOf(path);
      if (/^metadata(\(\d+\))?\.json$/i.test(name) || /^metadata\.json$/i.test(name)) {
        if (note.title) albums.set(dir, note.title);
        continue;
      }
      if (!note.photoTakenTime && !note.geoData && !note.title) continue;
      takeout = true;
      if (!notes.has(dir)) notes.set(dir, new Map());
      notes.get(dir)!.set(noteKey(name), note);
      if (note.title && !notes.get(dir)!.has(note.title)) notes.get(dir)!.set(note.title, note);
    }
  }
  let fromXmp = false;
  for (const { file, path } of picked) {
    const name = nameOf(path);
    if (/\.(json|xmp)$/i.test(name)) continue;
    const dir = dirOf(path);
    let extra: Extra | undefined;
    const folderNotes = notes.get(dir);
    const note = folderNotes && findNote(name, folderNotes);
    if (note) {
      extra = fromTakeout(note);
      const folder = nameOf(dir);
      const album = albums.get(dir) || (folder && !YEARS.test(folder) ? folder : '');
      if (album) extra.album = album;
      if (/^archive$/i.test(folder)) extra.archived = true;
    }
    const xmp = xmps.get(path.toLowerCase()) || xmps.get(path.replace(/\.[^./]+$/, '').toLowerCase());
    if (xmp) {
      fromXmp = true;
      extra = { ...fromXMP(await xmp.text()), ...extra };
    }
    if (extra && Object.keys(extra).length) extras.set(path, extra);
    void file;
  }
  return { extras, from: takeout ? 'Google Takeout’s notes' : fromXmp ? 'the XMP sidecars' : '' };
}
