// The demo's stand-in for the Photostead server: answers the web app's /api/v1 requests in the
// browser from the sample library, so the real app runs on a static site. Changes (favorites,
// albums, the trash, edits, share links, comments) last until the page is reloaded. Anything that
// needs a real server (sign-in methods, pairing a phone, uploads and imports, checking files)
// answers 403 with a short note.

import qrcode from 'qrcode-generator';
import type {
  AIModel, Album, AskResult, AssetDetail, Backups, Bucket, Character, Counts, HiddenDays, Household, Keyword, LastingCode, MapPoint, Memory, Person, PhotoEdit,
  Place, Prompt, Recipe, RecentAsset, Ref, Sample, SearchResult, ShareLink, SmartRules, Stack, TimelineAsset, User, VaultState,
} from '../api';
import { answer, askRequest, chips, describe, describeN, followUp, parse, planJSON, relaxable, structured, trimSpace, without, type Alt, type Plan, type Result, type Vocabulary } from './ask';
import { isDay, loadLocation, nowIn } from './gotime';
import { ilike, textSearch } from './pgtext';
import type { DemoGuest, DemoItem, DemoLibrary, DemoShared } from './library';

export type DemoRequest = { method: string; path: string; query: URLSearchParams; body: Record<string, unknown> };
export type DemoResponse = { status: number; body?: unknown };

const ok = (body?: unknown): DemoResponse => (body === undefined ? { status: 204 } : { status: 200, body });
// An error as the server's API library (huma) writes one: the status's name, the status, and what
// went wrong. huma's own checks of a request body also list each problem (errors[]); the demo says
// only "validation failed", the detail the app shows.
const STATUS_TEXT: Record<number, string> = { 403: 'Forbidden', 404: 'Not Found', 409: 'Conflict', 422: 'Unprocessable Entity' };
const problem = (status: number, detail: string): DemoResponse => ({ status, body: { title: STATUS_TEXT[status] ?? 'Error', status, detail } });
export const NOT_IN_DEMO = 'Not in the demo: this needs your own Photostead server.';
const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

// "Leo's" (or "Leo’s") is "leo", so a possessive finds what the name does.
const words = (s: string): string[] =>
  s.toLowerCase().normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/['’]s\b/g, '').replace(/['’]/g, '').match(/[a-z0-9]+/g) ?? [];
const STOP = new Set(['a', 'an', 'the', 'of', 'in', 'on', 'at', 'with', 'and', 'or', 'to', 'for', 'from', 'by', 'my', 'our', 'me', 'us', 'is', 'are', 'was', 'were', 'show', 'find',
  'photos', 'photo', 'pictures', 'picture', 'pics', 'images', 'image', 'any', 'all', 'some', 'have', 'do', 'did', 'i', 'we', 'when', 'where', 'what', 'which', 'who', 'how', 'many', 'much', 'that', 'this', 'those', 'these', 'it', 'its', 'like', 'there', 'their', 'them',
  'go', 'went', 'been', 'visit', 'visited', 'take', 'took', 'taken', 'get', 'got', 'see', 'saw', 'was', 'last', 'first', 'time']);
// Plural and singular, so "dogs" finds "dog" and "beaches" finds "beach".
const stem = (w: string) => (w.length > 4 && w.endsWith('ies') ? w.slice(0, -3) + 'y' : w.length > 4 && /(ches|shes|xes|ses)$/.test(w) ? w.slice(0, -2) : w.length > 3 && w.endsWith('s') && !w.endsWith('ss') ? w.slice(0, -1) : w);

// A few words people search with that the captions say another way: stands in for SigLIP's sense
// of meaning, which matches by what's in the picture rather than by the words.
const ALSO: Record<string, string[]> = {
  dog: ['pepper', 'collie', 'puppy'], puppy: ['pepper', 'collie'], cat: ['miso', 'tabby'], kitten: ['miso'], pet: ['pepper', 'miso', 'dog', 'cat'],
  kid: ['leo', 'june', 'child', 'boy', 'girl'], child: ['leo', 'june'], children: ['leo', 'june'], baby: ['june'], grandma: ['rosa'], grandmother: ['rosa'], nana: ['rosa'],
  ocean: ['beach', 'sea', 'tide', 'surf', 'coast'], sea: ['beach', 'ocean', 'tide', 'coast'], coast: ['beach', 'sea'], water: ['lake', 'river', 'sea', 'pond', 'waterfall', 'lagoon', 'sprinkler'],
  mountain: ['mt', 'hood', 'peak', 'caldera', 'ridge'], snow: ['winter', 'sled', 'powder', 'snowy'], winter: ['snow'], sunset: ['dusk', 'golden', 'sun'], night: ['stars', 'fireworks', 'dusk', 'milky'],
  food: ['cake', 'pastry', 'pancake', 'pastel', 'turkey', 'matcha', 'cocoa', 'tart', 'dinner', 'breakfast', 'apple'], dessert: ['cake', 'pastry', 'tart', 'pastel', 'donut'],
  party: ['birthday', 'candle', 'balloon'], birthday: ['candle', 'cake'], christmas: ['tree', 'ornament', 'king'], holiday: ['christmas', 'thanksgiving', 'halloween'],
  trip: ['lisbon', 'kyoto', 'iceland', 'beach', 'camping'], travel: ['lisbon', 'kyoto', 'iceland'], vacation: ['lisbon', 'kyoto', 'iceland', 'beach'], japan: ['kyoto'], portugal: ['lisbon', 'sintra', 'belem'],
  flower: ['blossom', 'peony', 'sunflower', 'daisy', 'wildflower'], flowers: ['blossom', 'peony'], tree: ['forest', 'fir', 'maple', 'orchard', 'birch', 'bamboo'], forest: ['tree', 'bamboo', 'fir', 'birch'],
  red: ['vermilion', 'crimson'], blue: ['cobalt', 'azulejo'], space: ['mars', 'planet', 'astronaut', 'galaxy', 'station'], robot: [], ai: ['generated'],
};

// Ask's limits, as the server's: items listed for a question answered by date, items a
// description is ranked among, and the closest of those listed.
const ASK_LIMIT = 2000;
const ASK_RANKING = 20000;
const MEANING_LIMIT = 300;

// A model or LoRA's name as the server keys it: the file name without folders or extension, lower case.
const modelKey = (name: string) => {
  const path = name.replaceAll('\\', '/').replace(/\/+$/, '');
  const base = name === '' ? '.' : path === '' ? '/' : path.slice(path.lastIndexOf('/') + 1);
  return base.replace(/\.(safetensors|ckpt|pt|pth|bin|gguf)$/i, '').trim().toLowerCase();
};

type Scope = { type: string; collection: string; folder: string; hideUtility: boolean; library: string; rating: number; flag: string; label: string };

// The demo household's server, away from home: on their tailnet, with Funnel off (see /remote).
const TAILNET = 'https://photostead.your-tailnet.ts.net';

const cmp = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

/** A place's name as the server writes it (placeName): the town, its region unless that's the town, the country. */
const placeName = (name: string, region: string, country: string) => [name, region && region !== name ? region : '', country].filter(Boolean).join(', ');

/** An edited photo's size, from the original's (media.Recipe.Size). */
export function editedSize(r: Recipe, w: number, h: number): [number, number] {
  if (r.rotate === 90 || r.rotate === 270) [w, h] = [h, w];
  const c = r.crop;
  if (c && !(c.x <= 0 && c.y <= 0 && c.w >= 1 && c.h >= 1)) [w, h] = [Math.max(1, Math.round(w * c.w)), Math.max(1, Math.round(h * c.h))];
  return [w, h];
}

/** Why the server refuses a recipe (media.Recipe.Check), or '' when it can be applied. */
function recipeProblem(r: Recipe): string {
  if (![0, 90, 180, 270].includes(r.rotate)) return 'rotate is 0, 90, 180 or 270';
  const c = r.crop;
  if (c && (c.w < 0.02 || c.h < 0.02 || c.x < 0 || c.y < 0 || c.x + c.w > 1.0001 || c.y + c.h > 1.0001)) return 'the crop must be inside the picture, and not tiny';
  for (const v of [(r.exposure ?? 0) / 2, r.contrast ?? 0, r.saturation ?? 0, r.warmth ?? 0]) {
    if (!(v >= -1 && v <= 1)) return 'exposure is -2 to 2 stops; contrast, saturation and warmth are -1 to 1';
  }
  return '';
}

/** A recipe that changes nothing (media.Recipe.Empty). */
const emptyRecipe = (r: Recipe) => r.rotate % 360 === 0 && !r.flip && (!r.crop || (r.crop.x <= 0 && r.crop.y <= 0 && r.crop.w >= 1 && r.crop.h >= 1)) &&
  !r.exposure && !r.contrast && !r.saturation && !r.warmth;

/** An address as a QR code, as the server sends one (an image data URL): black on white, 280 px. */
function qrImage(text: string): string {
  const q = qrcode(0, 'M');
  q.addData(text);
  q.make();
  const n = q.getModuleCount();
  let d = '';
  for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (q.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${n} ${n}" width="280" height="280" shape-rendering="crispEdges"><rect width="${n}" height="${n}" fill="#fff"/><path d="${d}"/></svg>`;
  return `data:image/svg+xml;base64,${btoa(svg)}`;
}

/** A pairing code as newPairCode makes one: eight letters and digits that can't be misread, as two fours. */
function pairCode(): string {
  const alphabet = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789';
  const b = new Uint8Array(8);
  crypto.getRandomValues(b);
  const s = [...b].map((x) => alphabet[x % alphabet.length]).join('');
  return `${s.slice(0, 4)}-${s.slice(4)}`;
}

/** A share link's token: 18 random bytes, base64url, as newLinkToken. */
function linkToken(): string {
  const b = new Uint8Array(18);
  crypto.getRandomValues(b);
  return btoa(String.fromCharCode(...b)).replace(/\+/g, '-').replace(/\//g, '_');
}

type DemoLink = { id: string; album: string; token: string; hasPassword: boolean; expiresAt?: string; allowDownload: boolean; allowUpload: boolean; createdAt: string };

export class DemoServer {
  private lib: DemoLibrary;
  private items: Map<string, DemoItem>;
  private me: User;
  private trashed = new Map<string, string>(); // id -> when
  private vault = new Set<string>();
  private vaultPin = '';
  private vaultOpen = false;
  private stacks = new Map<string, { id: string; kind: Stack['kind']; cover: string; members: string[]; state: 'suggested' | 'confirmed' }>();
  private albums: Map<string, { id: string; title: string; description?: string; parent?: string; cover?: string; kind: 'manual' | 'smart'; rules?: SmartRules }>;
  private hiddenPeople = new Set<string>();
  private outOfMemories = new Set<string>();
  private hiddenDays: HiddenDays[] = [];
  private names: Map<string, string>; // person id -> name, as renamed here
  private householdName: string;
  private counter = 0;
  // Photo edits: the recipe, and the ThumbHash of the previews the demo's renderer made with it.
  private edits = new Map<string, { recipe: Recipe; updatedAt: string; thumbhash?: string }>();
  private rendering = new Set<string>(); // edits whose previews are still being made
  private links = new Map<string, DemoLink>();
  private lastingCodes: LastingCode[] = []; // pairing codes that last, newest first (no device pairs with one here)
  // The shared album: who it's shared with, what was said and picked there, and what was put in it
  // here; and the guests (until the admin, the viewer, removes them).
  private sharedWith: DemoShared['members'];
  private comments: DemoShared['comments'];
  private picks: DemoShared['picks'];
  private putIn = new Map<string, string>(); // album:asset -> when, for what's added here
  private guests: DemoGuest[];

  constructor(lib: DemoLibrary) {
    this.lib = lib;
    this.sharedWith = lib.shared.members.map((s) => ({ ...s }));
    this.comments = lib.shared.comments.map((c) => ({ ...c }));
    this.picks = lib.shared.picks.map((p) => ({ ...p }));
    this.guests = (lib.guests ?? []).map((g) => ({ ...g }));
    this.items = new Map(lib.items.map((i) => [i.id, { ...i, keywords: [...i.keywords], albums: [...i.albums], people: [...i.people], labels: [...i.labels] }]));
    const m = lib.members[0];
    this.me = { id: m.id, email: m.email, username: m.name.toLowerCase(), displayName: m.name, role: 'admin', timeZone: lib.timeZone, timeZoneChosen: true,
      settings: { organize: false, setupHidden: true, setupSkipped: ['photos', 'phone', 'away', 'household', 'copy'] } }; // no setup checklist: there's no server to set up
    this.albums = new Map(lib.albums.map((a) => [a.id, { ...a, kind: 'manual' as const }]));
    this.names = new Map(lib.people.map((p) => [p.id, p.name]));
    this.householdName = lib.household.name;
    // Shots of one moment (and an AI run's variations) are suggested as look-alikes.
    const groups = new Map<string, DemoItem[]>();
    for (const i of lib.items) if (i.group) groups.set(i.group, [...(groups.get(i.group) ?? []), i]);
    for (const [g, list] of groups) {
      if (list.length < 2) continue;
      const ai = list.every((i) => i.generation);
      this.stacks.set(this.uuid(`stack:${g}`), { id: this.uuid(`stack:${g}`), kind: ai ? 'ai_batch' : 'near_duplicate', cover: list[0].id, members: list.map((i) => i.id), state: 'suggested' });
    }
  }

  // Ids made here look like the server's, which the app checks for.
  private uuid(seed: string): string {
    let h = 2166136261;
    const hex: string[] = [];
    for (let round = 0; round < 4; round++) {
      for (const c of `${round}:${seed}`) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
      hex.push(h.toString(16).padStart(8, '0'));
    }
    const s = hex.join('');
    return `${s.slice(0, 8)}-${s.slice(8, 12)}-4${s.slice(13, 16)}-a${s.slice(17, 20)}-${s.slice(20, 32)}`;
  }
  private newId(kind: string) {
    return this.uuid(`${kind}:${Date.now()}:${this.counter++}`);
  }

  // ---- what's where ----

  private local(i: DemoItem) {
    return i.takenAt.slice(0, 10);
  }
  private inVault(i: DemoItem) {
    return this.vault.has(i.id);
  }
  /** Hidden under another item of a confirmed stack. */
  private stackedAway(i: DemoItem) {
    for (const s of this.stacks.values()) if (s.state === 'confirmed' && s.members.includes(i.id) && s.cover !== i.id) return true;
    return false;
  }
  private stackSize(i: DemoItem) {
    for (const s of this.stacks.values()) if (s.state === 'confirmed' && s.cover === i.id) return s.members.filter((id) => !this.trashed.has(id)).length;
    return undefined;
  }
  private onTimeline(i: DemoItem) {
    return this.visible(i) && !i.archived && !this.stackedAway(i);
  }
  /** The viewer's own item: the only kind the server's edits (the trash, dates, labels, the vault, sharing) change. */
  private mine(i: DemoItem) {
    return i.owner === this.me.id;
  }
  /**
   * What the viewer can see, as the server's gate (visible_assets) has it: their own items outside
   * the trash and the vault, and another member's while it's in the household space and in its
   * owner's timeline. The gate's other way in, another member's item in an album shared with the
   * viewer (or put in one of the viewer's by someone else), doesn't arise: the demo's albums are
   * the viewer's own, filled by the viewer, so an item taken out of the household leaves them too.
   */
  private visible(i: DemoItem) {
    return !this.trashed.has(i.id) && !this.inVault(i) && (this.mine(i) || (Boolean(i.shared) && !i.archived));
  }
  /**
   * The viewer's people in an item. The server groups faces per owner, so a person is someone in
   * their owner's own photos: another member's shared photo has that member's people, which the
   * viewer doesn't see. The demo library lists the household's people once, so here they're the
   * viewer's, found in the viewer's own items.
   */
  private peopleIn(i: DemoItem): string[] {
    return i.owner === this.me.id ? i.people : [];
  }
  private all() {
    return [...this.items.values()].sort((a, b) => b.takenAt.localeCompare(a.takenAt));
  }
  /** Adult content: nudity or sex, labeled by its owner (the sample library has none of its own). */
  private adult(i: DemoItem) {
    return i.labels.includes('adult');
  }
  /** Left out everywhere but the Adult page: the viewer chose to hide adult content (hideAdult). */
  private hiddenAdult(i: DemoItem) {
    return this.me.settings?.adult === 'hide' && this.adult(i);
  }
  /** An item's size and ThumbHash: with its edit, once the edit's previews are made. */
  private look(i: DemoItem) {
    const e = this.edits.get(i.id);
    const [width, height] = e ? editedSize(e.recipe, i.width, i.height) : [i.width, i.height];
    return { width, height, thumbhash: e?.thumbhash ?? i.thumbhash };
  }

  private scope(q: URLSearchParams): Scope {
    return { type: q.get('type') ?? '', collection: q.get('collection') ?? '', folder: q.get('folder') ?? '', hideUtility: q.get('hideUtility') === 'true',
      library: q.get('library') ?? '', rating: Number(q.get('rating') ?? 0), flag: q.get('flag') ?? '', label: q.get('label') ?? '' };
  }

  private inScope(s: Scope): DemoItem[] | null {
    const [kind, ref] = s.collection.split(':');
    let list: DemoItem[];
    const all = this.all();
    switch (kind) {
      case '':
        list = all.filter((i) => this.onTimeline(i) && !(s.hideUtility && i.labels.length));
        if (s.library === 'mine') list = list.filter((i) => i.owner === this.me.id);
        if (s.library === 'household') list = list.filter((i) => i.shared);
        break;
      case 'favorites':
        list = all.filter((i) => i.favorite && this.visible(i) && !i.archived);
        break;
      case 'archive':
        list = all.filter((i) => i.archived && this.visible(i));
        break;
      case 'trash':
        list = all.filter((i) => this.trashed.has(i.id) && !this.inVault(i));
        break;
      case 'vault':
      case 'vault-deleted':
        if (!this.vaultOpen) return null;
        list = all.filter((i) => this.inVault(i) && this.trashed.has(i.id) === (kind === 'vault-deleted'));
        break;
      case 'ai':
        list = all.filter((i) => i.generation && this.onTimeline(i));
        break;
      case 'captured':
        list = all.filter((i) => i.camera && !i.generation && this.onTimeline(i));
        break;
      case 'screenshot':
      case 'document':
      case 'receipt':
      case 'id_card':
      case 'qr_code':
      case 'adult':
      case 'suggestive':
        list = all.filter((i) => i.labels.includes(kind) && this.visible(i));
        break;
      case 'place':
        list = all.filter((i) => i.placeId === ref && this.visible(i) && !i.archived);
        break;
      case 'album': {
        const a = this.albums.get(ref);
        if (!a) return null;
        list = all.filter((i) => this.visible(i) && (a.kind === 'smart' ? this.matchesRules(i, a.rules ?? {}) : i.albums.includes(ref)));
        break;
      }
      case 'person':
        list = all.filter((i) => this.peopleIn(i).includes(ref) && this.visible(i) && !i.archived);
        break;
      case 'character':
        list = all.filter((i) => i.characters?.includes(ref) && this.visible(i));
        break;
      case 'prompt': {
        const p = this.prompts().find((x) => x.id === ref);
        list = p ? all.filter((i) => this.visible(i) && i.generation?.prompt === p.text) : [];
        break;
      }
      case 'model':
      case 'lora': {
        const m = this.modelList().find((x) => x.id === ref);
        list = m ? all.filter((i) => this.visible(i) && (i.generation?.model === m.name || i.generation?.loras.some((l) => l.name === m.name))) : [];
        break;
      }
      case 'folder':
        // only the files directly in it, as on the server (the top's own are in '.')
        list = all.filter((i) => this.visible(i) && i.folderPath === (s.folder || '.'));
        break;
      default:
        return null;
    }
    if (kind !== 'adult') list = list.filter((i) => !this.hiddenAdult(i));
    if (s.type) list = list.filter((i) => i.type === s.type);
    if (s.rating) list = list.filter((i) => (i.rating ?? 0) >= s.rating);
    if (s.flag === 'unrejected') list = list.filter((i) => i.flag !== 'reject');
    else if (s.flag) list = list.filter((i) => i.flag === s.flag);
    if (s.label) list = list.filter((i) => i.label === s.label);
    return list;
  }

  private matchesRules(i: DemoItem, r: SmartRules) {
    if (r.type && i.type !== r.type) return false;
    if (r.favorite && !i.favorite) return false;
    if (r.rating && (i.rating ?? 0) < r.rating) return false;
    if (r.flag === 'unrejected' ? i.flag === 'reject' : r.flag && i.flag !== r.flag) return false;
    if (r.label && i.label !== r.label) return false;
    if (r.people?.length && !r.people.every((p) => this.peopleIn(i).includes(p))) return false;
    if (r.keywords?.length && !r.keywords.some((k) => i.keywords.includes(k))) return false;
    if (r.after && this.local(i) < r.after) return false;
    if (r.before && this.local(i) > r.before) return false;
    if (r.source === 'ai' && !i.generation) return false;
    if (r.source === 'not_ai' && i.generation) return false;
    if (r.source === 'camera' && (!i.camera || i.generation)) return false;
    return true;
  }

  private asset(i: DemoItem): TimelineAsset {
    const look = this.look(i);
    return {
      id: i.id, type: i.type, takenAt: i.takenAt, localDate: this.local(i), width: look.width, height: look.height, durationMs: i.durationMs,
      thumbhash: look.thumbhash, render: this.rendering.has(i.id) ? 'pending' : 'done', favorite: i.favorite || undefined, ai: i.generation ? i.generation.kind : undefined,
      shared: i.shared || undefined, fromOthers: (i.owner !== this.me.id && i.shared) || undefined, stackSize: this.stackSize(i),
      adult: this.adult(i) || undefined, rating: i.rating, flag: i.flag, label: i.label,
    };
  }
  private sample(i: DemoItem): Sample {
    return { id: i.id, thumbhash: this.look(i).thumbhash };
  }

  private buckets(list: DemoItem[]): Bucket[] {
    const m = new Map<string, number>();
    for (const i of list) m.set(this.local(i).slice(0, 7), (m.get(this.local(i).slice(0, 7)) ?? 0) + 1);
    return [...m].sort((a, b) => b[0].localeCompare(a[0])).map(([month, count]) => ({ month, count }));
  }

  private span(list: DemoItem[]) {
    const days = list.map((i) => this.local(i)).sort();
    return days.length ? { firstDate: days[0], lastDate: days[days.length - 1] } : {};
  }

  // ---- collections ----

  private album(id: string): Album | null {
    const a = this.albums.get(id);
    if (!a) return null;
    const list = this.inScope({ ...this.scope(new URLSearchParams()), collection: `album:${id}` }) ?? [];
    const cover = (a.cover && this.items.get(a.cover) && list.find((i) => i.id === a.cover)) || list[list.length - 1];
    const path: Ref[] = [];
    for (let p = a.parent; p; p = this.albums.get(p)?.parent) path.unshift({ id: p, name: this.albums.get(p)!.title });
    return {
      id, title: a.title, description: a.description, kind: a.kind, rules: a.rules, parent: a.parent, path: path.length ? path : undefined,
      children: [...this.albums.values()].filter((x) => x.parent === id).length || undefined, count: list.length, ...this.span(list),
      cover: cover?.id, coverThumbhash: cover && this.look(cover).thumbhash, role: 'owner', members: id === this.lib.shared.album ? this.sharedWith.length || undefined : undefined,
    };
  }

  private person(id: string): Person | null {
    const p = this.lib.people.find((x) => x.id === id);
    if (!p) return null;
    const list = this.all().filter((i) => this.peopleIn(i).includes(id) && this.visible(i) && !i.archived);
    const cover = list[0];
    return { id, name: this.names.get(id) ?? p.name, birthDate: p.birthDate, count: list.length, faceItems: list.length, origin: 'faces', kind: p.kind, species: p.species,
      hidden: this.hiddenPeople.has(id) || undefined, keptOutOfMemories: this.outOfMemories.has(id) || undefined, coverFace: p.face, cover: cover?.id, coverThumbhash: cover && this.look(cover).thumbhash };
  }

  private prompts(): Prompt[] {
    const by = new Map<string, DemoItem[]>();
    for (const i of this.all()) if (i.generation?.prompt && this.visible(i)) by.set(i.generation.prompt, [...(by.get(i.generation.prompt) ?? []), i]);
    return [...by].map(([text, list]) => ({
      id: this.uuid(`prompt:${text}`), text, uses: list.length, ...this.span(list), samples: list.slice(0, 6).map((i) => this.sample(i)),
      models: [...new Set(list.map((i) => i.generation!.model).filter((m): m is string => Boolean(m)))],
      loras: [...new Set(list.flatMap((i) => i.generation!.loras.map((l) => l.name)))],
    }));
  }

  private modelList(): AIModel[] {
    const by = new Map<string, { kind: 'model' | 'lora'; list: DemoItem[] }>();
    for (const i of this.all()) {
      if (!i.generation || !this.visible(i)) continue;
      if (i.generation.model) {
        const e = by.get(i.generation.model) ?? { kind: 'model' as const, list: [] };
        e.list.push(i);
        by.set(i.generation.model, e);
      }
      for (const l of i.generation.loras) {
        const e = by.get(l.name) ?? { kind: 'lora' as const, list: [] };
        e.list.push(i);
        by.set(l.name, e);
      }
    }
    return [...by].map(([name, { kind, list }]) => ({ id: this.uuid(`model:${name}`), kind, name, uses: list.length, ...this.span(list),
      samples: list.slice(0, 6).map((i) => this.sample(i)), with: list[0].generation?.tool }));
  }

  private character(id: string): Character | null {
    const c = this.lib.characters.find((x) => x.id === id);
    if (!c) return null;
    const list = this.all().filter((i) => i.characters?.includes(id) && this.visible(i));
    return { id, name: c.name, patterns: c.patterns, count: list.length, ...this.span(list), samples: list.slice(0, 6).map((i) => this.sample(i)),
      loras: [...new Set(list.flatMap((i) => i.generation?.loras.map((l) => l.name) ?? []))], faceGroups: 0 };
  }

  private counts(): Counts {
    const tl = this.all().filter((i) => this.onTimeline(i));
    const labels: Record<string, number> = {};
    for (const i of this.all()) if (this.visible(i)) for (const l of i.labels) labels[l] = (labels[l] ?? 0) + 1;
    const models = this.modelList();
    return {
      all: tl.length, images: tl.filter((i) => i.type === 'image').length, videos: tl.filter((i) => i.type === 'video').length,
      favorites: this.all().filter((i) => i.favorite && this.visible(i) && !i.archived).length, archived: this.all().filter((i) => i.archived && this.visible(i)).length,
      albums: this.albums.size, people: this.lib.people.filter((p) => !this.hiddenPeople.has(p.id)).length, prompts: this.prompts().length,
      models: models.length, characters: this.lib.characters.length, ai: tl.filter((i) => i.generation).length,
      captured: tl.filter((i) => i.camera && !i.generation).length, labels, trash: this.all().filter((i) => this.trashed.has(i.id) && !this.inVault(i)).length,
      lookAlikes: [...this.stacks.values()].filter((s) => s.state === 'suggested').length,
    };
  }

  private detail(i: DemoItem): AssetDetail {
    const g = i.generation;
    const prompt = g?.prompt ? this.prompts().find((p) => p.text === g.prompt) : undefined;
    const model = g?.model ? this.modelList().find((m) => m.name === g.model) : undefined;
    const owner = this.lib.members.find((m) => m.id === i.owner);
    return {
      ...this.asset(i),
      owner: i.owner !== this.me.id && owner ? { id: owner.id, name: owner.name } : undefined,
      mime: i.mime, offsetMin: i.offsetMin, dateSource: g ? 'xmp-created' : 'exif-original', sourceType: g ? (g.kind === 'edited' ? 'ai_edited' : 'ai_generated') : 'camera',
      fileName: i.fileName, folderPath: i.folderPath, library: 'Photostead', fileSize: i.fileSize, camera: i.camera, location: this.location(i),
      title: i.title, description: i.description, archived: i.archived || undefined,
      albums: i.albums.filter((a) => this.albums.has(a)).map((a) => ({ id: a, name: this.albums.get(a)!.title })),
      people: this.peopleIn(i).map((p) => ({ id: p, name: this.names.get(p) ?? '', kind: this.lib.people.find((x) => x.id === p)?.kind, source: 'faces' })),
      characters: (i.characters ?? []).map((c) => ({ id: c, name: this.lib.characters.find((x) => x.id === c)?.name ?? '',
        source: g?.settings?.reference_image ? 'lineage' : undefined })),
      generation: g ? { ...g, promptId: prompt?.id, promptUses: prompt?.uses, modelId: model?.id } : undefined,
      text: i.text, labels: i.labels, trashed: this.trashed.has(i.id) || undefined, trashedAt: this.trashed.get(i.id), keywords: i.keywords.length ? i.keywords : undefined,
      edited: this.edits.has(i.id) || undefined,
    };
  }

  /** Where an item was taken, with its place's name as the server writes it. */
  private location(i: DemoItem): AssetDetail['location'] {
    if (!i.location) return undefined;
    const p = i.placeId ? this.lib.places.find((x) => x.id === i.placeId) : undefined;
    return p ? { ...i.location, place: placeName(p.name, p.region, p.country), placeId: p.id } : { ...i.location };
  }

  // ---- places ----

  /** The towns the viewer's items were taken near, each with its newest item: by country, then most items first (ListPlaces). */
  private places(): Place[] {
    const by = new Map<string, DemoItem[]>();
    for (const i of this.all()) if (i.placeId && this.visible(i) && !i.archived && !this.hiddenAdult(i)) by.set(i.placeId, [...(by.get(i.placeId) ?? []), i]);
    return this.lib.places.filter((p) => by.has(p.id)).map((p): Place => {
      const list = by.get(p.id)!;
      return { id: p.id, name: p.name, region: p.region || undefined, country: p.country || undefined, countryCode: p.countryCode || undefined, lat: p.lat, lon: p.lon,
        count: list.length, cover: list[0].id, coverThumbhash: this.look(list[0]).thumbhash };
    }).sort((a, b) => cmp(a.country ?? '', b.country ?? '') || b.count - a.count || cmp(a.name, b.name));
  }

  /** Every item the viewer can see with a location, newest first (MapPoints). */
  private mapPoints(): MapPoint[] {
    return this.all().filter((i) => i.location && this.visible(i) && !i.archived && !this.hiddenAdult(i))
      .map((i) => ({ id: i.id, lat: i.location!.lat, lon: i.location!.lon, video: i.type === 'video' || undefined, thumbhash: this.look(i).thumbhash }));
  }

  // ---- photo edits ----

  /** A photo's edit as the server sends it: the recipe (or none), and its size before and with it. */
  private photoEdit(i: DemoItem): PhotoEdit {
    const e = this.edits.get(i.id);
    const [width, height] = e ? editedSize(e.recipe, i.width, i.height) : [i.width, i.height];
    return { recipe: e ? e.recipe : null, origWidth: i.width, origHeight: i.height, width, height, updatedAt: e?.updatedAt };
  }

  /** The demo's renderer has made an edit's previews: the item shows them from now on (unless it was edited again meanwhile). */
  rendered(id: string, recipe: Recipe, thumbhash: string): boolean {
    const e = this.edits.get(id);
    if (!e || JSON.stringify(e.recipe) !== JSON.stringify(recipe)) return false;
    e.thumbhash = thumbhash;
    this.rendering.delete(id);
    return true;
  }

  // ---- search and Ask ----

  /** Everything an item could be found by, as words, weighted: what's in it counts most. */
  private haystack(i: DemoItem) {
    const people = i.people.map((p) => this.names.get(p) ?? '');
    const albums = i.albums.map((a) => this.albums.get(a)?.title ?? '');
    const d = new Date(this.local(i) + 'T12:00:00');
    return {
      sees: new Set([...words(i.sees), ...words(i.place ?? ''), ...words(i.title ?? ''), ...people.flatMap(words), ...albums.flatMap(words), ...i.keywords.flatMap(words)].map(stem)),
      prompt: new Set(words(i.generation?.prompt ?? '').map(stem)),
      text: new Set(words(i.text ?? '').map(stem)),
      details: new Set([...words(i.description ?? ''), ...words(MONTHS[d.getMonth()]), String(d.getFullYear()), ...i.labels].map(stem)),
    };
  }

  private search(q: string, type: string, mode: string, similar: string): SearchResult {
    const base = this.all().filter((i) => this.onTimeline(i) && !this.hiddenAdult(i) && (!type || i.type === type));
    const meaning = { available: true, embedded: this.items.size, waiting: 0, textRead: this.items.size, textWaiting: 0 };
    if (similar) {
      const from = this.items.get(similar);
      if (!from) return { query: '', mode: 'similar', order: 'relevance', assets: [], truncated: false, meaning };
      const mine = this.haystack(from).sees;
      const scored = base.filter((i) => i.id !== similar).map((i) => {
        const s = this.haystack(i).sees;
        let n = 0;
        for (const w of mine) if (s.has(w) && !STOP.has(w)) n++;
        return { i, n: n + (i.group && i.group === from.group ? 50 : 0) + (i.albums.some((a) => from.albums.includes(a)) ? 3 : 0) };
      }).filter((x) => x.n >= 3).sort((a, b) => b.n - a.n).slice(0, 60);
      return { query: '', mode: 'similar', order: 'relevance', assets: scored.map((x) => this.asset(x.i)), truncated: false, meaning };
    }
    if (mode === 'names') {
      const ws = words(q);
      const hits = base.filter((i) => ws.every((w) => `${i.fileName} ${i.folderPath}`.toLowerCase().includes(w)));
      return { query: q, mode: 'names', order: 'date', assets: hits.map((i) => this.asset(i)), truncated: false, meaning };
    }
    const { ranked, found } = this.byMeaning(q, base);
    return { query: q, mode: 'meaning', order: 'relevance', assets: ranked.map((i) => this.asset(i)), truncated: false, meaning, found };
  }

  /** The items that look like q, closest first: the demo's stand-in for SigLIP. */
  private byMeaning(q: string, base: DemoItem[]) {
    const terms = words(q).filter((w) => !STOP.has(w)).map(stem);
    const found = { meaning: 0, prompts: 0, text: 0, details: 0, names: 0 };
    if (!terms.length) return { ranked: [], found };
    const scored: { i: DemoItem; n: number }[] = [];
    for (const i of base) {
      const h = this.haystack(i);
      let n = 0;
      let all = true;
      const by = { sees: false, prompt: false, text: false, details: false };
      for (const t of terms) {
        const alts = [t, ...(ALSO[t] ?? []).map(stem)];
        const hit = (set: Set<string>) => alts.some((a) => set.has(a));
        const s = hit(h.sees), p = hit(h.prompt), x = hit(h.text), d = hit(h.details);
        if (!(s || p || x || d)) all = false;
        n += (s ? 3 : 0) + (p ? 2 : 0) + (x ? 2 : 0) + (d ? 1 : 0);
        by.sees ||= s;
        by.prompt ||= p && !s;
        by.text ||= x && !s;
        by.details ||= d && !s;
      }
      if (!n || (!all && terms.length > 1 && n < terms.length * 3)) continue;
      scored.push({ i, n: n + (all ? 10 : 0) });
      if (by.sees) found.meaning++;
      if (by.prompt) found.prompts++;
      if (by.text) found.text++;
      if (by.details) found.details++;
    }
    scored.sort((a, b) => b.n - a.n || b.i.takenAt.localeCompare(a.i.takenAt));
    return { ranked: scored.map((x) => x.i), found };
  }

  // ---- Ask, run as the server's api/ask.go runs it ----

  /**
   * The names a question can mention: people, characters, and the models and LoRAs of the items
   * here. A model's id is the demo's own (made from its name) where the server's is its database
   * row's; a plan carries it (modelIds) only back to the same server, which reads its own ids.
   */
  private askVocabulary(): Vocabulary {
    return {
      people: this.lib.people.filter((p) => !this.hiddenPeople.has(p.id) && (this.names.get(p.id) ?? p.name) !== '').map((p) => ({ id: p.id, name: this.names.get(p.id) ?? p.name })),
      characters: this.lib.characters.map((c) => ({ id: c.id, name: c.name })),
      models: this.modelList().map((m) => ({ id: m.id, kind: m.kind, name: modelKey(m.name) })),
    };
  }

  /** The timeline's items a plan's filters find (its description aside), newest first or oldest first, and how many in all. */
  private askItems(p: Plan, oldestFirst: boolean, lim: number): { rows: DemoItem[]; total: number } {
    const people = p.people.map((r) => r.id);
    // A model named but in nothing here matches nothing.
    const models = p.model === '' ? null : new Set(this.modelList().filter((m) => p.modelIds.includes(m.id)).map((m) => m.name));
    const after = isDay(p.after) ? p.after : '', before = isDay(p.before) ? p.before : '';
    const list = [...this.items.values()].filter((i) => {
      const g = i.generation;
      return this.visible(i) && !i.archived && !this.hiddenAdult(i) &&
        (!p.type || i.type === p.type) &&
        (!after || this.local(i) >= after) &&
        (!before || this.local(i) < before) &&
        (!p.aiOnly || Boolean(g)) &&
        (!p.favorite || Boolean(i.favorite)) &&
        // Every one of them (a person named twice is never found twice), among the viewer's people.
        (!people.length || new Set(people.filter((id) => this.peopleIn(i).includes(id))).size === people.length) &&
        (!p.character || Boolean(i.characters?.includes(p.character.id))) &&
        (!models || Boolean(g && ((g.model && models.has(g.model)) || g.loras.some((l) => models.has(l.name))))) &&
        (!p.source || Boolean(g && (g.source === p.source || (g.tool !== undefined && ilike(g.tool, '%' + p.source + '%'))))) &&
        (!p.prompt || Boolean(g?.prompt && textSearch('english', g.prompt, p.prompt))) &&
        (!p.ocr || Boolean(i.text && textSearch('simple', i.text, p.ocr))) &&
        (!p.filename || ilike(this.relPath(i), '%' + p.filename.replace(/[\\%_]/g, '\\$&') + '%')) &&
        (!p.label || i.labels.includes(p.label));
    });
    const cmp = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);
    const at = (i: DemoItem) => Date.parse(i.takenAt);
    list.sort((a, b) => (oldestFirst ? cmp(this.local(a), this.local(b)) || at(a) - at(b) : 0) ||
      cmp(this.local(b), this.local(a)) || at(b) - at(a) || cmp(b.id, a.id));
    return { rows: list.slice(0, lim), total: list.length };
  }

  /** An item's file, as the server's files table has it: its path in the library. */
  private relPath(i: DemoItem) {
    return i.folderPath === '' || i.folderPath === '.' ? i.fileName : `${i.folderPath}/${i.fileName}`;
  }

  /**
   * An item as Ask lists it: the server's rows for it don't say what's shared or stacked. Its
   * takenAt keeps the item's own offset, as everywhere in the demo, where the server writes the
   * same instant in its process's zone; the app shows times from an item's detail (offsetMin).
   */
  private askAsset(i: DemoItem): TimelineAsset {
    return { ...this.asset(i), shared: undefined, fromOthers: undefined, stackSize: undefined };
  }

  /** Answers a plan: by date when it's all filters; ranked by likeness when it describes what the items look like. */
  private runPlan(p: Plan): { r: Result; items: DemoItem[]; truncated: boolean; notes: string[] } {
    const notes: string[] = [];
    const r: Result = { total: 0, exact: true, order: 'date', newest: '', oldest: '', alt: null };
    if (p.text !== '') {
      // Here the demo can't do as the server does: the server ranks by meaning with SigLIP's
      // embeddings, which the demo doesn't have, so its word search (byMeaning) stands in and the
      // order can differ. The items and their count are the server's: SigLIP ranks every item the
      // filters found (AskItems, then AskRank), or for a description alone every item (nearest,
      // which takes no filters at all, so dates a plan sent back has without saying when don't
      // count), and those the demo's search doesn't match come after, newest first. Only past
      // MEANING_LIMIT candidates would the items differ (which are closest is the ranking's), and
      // the sample library has fewer. And search by meaning is never unavailable here.
      r.exact = false;
      r.order = 'relevance';
      const among = structured(p) ? this.askItems(p, false, ASK_RANKING).rows : this.askItems({ ...p, after: '', before: '' }, false, Infinity).rows;
      const ranked = this.byMeaning(p.text, among).ranked;
      const hit = new Set(ranked);
      const items = [...ranked, ...among.filter((i) => !hit.has(i))].slice(0, MEANING_LIMIT);
      r.total = items.length;
      for (const i of items) {
        if (r.newest === '' || this.local(i) > r.newest) r.newest = this.local(i);
        if (r.oldest === '' || this.local(i) < r.oldest) r.oldest = this.local(i);
      }
      return { r, items, truncated: false, notes };
    }
    const { rows, total } = this.askItems(p, p.intent === 'first', ASK_LIMIT);
    if (rows.length === 0) return { r, items: [], truncated: false, notes };
    r.total = total;
    const truncated = r.total > rows.length;
    const first = this.local(rows[0]), last = this.local(rows[rows.length - 1]);
    if (p.intent === 'first') {
      r.oldest = first;
      if (!truncated) r.newest = last;
    } else {
      r.newest = first;
      if (!truncated) r.oldest = last;
    }
    return { r, items: rows, truncated, notes };
  }

  /** Finds the one filter whose removal brings back the most, so an empty answer can offer it. */
  private relax(p: Plan): Alt | null {
    let best: Alt | null = null;
    for (const k of relaxable(p)) {
      const q = without(p, k);
      if (k !== 'text') q.text = ''; // a description ranks rather than filters; count what the filters find
      if (!structured(q)) continue;
      const { total } = this.askItems(q, false, 1);
      if (total > 0 && (best === null || total > best.count)) best = { remove: k, count: total, describe: describeN(q, total) };
    }
    return best;
  }

  /** Ask: the question is planned into filters (ask.ts) and run against the library, as the server does. */
  private ask(body: unknown): DemoResponse {
    const req = askRequest(body); // what the server's API library refuses, the demo does
    if (!req) return problem(422, 'validation failed');
    const q = trimSpace(req.q);
    const { prev, exclude } = req;
    if (q === '' && !prev) return problem(422, 'Ask something.');
    const vocab = this.askVocabulary();
    const now = nowIn(this.me.timeZone);
    let plan: Plan;
    if (q === '') plan = prev!; // only dropping filters
    else if (prev) plan = followUp(prev, parse(q, now, vocab), q);
    else plan = parse(q, now, vocab);
    plan = without(plan, ...exclude);

    const { r, items, truncated, notes } = this.runPlan(plan);
    if (r.total === 0) r.alt = this.relax(plan);
    const out: AskResult = {
      plan: planJSON(plan), chips: chips(plan), answer: answer(plan, r), describe: describe(plan), assets: items.map((i) => this.askAsset(i)),
      total: r.total, exact: r.exact, order: r.order as AskResult['order'], truncated, notes, alt: r.alt ?? undefined,
    };
    return ok(out);
  }

  // ---- memories ----

  private memories(): Memory[] {
    const today = new Date();
    const md = (d: Date) => `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    const hidden = (i: DemoItem) => this.hiddenDays.some((h) => this.local(i) >= h.from && this.local(i) <= h.to);
    const pool = this.all().filter((i) => this.onTimeline(i) && !i.labels.length && !i.generation && !hidden(i) && !this.peopleIn(i).some((p) => this.outOfMemories.has(p)));
    const yearsAgo = (n: number) => (n === 1 ? 'A year ago' : `${n} years ago`);
    const out: Memory[] = [];
    // On this day (or the week around it) in each earlier year; the demo also reaches back to the
    // nearest outing in the season, so there's always something to show whatever day it is.
    for (let y = today.getFullYear() - 1; y >= today.getFullYear() - 3; y--) {
      const day = new Date(y, today.getMonth(), today.getDate());
      const near = (i: DemoItem, around: number) => Math.abs(new Date(this.local(i) + 'T12:00:00').getTime() - day.getTime()) <= around * 86400000;
      let around = 0;
      let list = pool.filter((i) => this.local(i) === `${y}-${md(today)}`);
      if (!list.length) list = pool.filter((i) => near(i, (around = 3)));
      if (!list.length) {
        const nearest = pool.filter((i) => near(i, 45)).sort((a, b) => Math.abs(new Date(this.local(a)).getTime() - day.getTime()) - Math.abs(new Date(this.local(b)).getTime() - day.getTime()))[0];
        if (nearest) {
          list = pool.filter((i) => this.local(i) === this.local(nearest));
          const d = new Date(this.local(nearest) + 'T12:00:00');
          const n = today.getFullYear() - y;
          out.push({ key: `day:${this.local(nearest)}`, kind: 'on_this_day', title: yearsAgo(n),
            subtitle: d.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' }), from: this.local(nearest), to: this.local(nearest), count: list.length, assets: list.slice(0, 12).map((i) => this.asset(i)) });
        }
        continue;
      }
      const n = today.getFullYear() - y;
      const sub = day.toLocaleDateString('en-US', { month: 'long', day: 'numeric', year: 'numeric' });
      out.push({ key: `day:${y}-${md(today)}`, kind: 'on_this_day', title: yearsAgo(n), subtitle: around ? `The week of ${sub}` : sub,
        from: this.local(list[list.length - 1]), to: this.local(list[0]), count: list.length, assets: list.slice(0, 12).map((i) => this.asset(i)) });
    }
    const people = this.lib.people.filter((p) => !this.hiddenPeople.has(p.id) && !this.outOfMemories.has(p.id));
    if (people.length) {
      const doy = Math.floor((today.getTime() - new Date(today.getFullYear(), 0, 0).getTime()) / 86400000);
      const p = people[doy % people.length];
      const list = pool.filter((i) => this.peopleIn(i).includes(p.id)).reverse();
      if (list.length) {
        const step = Math.max(1, Math.floor(list.length / 12));
        const first = this.local(list[0]), last = this.local(list[list.length - 1]);
        const name = this.names.get(p.id) ?? p.name;
        const sameYear = first.slice(0, 4) === last.slice(0, 4);
        const month = (day: string, year: boolean) => new Date(day + 'T12:00:00Z').toLocaleDateString('en-US', { month: 'long', year: year ? 'numeric' : undefined, timeZone: 'UTC' });
        out.push({ key: `person:${p.id}`, kind: 'person', title: sameYear ? `With ${name}` : `${name} over the years`,
          subtitle: sameYear ? `${month(first, false)} – ${month(last, true)}` : `${first.slice(0, 4)} – ${last.slice(0, 4)}`, from: first, to: last, person: { id: p.id, name, kind: p.kind, species: p.species },
          count: list.length, assets: list.filter((_, n) => n % step === 0).slice(0, 12).map((i) => this.asset(i)) });
      }
    }
    return out;
  }

  // ---- the household ----

  private household(): Household {
    const shared = this.all().filter((i) => i.shared && this.visible(i));
    return {
      id: this.lib.household.id, name: this.householdName, role: 'owner', canManage: true, shared: shared.length,
      members: this.lib.members.map((m, n) => ({ id: m.id, name: m.name, email: m.email, role: m.role, joinedAt: m.joinedAt, shared: shared.filter((i) => i.owner === m.id).length, me: n === 0 || undefined })),
      invites: [],
      // The viewer is the server's admin: every guest, by personal link (no address), none signed in here.
      guests: this.guests.map((g) => ({ id: g.id, name: g.name, addedBy: this.memberName(g.addedBy) || undefined, addedAt: g.addedAt,
        albums: this.sharedWith.filter((s) => s.member === g.id).length, devices: 0 })),
      rule: { enabled: true, people: this.lib.people.filter((p) => p.kind === 'human').map((p) => p.id) },
    };
  }

  /** A guest goes (DELETE /api/v1/household/guests/{id}), with what's theirs: the album shared
   *  with them, their comments and their picks. The album and its photos stay. */
  private removeGuest(id: string): DemoResponse {
    const member = this.lib.members.find((x) => x.id === id);
    if (member) return problem(409, `${member.name} is in the household, not a guest. Only guests can be removed.`);
    if (!this.guests.some((g) => g.id === id)) return problem(404, 'No such guest.');
    this.guests = this.guests.filter((g) => g.id !== id);
    this.sharedWith = this.sharedWith.filter((s) => s.member !== id);
    this.comments = this.comments.filter((c) => c.member !== id);
    this.picks = this.picks.filter((p) => p.member !== id);
    return ok();
  }

  private vaultState(): VaultState {
    const inside = this.all().filter((i) => this.inVault(i));
    return { hasPin: Boolean(this.vaultPin), unlocked: this.vaultOpen, items: this.vaultOpen ? inside.filter((i) => !this.trashed.has(i.id)).length : undefined,
      deleted: this.vaultOpen ? inside.filter((i) => this.trashed.has(i.id)).length : undefined };
  }

  // ---- requests ----

  handle(r: DemoRequest): DemoResponse {
    const p = r.path.replace(/\/+$/, '');
    const b = r.body ?? {}; // a body of null is JSON too
    const ids = [...new Set((Array.isArray(b.assetIds) ? b.assetIds : []).filter((id): id is string => typeof id === 'string' && this.items.has(id)))];
    // As the server's edits: each changes only the items it may, and says how many it changed. Most
    // take only the viewer's own items (another member's shared photo is theirs to trash, date,
    // label or put in a vault); favorites and marks, which are the viewer's own, any they can see.
    const own = ids.map((id) => this.items.get(id)!).filter((i) => this.mine(i));
    const seen = ids.map((id) => this.items.get(id)!).filter((i) => this.visible(i));
    const change = (list: DemoItem[], fn: (i: DemoItem) => void) => {
      list.forEach(fn);
      return ok({ changed: list.length });
    };
    let m: RegExpMatchArray | null;
    const route = (method: string, re: RegExp) => r.method === method && (m = p.match(re)) !== null;

    // Signing in: the demo is signed in as the library's owner.
    if (route('GET', /^\/api\/v1\/setup$/)) return ok({ needed: false, serverZone: this.lib.timeZone });
    if (route('GET', /^\/api\/v1\/auth\/me$/)) return ok(this.me);
    if (route('POST', /^\/api\/v1\/auth\/login$/)) return ok({ user: this.me });
    if (route('POST', /^\/api\/v1\/auth\/logout$/)) return ok();
    if (route('PATCH', /^\/api\/v1\/auth\/me\/settings$/)) {
      this.me = { ...this.me, settings: { ...this.me.settings, ...(b as object) } };
      return ok(this.me);
    }
    if (route('PUT', /^\/api\/v1\/auth\/me\/time-zone$/)) {
      // As auth.Zone checks it: a zone Go can load, by its tz database name.
      const zone = b.timeZone;
      if (typeof zone !== 'string' || zone === '' || [...zone].length > 64) return problem(422, 'validation failed');
      const name = trimSpace(zone);
      if (name === '' || name === 'Local') return problem(422, 'choose a time zone, for example America/Los_Angeles');
      if (loadLocation(name) === null) return problem(422, `${JSON.stringify(name)} isn't a time zone Photostead knows`);
      this.me = { ...this.me, timeZone: zone };
      return ok({ user: this.me, cameraClocksMoved: 0, daysChanged: 0 });
    }
    if (route('GET', /^\/api\/v1\/auth\/security$/)) return ok({ hasPassword: true, twoStep: false, recoveryLeft: 0, passkeys: [], signOnLinks: [] });
    if (route('GET', /^\/api\/v1\/auth\/sessions$/))
      return ok({ sessions: [{ id: this.uuid('session'), current: true, userAgent: navigator.userAgent, createdAt: new Date().toISOString(), lastSeenAt: new Date().toISOString() }] });
    // Pairing codes that last: made, listed and revoked as the server does, for the household's
    // members. No device can pair with one: that needs a real server.
    if (route('GET', /^\/api\/v1\/devices\/pairing\/lasting$/)) return ok({ codes: this.lastingCodes.filter((c) => c.expiresAt > new Date().toISOString()) });
    if (route('POST', /^\/api\/v1\/devices\/pairing\/lasting$/)) {
      const days = b.days ?? 14, devices = b.devices ?? 5, note = b.note ?? '';
      if (typeof days !== 'number' || typeof devices !== 'number' || !Number.isInteger(days) || !Number.isInteger(devices) || days < 1 || days > 90 || devices < 1 || devices > 50 ||
        typeof note !== 'string' || [...note].length > 60)
        return problem(422, 'validation failed');
      const member = this.lib.members.find((x) => x.id === (b.userId ?? this.me.id));
      if (!member) return problem(404, 'No such account.');
      const now = Date.now(), code = pairCode();
      const lasting: LastingCode = { id: this.newId('pairing'), userId: member.id, name: member.name, email: member.email, devices: 0, maxDevices: devices,
        note: note.split(/\s+/).filter(Boolean).join(' '), // on one line, as the server keeps it
        createdAt: new Date(now).toISOString(), expiresAt: new Date(now + days * 86400000).toISOString() };
      this.lastingCodes.unshift(lasting);
      const link = `photostead://pair?${new URLSearchParams([['code', code], ['server', this.uuid('server')], ['url', TAILNET], ['url', 'http://192.168.1.20:4800']])}`;
      return ok({ code, expiresAt: lasting.expiresAt, link, qr: qrImage(link), lasting });
    }
    if (route('DELETE', /^\/api\/v1\/devices\/pairing\/lasting\/([0-9a-f-]{36})$/)) {
      const n = this.lastingCodes.length;
      this.lastingCodes = this.lastingCodes.filter((c) => c.id !== m![1]);
      return this.lastingCodes.length === n ? problem(404, 'No such code.') : ok();
    }
    if (route('GET', /^\/api\/v1\/status$/))
      return ok({ indexing: 0, search: { available: true, embedded: this.items.size, waiting: 0, textRead: this.items.size, textWaiting: 0 } });
    if (route('GET', /^\/api\/v1\/imports\/immich$/)) return ok({ connected: false });
    if (route('GET', /^\/api\/v1\/remote$/))
      return ok({ available: true, tailscale: { status: 'running', hostname: 'photostead', url: TAILNET, funnel: false, devices: true }, addresses: { home: 'http://192.168.1.20:4800', tailnet: TAILNET } });
    if (route('GET', /^\/api\/v1\/server\/health$/)) { // a server in good shape
      const last = new Date(new Date().setHours(3, 30, 0, 0) - (new Date().getHours() < 4 ? 86400000 : 0));
      return ok({ checks: [
        { id: 'share', state: 'ok', title: 'Photo share', detail: 'Uploads and phone backups go to /photos/library.' },
        { id: 'space', state: 'ok', title: 'Disk space', detail: '412 GB free for the database and thumbnails (/data); 6.8 TB for originals (/photos).' },
        { id: 'memory', state: 'ok', title: 'Memory', detail: '32 GB.' },
        { id: 'folders', state: 'ok', title: 'Your folders', detail: '1 folder indexed in place, read-only, and watched for new files.' },
        { id: 'ai', state: 'ok', title: 'Search, faces and text', detail: 'All caught up.' },
        { id: 'backups', state: 'ok', title: 'Backups', detail: `Last copy of the database: ${last.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}, 03:30.` },
        { id: 'remote', state: 'ok', title: 'Reaching it from away', detail: `At ${TAILNET}, on your tailnet.` },
      ] });
    }
    if (route('GET', /^\/api\/v1\/server\/backups$/)) {
      const at = (d: number) => new Date(Date.now() - d * 86400000).toISOString().slice(0, 10);
      const copies = [0, 1, 2, 3, 4, 5, 6].map((d) => ({ name: `photostead-${at(d)}.dump`, bytes: 48_000_000 - d * 120_000, at: `${at(d)}T03:00:00Z` }));
      // Every original checked against its hash: a thirtieth of them each night.
      const n = this.items.size, bytes = [...this.items.values()].reduce((s, i) => s + i.fileSize, 0);
      const out: Backups = {
        on: true, dir: '/mnt/user/photostead/backups', keep: 14, status: { last: copies[0], seconds: 41 }, copies, running: false, nextAt: `${at(-1)}T03:00:00Z`,
        files: { files: n, checked: n, damaged: [], running: false, nextAt: `${at(-1)}T04:00:00Z`,
          last: { at: `${at(0)}T04:00:00Z`, seconds: 6, checked: Math.ceil(n / 30), bytes: Math.round(bytes / 30), damaged: 0, missing: 0, changed: 0, finished: true } },
      };
      return ok(out);
    }

    // The library.
    if (route('GET', /^\/api\/v1\/library\/counts$/)) return ok(this.counts());
    if (route('GET', /^\/api\/v1\/timeline\/buckets$/)) {
      const list = this.inScope(this.scope(r.query));
      if (!list) return problem(this.scope(r.query).collection.startsWith('vault') ? 403 : 404, 'Not found');
      return ok({ total: list.length, buckets: this.buckets(list) });
    }
    if (route('GET', /^\/api\/v1\/timeline$/)) {
      const list = this.inScope(this.scope(r.query));
      if (!list) return problem(404, 'Not found');
      const month = r.query.get('month') ?? '';
      return ok({ month, assets: list.filter((i) => this.local(i).startsWith(month)).map((i) => this.asset(i)) });
    }
    if (route('GET', /^\/api\/v1\/timeline\/recent$/)) {
      // What the timeline shows, newest-arrived first, a page at a time; the cursor is the last one's arrival and id.
      const type = r.query.get('type') ?? '', limit = Number(r.query.get('limit') || 500), cursor = r.query.get('cursor') ?? '';
      if (!Number.isInteger(limit) || limit < 1 || limit > 2000) return problem(422, 'validation failed');
      let list = this.all().filter((i) => this.onTimeline(i) && !this.hiddenAdult(i) && (!type || i.type === type)).sort((a, b) => cmp(b.addedAt, a.addedAt) || cmp(b.id, a.id));
      if (cursor) {
        const [at, id] = cursor.split('~');
        if (!at || !id) return problem(422, 'cursor isn’t one Photostead gave');
        list = list.filter((i) => i.addedAt < at || (i.addedAt === at && i.id < id));
      }
      const page = list.slice(0, limit), last = page[page.length - 1];
      const assets: RecentAsset[] = page.map((i) => ({ ...this.asset(i), addedAt: i.addedAt }));
      return ok({ assets, next: list.length > limit ? `${last.addedAt}~${last.id}` : undefined });
    }

    // Places: named offline, as a list by country. The map isn't in the demo: its tiles come from a
    // map service (OpenFreeMap), and photostead.com contacts no one else.
    if (route('GET', /^\/api\/v1\/places$/)) return ok({ places: this.places(), attribution: 'Places from GeoNames (CC BY 4.0)' });
    if (route('GET', /^\/api\/v1\/map$/)) return ok({ points: this.mapPoints() });

    // Editing a photo: the recipe is kept, and the demo's renderer (index.tsx) makes the previews
    // with it in the browser, as the server makes them.
    if ((m = p.match(/^\/api\/v1\/assets\/([0-9a-f-]{36})\/edit$/)) !== null && ['GET', 'PUT', 'DELETE'].includes(r.method)) {
      const i = this.items.get(m[1]);
      if (!i || !(this.visible(i) || (this.mine(i) && this.vaultOpen && this.inVault(i)))) return problem(404, 'No such item.');
      if (r.method === 'GET') return ok(this.photoEdit(i));
      if (!this.mine(i)) return problem(403, 'Only its owner can edit this photo.');
      if (i.type !== 'image') return problem(422, 'Only photos can be edited.');
      const rec = b as unknown as Recipe;
      if (r.method === 'PUT') {
        const why = typeof rec.rotate !== 'number' ? 'validation failed' : recipeProblem(rec);
        if (why) return problem(422, why);
      }
      if (r.method === 'DELETE' || emptyRecipe(rec)) {
        this.edits.delete(i.id);
        this.rendering.delete(i.id);
      } else {
        const keep: Recipe = { rotate: rec.rotate, flip: rec.flip || undefined, crop: rec.crop, exposure: rec.exposure || undefined, contrast: rec.contrast || undefined,
          saturation: rec.saturation || undefined, warmth: rec.warmth || undefined };
        this.edits.set(i.id, { recipe: JSON.parse(JSON.stringify(keep)), updatedAt: new Date().toISOString() });
        this.rendering.add(i.id);
      }
      return ok(this.photoEdit(i));
    }
    if (route('GET', /^\/api\/v1\/assets\/([0-9a-f-]{36})$/)) {
      // One the viewer can see, or one in their own trash, or in their vault while it's unlocked.
      const i = this.items.get(m![1]);
      const shown = i && (this.visible(i) || (this.mine(i) && (this.inVault(i) ? this.vaultOpen : this.trashed.has(i.id))));
      return shown ? ok(this.detail(i)) : problem(404, 'No such item.');
    }
    if (route('GET', /^\/api\/v1\/assets\/([0-9a-f-]{36})\/stack$/)) {
      const s = [...this.stacks.values()].find((x) => x.members.includes(m![1]));
      return s ? ok(this.stack(s)) : problem(404, 'Not in a stack');
    }
    if (route('POST', /^\/api\/v1\/assets\/favorite$/)) return change(seen, (i) => void (i.favorite = Boolean(b.favorite)));
    if (route('POST', /^\/api\/v1\/assets\/trash$/))
      return change(own.filter((i) => !this.trashed.has(i.id)), (i) => void this.trashed.set(i.id, new Date().toISOString()));
    if (route('POST', /^\/api\/v1\/assets\/restore$/)) return change(own.filter((i) => this.trashed.has(i.id)), (i) => void this.trashed.delete(i.id));
    if (route('POST', /^\/api\/v1\/trash\/empty$/)) {
      const gone = [...this.trashed.keys()].filter((id) => !this.inVault(this.items.get(id)!) && (!ids.length || ids.includes(id)));
      gone.forEach((id) => {
        this.items.delete(id);
        this.trashed.delete(id);
      });
      return ok({ deleted: gone.length, kept: 0 });
    }
    if (route('POST', /^\/api\/v1\/assets\/marks$/)) {
      if (b.rating === undefined && b.flag === undefined && b.label === undefined) return problem(422, 'Give a rating, a flag or a label.');
      return change(seen, (i) => {
        if (typeof b.rating === 'number') i.rating = b.rating || undefined;
        if (b.flag) i.flag = b.flag === 'none' ? undefined : (b.flag as DemoItem['flag']);
        if (b.label) i.label = b.label === 'none' ? undefined : (b.label as DemoItem['label']);
      });
    }
    if (route('POST', /^\/api\/v1\/assets\/description$/)) return change(own, (i) => void (i.description = String(b.text ?? '').trim() || undefined));
    if (route('POST', /^\/api\/v1\/assets\/keywords$/)) {
      const add = (b.add as string[]) ?? [], remove = (b.remove as string[]) ?? [];
      let added = 0, removed = 0;
      for (const i of own) {
        for (const k of add) if (!i.keywords.includes(k)) (i.keywords.push(k), added++);
        for (const k of remove) if (i.keywords.includes(k)) (i.keywords = i.keywords.filter((x) => x !== k), removed++);
      }
      return ok({ added, removed });
    }
    if (route('POST', /^\/api\/v1\/assets\/keywords\/list$/)) return ok({ keywords: this.keywords(own) });
    if (route('GET', /^\/api\/v1\/keywords$/)) return ok({ keywords: this.keywords(this.all().filter((i) => this.visible(i))) });
    if (route('POST', /^\/api\/v1\/assets\/date$/)) {
      let shift = Number(b.shiftSeconds ?? 0);
      if (typeof b.setEarliest === 'string' && b.setEarliest !== '') {
        // As the server: the earliest of them, by its clock when it was taken, moves to this time.
        const want = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2})?$/.test(b.setEarliest) ? Date.parse(b.setEarliest + 'Z') : NaN;
        if (Number.isNaN(want)) return problem(422, 'setEarliest must look like 2024-03-05T14:30');
        const first = [...own].sort((x, y) => Date.parse(x.takenAt) - Date.parse(y.takenAt))[0];
        if (!first) return problem(404, 'None of those are yours.');
        shift = Math.round((want - (Date.parse(first.takenAt) + first.offsetMin * 60000)) / 1000);
      }
      if (shift === 0) return ok({ changed: 0, shiftSeconds: 0 });
      // The viewer's own, outside the trash (ShiftDates); the day each falls on follows.
      const moved = own.filter((i) => !this.trashed.has(i.id));
      for (const i of moved) {
        const t = new Date(new Date(i.takenAt).getTime() + shift * 1000 + i.offsetMin * 60000);
        const off = `${i.offsetMin < 0 ? '-' : '+'}${String(Math.floor(Math.abs(i.offsetMin) / 60)).padStart(2, '0')}:${String(Math.abs(i.offsetMin) % 60).padStart(2, '0')}`;
        i.takenAt = t.toISOString().slice(0, 19) + off;
      }
      return ok({ changed: moved.length, shiftSeconds: shift });
    }
    if (route('POST', /^\/api\/v1\/labels\/([a-z_]+)\/(add|remove)$/)) {
      // Adult, so not merely suggestive (ClearSuggestive).
      const other = m![1] === 'adult' ? 'suggestive' : '';
      return change(own, (i) => void (i.labels = m![2] === 'add' ? [...new Set([...i.labels.filter((l) => l !== other), m![1]])] : i.labels.filter((l) => l !== m![1])));
    }

    // Albums.
    if (route('GET', /^\/api\/v1\/albums$/)) {
      const parent = r.query.get('parent');
      const list = [...this.albums.values()].filter((a) => (r.query.get('all') === 'true' ? true : parent ? a.parent === parent : !a.parent));
      return ok({ albums: list.map((a) => this.album(a.id)!).sort((x, y) => (y.lastDate ?? '').localeCompare(x.lastDate ?? '')) });
    }
    if (route('GET', /^\/api\/v1\/albums\/([0-9a-f-]{36})$/)) return this.album(m![1]) ? ok(this.album(m![1])) : problem(404, 'That album isn’t here anymore.');
    if (route('POST', /^\/api\/v1\/albums$/)) {
      const id = this.newId('album');
      this.albums.set(id, { id, title: String(b.title || 'Untitled'), parent: (b.parent as string) || undefined, kind: b.rules ? 'smart' : 'manual', rules: b.rules as SmartRules | undefined });
      for (const a of ids) this.items.get(a)!.albums.push(id);
      return ok(this.album(id));
    }
    if (route('PATCH', /^\/api\/v1\/albums\/([0-9a-f-]{36})$/)) {
      const a = this.albums.get(m![1]);
      if (!a) return problem(404, 'Not found');
      if (typeof b.title === 'string') a.title = b.title;
      if (typeof b.description === 'string') a.description = b.description || undefined;
      if (b.rules) a.rules = b.rules as SmartRules;
      return ok(this.album(a.id));
    }
    if (route('DELETE', /^\/api\/v1\/albums\/([0-9a-f-]{36})$/)) {
      const id = m![1];
      for (const a of this.albums.values()) if (a.parent === id) a.parent = this.albums.get(id)?.parent;
      this.albums.delete(id);
      for (const i of this.items.values()) i.albums = i.albums.filter((a) => a !== id);
      return ok();
    }
    if (route('POST', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/(add|remove)$/)) {
      const id = m![1];
      if (!this.albums.has(id)) return problem(404, 'Not found');
      for (const a of ids) {
        const i = this.items.get(a)!;
        if (m![2] === 'add' && !i.albums.includes(id)) this.putIn.set(`${id}:${a}`, new Date().toISOString().replace(/\.\d+Z$/, 'Z'));
        i.albums = m![2] === 'add' ? [...new Set([...i.albums, id])] : i.albums.filter((x) => x !== id);
      }
      return ok(this.album(id));
    }
    if (route('POST', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/move$/)) {
      const a = this.albums.get(m![1]);
      if (!a) return problem(404, 'Not found');
      a.parent = (b.parent as string) || undefined;
      return ok(this.album(a.id));
    }
    if (route('GET', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/people$/)) {
      // One album is shared with the rest of the household (library.shared), and its guests; the
      // others could be, with the household and those guests.
      const others = [
        ...this.lib.members.slice(1).map((o) => ({ id: o.id, name: o.name, email: o.email })),
        ...this.guests.filter((g) => this.sharedWith.some((s) => s.member === g.id)).map((g) => ({ id: g.id, name: g.name, email: '', guest: true })),
      ];
      const shared = m![1] === this.lib.shared.album ? this.sharedWith : [];
      return ok({
        members: shared.map((s) => ({ ...others.find((o) => o.id === s.member)!, role: s.role })),
        candidates: others.filter((o) => !shared.some((s) => s.member === o.id)), invites: [],
      });
    }

    // Share links: the address (on the tailnet, as Funnel is off), its QR code and a card to print.
    // Nobody can open one, as nothing here is served to anyone else.
    if (route('GET', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/links$/)) {
      if (!this.albums.has(m![1])) return problem(404, 'No such album.');
      return ok({ links: [...this.links.values()].filter((l) => l.album === m![1]).map((l) => this.shareLink(l)) });
    }
    if (route('POST', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/links$/)) {
      if (!this.albums.has(m![1])) return problem(404, 'No such album.');
      const l: DemoLink = { id: this.newId('link'), album: m![1], token: linkToken(), hasPassword: Boolean(b.password), expiresAt: (b.expiresAt as string) || undefined,
        allowDownload: Boolean(b.allowDownload), allowUpload: Boolean(b.allowUpload), createdAt: new Date().toISOString() };
      this.links.set(l.id, l);
      return { status: 201, body: this.shareLink(l) };
    }
    if (route('PATCH', /^\/api\/v1\/links\/([0-9a-f-]{36})$/)) {
      const l = this.links.get(m![1]);
      if (!l) return problem(404, 'No such link.');
      if (b.password || b.clearPassword) l.hasPassword = Boolean(b.password);
      Object.assign(l, { expiresAt: (b.expiresAt as string) || undefined, allowDownload: Boolean(b.allowDownload), allowUpload: Boolean(b.allowUpload) });
      return ok(this.shareLink(l));
    }
    if (route('DELETE', /^\/api\/v1\/links\/([0-9a-f-]{36})$/)) return this.links.delete(m![1]) ? ok() : problem(404, 'No such link.');

    // Talking about the shared album, and picking from it: the viewer is its owner, who sees
    // everyone's picks and may take back any comment.
    if (route('GET', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/activity$/)) {
      if (!this.albums.has(m![1])) return problem(404, 'No such album.');
      return ok({ activity: this.activity(m![1]) });
    }
    if (route('GET', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/comments$/)) {
      if (!this.albums.has(m![1])) return problem(404, 'No such album.');
      const about = r.query.get('asset');
      if (about && !/^[0-9a-f-]{36}$/.test(about)) return problem(422, 'asset isn’t an id.');
      const list = this.comments.filter((c) => m![1] === this.lib.shared.album && (!about || c.assetId === about)).sort((x, y) => cmp(x.at, y.at));
      return ok({ comments: list.map((c) => this.comment(c)) });
    }
    if (route('POST', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/comments$/)) {
      if (!this.albums.has(m![1])) return problem(404, 'No such album.');
      if (typeof b.body !== 'string' || b.body.length > 2000) return problem(422, 'validation failed');
      const body = b.body.trim(), assetId = typeof b.assetId === 'string' ? b.assetId : undefined;
      if (body === '') return problem(422, 'Say something first.');
      if (m![1] !== this.lib.shared.album) return problem(403, NOT_IN_DEMO);
      if (assetId && !this.inAlbum(m![1]).some((i) => i.id === assetId)) return problem(404, 'That item isn’t in this album.');
      const c = { id: this.newId('comment'), member: this.me.id, assetId, body, at: new Date().toISOString() };
      this.comments.push(c);
      return { status: 201, body: this.comment(c) };
    }
    if (route('DELETE', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/comments\/([0-9a-f-]{36})$/)) {
      const before = this.comments.length;
      this.comments = this.comments.filter((c) => !(m![1] === this.lib.shared.album && c.id === m![2]));
      return this.comments.length < before ? ok() : problem(404, 'No such comment.');
    }
    if (route('GET', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/picks$/)) {
      if (!this.albums.has(m![1])) return problem(404, 'No such album.');
      return ok({ picks: this.albumPicks(m![1]) });
    }
    if ((route('PUT', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/picks\/([0-9a-f-]{36})$/) || route('DELETE', /^\/api\/v1\/albums\/([0-9a-f-]{36})\/picks\/([0-9a-f-]{36})$/))) {
      const [album, asset] = [m![1], m![2]];
      if (!this.albums.has(album)) return problem(404, 'No such album.');
      if (album !== this.lib.shared.album) return problem(403, NOT_IN_DEMO);
      const mineToo = (x: DemoShared['picks'][number]) => x.assetId === asset && x.member === this.me.id;
      if (r.method === 'DELETE') this.picks = this.picks.filter((x) => !mineToo(x));
      else if (!this.picks.some(mineToo) && this.inAlbum(album).some((i) => i.id === asset)) this.picks.push({ member: this.me.id, assetId: asset, at: new Date().toISOString() });
      return ok();
    }

    // People, pets and memories.
    if (route('GET', /^\/api\/v1\/people$/)) {
      const hidden = r.query.get('hidden') === 'true';
      const list = this.lib.people.filter((x) => this.hiddenPeople.has(x.id) === hidden).map((x) => this.person(x.id)!).sort((x, y) => y.count - x.count);
      return ok({ people: list, faces: { scanned: this.items.size, waiting: 0 } });
    }
    if (route('GET', /^\/api\/v1\/people\/([0-9a-f-]{36})$/)) return this.person(m![1]) ? ok(this.person(m![1])) : problem(404, 'Not found');
    if (route('PATCH', /^\/api\/v1\/people\/([0-9a-f-]{36})$/)) {
      const id = m![1];
      if (!this.person(id)) return problem(404, 'Not found');
      if (typeof b.name === 'string' && b.name.trim()) this.names.set(id, b.name.trim());
      if (typeof b.hidden === 'boolean') (b.hidden ? this.hiddenPeople.add(id) : this.hiddenPeople.delete(id));
      if (typeof b.inMemories === 'boolean') (b.inMemories ? this.outOfMemories.delete(id) : this.outOfMemories.add(id));
      return ok(this.person(id));
    }
    if (route('POST', /^\/api\/v1\/people\/([0-9a-f-]{36})\/remove$/))
      return change(own.filter((i) => this.peopleIn(i).includes(m![1])), (i) => void (i.people = i.people.filter((x) => x !== m![1])));
    if (route('GET', /^\/api\/v1\/memories$/)) return ok({ date: new Date().toISOString().slice(0, 10), memories: this.memories() });
    if (route('GET', /^\/api\/v1\/memories\/settings$/))
      return ok({ hiddenDays: this.hiddenDays, hiddenPeople: [...this.outOfMemories].map((id) => this.person(id)).filter(Boolean) });
    if (route('POST', /^\/api\/v1\/memories\/hidden$/)) {
      const from = String(b.from ?? ''), to = b.to ? String(b.to) : from;
      if (!isDay(from)) return problem(422, 'from isn’t a day');
      if (!isDay(to) || to < from) return problem(422, 'to must be a day, no earlier than from');
      const days = { id: this.newId('hidden'), from, to };
      this.hiddenDays.push(days);
      return { status: 201, body: days };
    }
    if (route('DELETE', /^\/api\/v1\/memories\/hidden\/([0-9a-f-]{36})$/)) {
      const before = this.hiddenDays.length;
      this.hiddenDays = this.hiddenDays.filter((h) => h.id !== m![1]);
      return this.hiddenDays.length < before ? ok() : problem(404, 'No such hidden days.');
    }

    // What was made with AI.
    if (route('GET', /^\/api\/v1\/prompts$/)) {
      const text = (r.query.get('q') ?? '').toLowerCase();
      let list = this.prompts().filter((x) => !text || x.text.toLowerCase().includes(text));
      list = r.query.get('sort') === 'recent' ? list.sort((x, y) => (y.lastDate ?? '').localeCompare(x.lastDate ?? '')) : list.sort((x, y) => y.uses - x.uses);
      const off = Number(r.query.get('offset') ?? 0), lim = Number(r.query.get('limit') ?? 40);
      return ok({ total: list.length, prompts: list.slice(off, off + lim) });
    }
    if (route('GET', /^\/api\/v1\/prompts\/([0-9a-f-]{36})$/)) {
      const x = this.prompts().find((y) => y.id === m![1]);
      return x ? ok(x) : problem(404, 'Not found');
    }
    if (route('GET', /^\/api\/v1\/models$/)) {
      const list = this.modelList().sort((x, y) => y.uses - x.uses);
      return ok({ models: list.filter((x) => x.kind === 'model'), loras: list.filter((x) => x.kind === 'lora') });
    }
    if (route('GET', /^\/api\/v1\/models\/([0-9a-f-]{36})$/)) {
      const x = this.modelList().find((y) => y.id === m![1]);
      return x ? ok(x) : problem(404, 'Not found');
    }
    if (route('GET', /^\/api\/v1\/characters$/)) return ok({ characters: this.lib.characters.map((c) => this.character(c.id)!) });
    if (route('GET', /^\/api\/v1\/characters\/([0-9a-f-]{36})$/)) return this.character(m![1]) ? ok(this.character(m![1])) : problem(404, 'Not found');

    // Search and Ask.
    if (route('GET', /^\/api\/v1\/search$/))
      return ok(this.search(r.query.get('q') ?? '', r.query.get('type') ?? '', r.query.get('mode') ?? 'auto', r.query.get('similar') ?? ''));
    if (route('POST', /^\/api\/v1\/ask$/)) return this.ask(r.body);

    // Look-alikes and stacks.
    if (route('GET', /^\/api\/v1\/stacks\/suggested$/)) {
      const list = [...this.stacks.values()].filter((s) => s.state === 'suggested' && s.members.filter((id) => this.items.has(id) && !this.trashed.has(id)).length > 1);
      const off = Number(r.query.get('offset') ?? 0);
      return ok({ total: list.length, stacks: list.slice(off, off + 40).map((s) => this.stack(s)) });
    }
    if (route('POST', /^\/api\/v1\/stacks\/suggested\/confirm$/)) {
      const list = [...this.stacks.values()].filter((s) => s.state === 'suggested');
      list.forEach((s) => (s.state = 'confirmed'));
      return ok({ changed: list.length, stackIds: list.map((s) => s.id) });
    }
    if (route('POST', /^\/api\/v1\/stacks\/unconfirm$/)) {
      const list = ((b.stackIds as string[]) ?? []).map((id) => this.stacks.get(id)).filter((s) => s !== undefined);
      list.forEach((s) => (s.state = 'suggested'));
      return ok({ changed: list.length });
    }
    if (route('POST', /^\/api\/v1\/stacks$/)) {
      if (ids.length < 2) return problem(422, 'Pick at least two of your own items.');
      if ([...this.stacks.values()].some((s) => s.members.some((x) => ids.includes(x)))) return problem(409, 'Some of them are in a stack already: unstack those first.');
      const id = this.newId('stack');
      this.stacks.set(id, { id, kind: 'manual', cover: ids[0], members: ids, state: 'confirmed' });
      return ok(this.stack(this.stacks.get(id)!));
    }
    if (route('POST', /^\/api\/v1\/stacks\/([0-9a-f-]{36})\/(confirm|cover|remove|trash-rest)$/)) {
      const s = this.stacks.get(m![1]);
      if (!s) return problem(404, 'Not found');
      if (m![2] === 'confirm') s.state = 'confirmed';
      if (m![2] === 'cover' && s.members.includes(String(b.assetId))) s.cover = String(b.assetId);
      if (m![2] === 'remove') {
        // a stack left with one item goes; one whose top left gets a new one
        s.members = s.members.filter((x) => !ids.includes(x));
        if (s.members.length < 2) this.stacks.delete(s.id);
        else if (!s.members.includes(s.cover)) s.cover = s.members[0];
      }
      if (m![2] === 'trash-rest') {
        const rest = s.members.filter((x) => x !== s.cover);
        rest.forEach((x) => this.trashed.set(x, new Date().toISOString()));
        this.stacks.delete(s.id); // the stack goes
        return ok({ changed: rest.length });
      }
      return m![2] === 'remove' ? ok({ changed: ids.length }) : ok(this.stack(s));
    }
    if (route('DELETE', /^\/api\/v1\/stacks\/([0-9a-f-]{36})$/)) {
      this.stacks.delete(m![1]);
      return ok();
    }

    // Folders: the managed layout, library/<person>/photos/<year>/<day>.
    if (route('GET', /^\/api\/v1\/folders$/)) return ok({ libraries: [this.library()] });
    if (route('GET', /^\/api\/v1\/folders\/([0-9a-f-]{36})$/)) {
      const at = r.query.get('path') ?? '';
      const below = new Map<string, number>();
      for (const i of this.all()) {
        if (!this.visible(i) || (at && !i.folderPath.startsWith(at + '/'))) continue;
        const name = i.folderPath.slice(at ? at.length + 1 : 0).split('/')[0];
        if (name) below.set(name, (below.get(name) ?? 0) + 1);
      }
      return ok({ library: this.library(), path: at, folders: [...below].sort((x, y) => y[0].localeCompare(x[0])).map(([name, items]) => ({ name, path: at ? `${at}/${name}` : name, items })) });
    }

    // The household, and the vault.
    if (route('GET', /^\/api\/v1\/household$/)) return ok(this.household());
    if (route('PATCH', /^\/api\/v1\/household$/)) {
      if (typeof b.name === 'string' && b.name.trim()) this.householdName = b.name.trim();
      return ok(this.household());
    }
    if (route('DELETE', /^\/api\/v1\/household\/guests\/([0-9a-f-]{36})$/)) return this.removeGuest(m![1]);
    // Into the household: the viewer's own items (not in the trash or the vault). Out of it: any
    // member may take any item out, and another member's then leaves the viewer's view.
    if (route('POST', /^\/api\/v1\/household\/share$/))
      return change(own.filter((i) => !i.shared && !this.trashed.has(i.id) && !this.inVault(i)), (i) => void (i.shared = true));
    if (route('POST', /^\/api\/v1\/household\/unshare$/)) return change(ids.map((id) => this.items.get(id)!).filter((i) => i.shared), (i) => void (i.shared = false));
    if (route('GET', /^\/api\/v1\/vault$/)) return ok(this.vaultState());
    if (route('POST', /^\/api\/v1\/vault\/pin$/)) {
      if (this.vaultPin && b.currentPin !== this.vaultPin) return problem(403, 'That PIN isn’t right.');
      this.vaultPin = String(b.pin ?? '');
      this.vaultOpen = true;
      return ok(this.vaultState());
    }
    if (route('POST', /^\/api\/v1\/vault\/unlock$/)) {
      if (String(b.pin ?? '') !== this.vaultPin) return problem(403, 'That PIN isn’t right.');
      this.vaultOpen = true;
      return ok(this.vaultState());
    }
    if (route('POST', /^\/api\/v1\/vault\/lock$/)) {
      this.vaultOpen = false;
      return ok(this.vaultState());
    }
    if (route('POST', /^\/api\/v1\/vault\/trash\/empty$/)) {
      if (!this.vaultOpen) return problem(403, 'The vault is locked. Unlock it with its PIN.');
      const gone = [...this.trashed.keys()].filter((id) => this.inVault(this.items.get(id)!) && (!ids.length || ids.includes(id)));
      gone.forEach((id) => {
        this.items.delete(id);
        this.trashed.delete(id);
        this.vault.delete(id);
      });
      return ok({ deleted: gone.length, kept: 0 });
    }
    if (route('POST', /^\/api\/v1\/vault\/add$/)) {
      // As the server's MoveToVault: needs a PIN set (not the vault unlocked), and takes the
      // viewer's own items from the timeline or the archive. They leave the household and any
      // stack, and come back out to the timeline.
      if (!this.vaultPin) return problem(409, 'Set a PIN for the vault first.');
      const moved = own.filter((i) => !this.trashed.has(i.id) && !this.inVault(i));
      for (const s of [...this.stacks.values()]) {
        const gone = s.members.filter((id) => moved.some((i) => i.id === id));
        if (!gone.length) continue;
        s.members = s.members.filter((id) => !gone.includes(id));
        if (s.members.length < 2) this.stacks.delete(s.id);
        else if (gone.includes(s.cover)) s.cover = s.members[0];
      }
      return change(moved, (i) => {
        this.vault.add(i.id);
        i.shared = false;
        i.archived = undefined;
      });
    }
    if (route('POST', /^\/api\/v1\/vault\/remove$/)) {
      if (!this.vaultOpen) return problem(403, 'The vault is locked. Unlock it with its PIN.');
      return change(own.filter((i) => this.inVault(i)), (i) => void this.vault.delete(i.id));
    }

    if (r.method === 'GET') return problem(404, 'Not found');
    return problem(403, NOT_IN_DEMO);
  }

  private stack(s: { id: string; kind: Stack['kind']; cover: string; members: string[]; state: 'suggested' | 'confirmed' }): Stack {
    return { id: s.id, kind: s.kind, state: s.state, cover: s.cover,
      members: s.members.filter((id) => !this.trashed.has(id)).map((id) => this.items.get(id)).filter((i) => i !== undefined).map((i) => this.asset(i)) };
  }

  private shareLink(l: DemoLink): ShareLink {
    const url = `${TAILNET}/s/${l.token}`;
    return { id: l.id, url, qr: qrImage(url), public: false, hasPassword: l.hasPassword, expiresAt: l.expiresAt, allowDownload: l.allowDownload, allowUpload: l.allowUpload,
      views: 0, createdAt: l.createdAt };
  }

  private inAlbum(id: string) {
    return this.inScope({ ...this.scope(new URLSearchParams()), collection: `album:${id}` }) ?? [];
  }

  /** A member's name, or a guest's. */
  private memberName(id: string) {
    return this.lib.members.find((x) => x.id === id)?.name ?? this.guests.find((g) => g.id === id)?.name ?? '';
  }

  private comment(c: DemoShared['comments'][number]) {
    // mine: the viewer may take it back, as the album's owner may any.
    return { id: c.id, assetId: c.assetId, author: this.memberName(c.member), guest: this.guests.some((g) => g.id === c.member) || undefined, mine: true,
      body: c.body, createdAt: c.at };
  }

  /** What happened in an album, newest first: a day's additions by each person together, and comments (AlbumActivity). */
  private activity(album: string) {
    const added = new Map<string, { who: string; at: string; list: { id: string; at: string }[] }>();
    for (const i of this.inAlbum(album)) {
      // The shared album's pictures were put in when it was shared; others when they were taken in.
      const at = this.putIn.get(`${album}:${i.id}`) ?? (album === this.lib.shared.album ? this.lib.shared.addedAt : i.addedAt);
      const key = at.slice(0, 10);
      const g = added.get(key) ?? { who: this.me.displayName, at, list: [] };
      if (at > g.at) g.at = at;
      g.list.push({ id: i.id, at });
      added.set(key, g);
    }
    const out = [
      ...[...added.values()].map((g) => ({ kind: 'added' as const, who: g.who, at: g.at, items: g.list.length,
        assets: g.list.sort((x, y) => cmp(y.at, x.at)).slice(0, 6).map((x) => x.id) })),
      ...(album === this.lib.shared.album ? this.comments : []).map((c) => ({ kind: 'comment' as const, id: c.id, mine: true, who: this.memberName(c.member), at: c.at, body: c.body, assetId: c.assetId })),
    ];
    return out.sort((x, y) => cmp(y.at, x.at)).slice(0, 100);
  }

  /** Who picked what in an album, in the order they were first picked; the owner sees everyone's. */
  private albumPicks(album: string) {
    const by = new Map<string, { assetId: string; people: string[]; mine: boolean }>();
    const here = new Set(this.inAlbum(album).map((i) => i.id));
    for (const x of (album === this.lib.shared.album ? this.picks : []).filter((y) => here.has(y.assetId)).sort((y, z) => cmp(y.at, z.at))) {
      const p = by.get(x.assetId) ?? { assetId: x.assetId, people: [], mine: false };
      p.people.push(this.memberName(x.member));
      if (x.member === this.me.id) p.mine = true;
      by.set(x.assetId, p);
    }
    return [...by.values()];
  }

  private keywords(list: DemoItem[]): Keyword[] {
    const n = new Map<string, number>();
    for (const i of list) for (const k of i.keywords) n.set(k, (n.get(k) ?? 0) + 1);
    return [...n].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([name, items]) => ({ name, items }));
  }

  private library() {
    return { id: this.uuid('library'), name: 'Photostead', kind: 'managed' as const, readOnly: false, items: this.all().filter((i) => this.visible(i)).length };
  }
}
