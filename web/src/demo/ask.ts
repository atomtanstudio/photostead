// Ask turns a plain-language question about the library into a plan of filters, and writes the
// answer. A port of the server's planner (server/internal/ask/ask.go), kept close to it so the two
// read side by side: the same rules, words, dates and replies. Dates, media type, favorites,
// people, AI characters, models and LoRAs, where an item was made, words in its prompt, text in the
// picture, file names and utility kinds (screenshots, receipts…). Whatever is left over describes
// what the items look like, and is matched by meaning. The demo server runs the plan as the
// server's api/ask.go does (engine.ts).
//
// Where Go and JavaScript differ, this does as Go does: \s is ASCII space in a regexp, a word's
// length is counted in bytes, lower case is per letter, and dates are Go's (gotime.ts): time.Date
// in the viewer's zone, with int64 arithmetic.

import type { AskChip, AskPlan } from '../api';
import { addDate, after, atoi, civil, formatDay, goDate, isDay, type GoTime } from './gotime';

/** Names a person or character. */
export type Named = { id: string; name: string };

/** What a question asks for, with Go's zero values for what it doesn't. Dates are local days (YYYY-MM-DD); before is exclusive. */
export type Plan = {
  intent: string; // list, count, first or last
  type: string; // image or video
  people: Named[];
  character: Named | null;
  model: string; // a model or LoRA, as the question named it
  modelIds: string[]; // the models and LoRAs whose names contain it
  source: string; // where it was made: comfyui, a1111, midjourney, grok, openai, google
  prompt: string; // words in its prompt
  ocr: string; // text in the picture
  filename: string;
  label: string; // a utility kind: screenshot, document, receipt, id_card, qr_code
  favorite: boolean;
  after: string;
  before: string;
  when: string; // the dates, as they'd be said
  aiOnly: boolean;
  text: string; // what the items look like (matched by meaning)
};

const blank = (): Plan => ({ intent: 'list', type: '', people: [], character: null, model: '', modelIds: [], source: '', prompt: '', ocr: '',
  filename: '', label: '', favorite: false, after: '', before: '', when: '', aiOnly: false, text: '' });

/** A model or LoRA a question can name. */
export type Model = { id: string; kind: string; name: string }; // kind: model or lora; name: normalized, lower case

/** What the planner recognizes by name. */
export type Vocabulary = { people: Named[]; characters: Named[]; models: Model[] };

/** The viewer's current time, in their time zone (nowIn). */
export type Now = GoTime;

// ---- Go's ways, where JavaScript's differ ----

// unicode.IsSpace, for strings.Fields and strings.TrimSpace.
const SPACE = '[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]';
const reFields = new RegExp(`${SPACE}+`);
const reTrim = new RegExp(`^${SPACE}+|${SPACE}+$`, 'g');
const fields = (s: string) => s.split(reFields).filter((w) => w !== '');
/** strings.TrimSpace: unlike String.prototype.trim, it trims U+0085 and keeps U+FEFF. */
export const trimSpace = (s: string) => s.replace(reTrim, '');
// strings.ToLower: letter by letter (no final sigma), and İ is plain i. Go's tables are Unicode
// 17's; the case pairs added since Unicode 15 are here (the upper-case letters from first to last,
// every step, and the offset to their lower case), for browsers whose own tables are older (Node
// 22's are Unicode 16).
const newerPairs = [[0x1c89, 0x1c89, 1, 1], [0xa7cb, 0xa7cb, 1, 0x264 - 0xa7cb], [0xa7cc, 0xa7ce, 2, 1], [0xa7d2, 0xa7d4, 2, 1],
  [0xa7da, 0xa7da, 1, 1], [0xa7dc, 0xa7dc, 1, 0x19b - 0xa7dc], [0x10d50, 0x10d65, 1, 0x20], [0x16ea0, 0x16eb8, 1, 0x1b]];
const lowerRune = (c: string) => {
  const r = c.codePointAt(0)!;
  for (const [first, last, step, offset] of newerPairs) if (r >= first && r <= last && (r - first) % step === 0) return String.fromCodePoint(r + offset);
  return c === 'İ' ? 'i' : c.toLowerCase();
};
const lowerCase = (s: string) => Array.from(s, lowerRune).join('');
// len(s): bytes, in UTF-8.
const utf8 = new TextEncoder();
const blen = (s: string) => utf8.encode(s).length;
// strconv.Atoi succeeds: a signed decimal that fits 64 bits.
const isInt = (s: string) => /^[+-]?[0-9]+$/.test(s) && BigInt(s) >= -(2n ** 63n) && BigInt(s) < 2n ** 63n;
// A regexp as Go reads it: \s is ASCII space only. All of them find every match.
const re = (src: string) => new RegExp(src.replace(/\\s/g, '[\\t\\n\\f\\r ]'), 'g');
// MatchString: search ignores a global regexp's lastIndex.
const matches = (r: RegExp, s: string) => s.search(r) >= 0;

// ---- the words ----

const months = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december'];

const stop = new Set(fields(`show me find all my the of from with a an please any some that which i did do have has when time times how many
		were was made make created generated take took taken in on at during for to and or is are what where who this these those there their our we
		you it its by containing contain contains using used ever only just look looks like see get give list can could would me also about last first
		use uses most recent latest earliest text words word says saying named called file filename prompt prompts lora loras model models checkpoint
		checkpoints kind type stuff things ones one something anything every each library collection items item say said read reads
		files shot shots`));

// Model families worth recognizing even before the exact model is known.
const families = ['flux', 'qwen', 'wan', 'sdxl', 'sd15', 'pony', 'illustrious', 'hunyuan', 'ltx', 'minimax', 'krea', 'chroma', 'hidream',
  'kolors', 'lustify', 'juggernaut', 'realvis', 'epicrealism', 'cyberrealistic', 'zimage', 'seedream', 'dmd2', 'lightx2v', 'fusionx'];

// Where things are made: the words people use, the value stored (a generation's source, or part
// of its tool's name), and how to say it.
const sources = new Map(Object.entries({ comfyui: 'comfyui', comfy: 'comfyui', a1111: 'a1111', automatic1111: 'a1111', forge: 'a1111',
  midjourney: 'midjourney', grok: 'grok', chatgpt: 'openai', openai: 'openai', 'dall-e': 'openai', dalle: 'openai', sora: 'openai',
  google: 'google', gemini: 'google', imagen: 'google', veo: 'google' }));

// Says a source's name. Maps, like Go's: a plan sent back can name any source, and an object's
// lookup would find "constructor" and the like on its prototype, where Go's map has nothing.
const sourceLabels = new Map(Object.entries({ comfyui: 'ComfyUI', a1111: 'A1111 or Forge', midjourney: 'Midjourney', grok: 'Grok',
  openai: 'OpenAI', google: 'Google AI' }));
/** Says a source's name ('' for one it doesn't know, as Go's map lookup). */
export const sourceLabel = (source: string) => sourceLabels.get(source) ?? '';
const typeLabels = new Map(Object.entries({ video: 'Videos', image: 'Photos and images' }));
const seasonStart = new Map(Object.entries({ spring: 3, summer: 6, autumn: 9, fall: 9, winter: 12 }));

// Utility kinds, as said, and their plural for answers.
const labelWords = [
  { re: re(String.raw`\b(screen ?shots?)\b`), label: 'screenshot', plural: 'screenshots' },
  { re: re(String.raw`\b(receipts?)\b`), label: 'receipt', plural: 'receipts' },
  { re: re(String.raw`\b(documents?|scans|scanned (?:pages?|documents?))\b`), label: 'document', plural: 'documents' },
  { re: re(String.raw`\b(id cards?|identity cards?|driver'?s? licen[cs]es?|passports?)\b`), label: 'id_card', plural: 'IDs' },
  { re: re(String.raw`\b(qr ?codes?)\b`), label: 'qr_code', plural: 'QR codes' },
];

const reCount = re(String.raw`\bhow many\b|\bcount\b|\bnumber of\b`);
const reFirst = re(String.raw`\bfirst time\b|\bearliest\b|\boldest\b|\bwhen did i (?:first|start)\b`);
const reLast = re(String.raw`\blast time\b|\bmost recent\b|\blatest\b|\bnewest\b|\bwhen did i last\b|\bwhen was the last\b`);
const rePromptQ = re(String.raw`\bprompt(?:ed)?(?: with| containing| that says)?\s+"([^"]+)"`);
const reFileQ = re(String.raw`\b(?:file(?:name)?|named|called)\s+"([^"]+)"`);
const reQuoted = re(String.raw`"([^"]+)"`);
const reSays = re(String.raw`\b(?:with the (?:text|words?)|that (?:says|say|reads|read)|saying)\s+([a-z0-9][a-z0-9 '-]{0,60})`);
const rePrompt = re(String.raw`\bprompts?\s+(?:with|containing|about|mentioning)\s+([a-z0-9][a-z0-9 ,'-]{0,80})`);
const reFav = re(String.raw`\b(favou?rites?|favou?rited|starred|liked)\b`);
const reVideo = re(String.raw`\b(videos?|clips?|movies?|footage)\b`);
const rePhoto = re(String.raw`\b(photos?|pictures?|pics?|images?|stills?|renders?)\b`);
const reToday = re(String.raw`\btoday\b`);
const reYest = re(String.raw`\byesterday\b`);
const reRel = re(String.raw`\b(this|last|past)\s+(week|month|year)\b`);
const reLastN = re(String.raw`\b(?:in the )?(?:last|past)\s+(\d+)\s+(days?|weeks?|months?|years?)\b`);
const reBetween = re(String.raw`\bbetween\s+((?:19|20)\d{2})\s+and\s+((?:19|20)\d{2})\b`);
const reSince = re(String.raw`\bsince\s+((?:19|20)\d{2})\b`);
const reBefore = re(String.raw`\bbefore\s+((?:19|20)\d{2})\b`);
const reMonth = re(String.raw`\b(in\s+)?(january|february|march|april|may|june|july|august|september|october|november|december)(?:\s+((?:19|20)\d{2}))?\b`);
const reSeason = re(String.raw`\b(?:(last|this)\s+)?(summer|winter|spring|autumn|fall)(?:\s+((?:19|20)\d{2}))?\b`);
const reYear = re(String.raw`\b(?:in\s+)?((?:19|20)\d{2})\b`);
const reSource = re(String.raw`\b(comfyui|comfy|a1111|automatic1111|forge|midjourney|grok|chatgpt|openai|dall-e|dalle|sora|google|gemini|imagen|veo)\b`);
const reAI = re(String.raw`\b(ai[- ]generated|ai[- ]made|generated|ai)\b`);
const reModelK = re(String.raw`\b([a-z0-9][a-z0-9._-]{2,40})\s+(?:loras?|models?|checkpoints?)\b`);
const reModelW = re(String.raw`\b(?:with|using|in|on|via)\s+([a-z0-9][a-z0-9._-]{2,40})\b`);
const reFamily = re(String.raw`\b(` + families.join('|') + String.raw`)\b`);
const reGen = re(String.raw`\b(lora|model|prompt|render)`);
const rePunct = re(String.raw`[?!.,;]+(\s|$)`);

// tailStops end a free-text phrase ("that says open late in june" → "open late").
const tailStops = new Set(['in', 'from', 'on', 'during', 'since', 'before', 'after', 'with']);

// cutTail keeps a phrase up to the first word that starts a new part of the question, and returns
// the rest so it can still be read.
function cutTail(phrase: string): [kept: string, rest: string] {
  const words = fields(phrase);
  for (let i = 0; i < words.length; i++) {
    if (i > 0 && tailStops.has(words[i])) return [words.slice(0, i).join(' '), words.slice(i).join(' ')];
  }
  return [words.join(' '), ''];
}

class Parser {
  constructor(public s: string) {}

  // take replaces each match of re with what fn returns (fn gets the submatches; returning m[0]
  // keeps the text as it was).
  take(r: RegExp, fn: (m: string[]) => string) {
    let b = '';
    let last = 0;
    for (const x of this.s.matchAll(r)) {
      b += this.s.slice(last, x.index) + fn(Array.from(x, (g) => g ?? ''));
      last = x.index! + x[0].length;
    }
    this.s = b + this.s.slice(last);
  }
}

// titleCase is Go's strings.ToUpper(s[:1]) + s[1:]: the first byte. When that's part of a longer
// letter (a plan sent back can say anything), Go writes U+FFFD for it and its JSON writes U+FFFD
// for each of the letter's other bytes.
const titleCase = (s: string) => {
  if (s === '') return s;
  const c = String.fromCodePoint(s.codePointAt(0)!);
  return c < '\x80' ? c.toUpperCase() + s.slice(1) : '\ufffd'.repeat(blen(c)) + s.slice(c.length);
};

const wordRe = (w: string) => new RegExp(String.raw`\b` + w.replace(/[\\.+*?()|[\]{}^$]/g, '\\$&') + String.raw`\b`, 'g');

/** Plans a question. now is the viewer's current time, in their time zone. */
export function parse(q: string, now: Now, v: Vocabulary): Plan {
  const plan = blank();
  const lower = lowerCase(q).replace(/[“”]/g, '"').replace(/[‘’]/g, "'");
  const p = new Parser(' ' + lower.replace(rePunct, ' ') + ' ');
  const today = civil(now);
  const [y, mo, d] = [Number(today.year), Number(today.month), Number(today.day)];
  // date(y, m, d) is time.Date(y, m, d, 0, 0, 0, 0, loc): midnight there (or when Go moves a midnight a clock change skips).
  const date = (year: number | bigint, month: number | bigint, day: number | bigint) =>
    goDate(BigInt(year), BigInt(month), BigInt(day), 0n, 0n, 0n, 0n, now.zone);
  const rng = (a: GoTime | null, b: GoTime | null, label: string) => {
    plan.after = a ? formatDay(a) : '';
    plan.before = b ? formatDay(b) : '';
    plan.when = label;
  };

  if (matches(reCount, p.s)) plan.intent = 'count';
  else if (matches(reFirst, p.s)) plan.intent = 'first';
  else if (matches(reLast, p.s)) plan.intent = 'last';

  p.take(rePromptQ, (m) => ((plan.prompt = m[1]), (plan.aiOnly = true), ' '));
  p.take(reFileQ, (m) => ((plan.filename = m[1]), ' '));
  p.take(reQuoted, (m) => ((plan.ocr = m[1]), ' '));
  p.take(reSays, (m) => {
    const [kept, rest] = cutTail(m[1]);
    plan.ocr = kept;
    return ' ' + rest + ' ';
  });
  p.take(rePrompt, (m) => {
    const [kept, rest] = cutTail(m[1]);
    plan.prompt = kept.replace(/^[ ,]+|[ ,]+$/g, '');
    plan.aiOnly = true;
    return ' ' + rest + ' ';
  });
  p.take(reFav, () => ((plan.favorite = true), ' '));
  for (const l of labelWords) p.take(l.re, () => ((plan.label = l.label), ' '));
  p.take(reVideo, () => ((plan.type = 'video'), ' '));
  p.take(rePhoto, (m) => {
    if (plan.type === '') plan.type = 'image';
    if (m[1].startsWith('render')) plan.aiOnly = true;
    return ' ';
  });

  // Dates, most specific first.
  p.take(reToday, () => (rng(date(y, mo, d), null, 'today'), ' '));
  p.take(reYest, () => (rng(date(y, mo, d - 1), date(y, mo, d), 'yesterday'), ' '));
  p.take(reRel, (m) => {
    const [, which, unit] = m;
    switch (unit) {
      case 'week': {
        let back = today.weekday;
        if (which !== 'this') back += 7;
        const a = date(y, mo, d - back);
        if (which === 'this') rng(a, null, 'this week');
        else rng(a, addDate(a, 0n, 0n, 7n), 'last week');
        break;
      }
      case 'month':
        if (which === 'this') {
          rng(date(y, mo, 1), null, titleCase(months[mo - 1]));
        } else {
          const a = date(y, mo - 1, 1);
          const { year, month } = civil(a);
          rng(a, date(y, mo, 1), titleCase(months[Number(month) - 1]) + ' ' + year);
        }
        break;
      default: {
        let yy = y;
        if (which !== 'this') yy--;
        rng(date(yy, 1, 1), date(yy + 1, 1, 1), String(yy));
      }
    }
    return ' ';
  });
  p.take(reLastN, (m) => {
    const n = atoi(m[1]);
    let a: GoTime;
    if (m[2].startsWith('day')) a = addDate(now, 0n, 0n, -n);
    else if (m[2].startsWith('week')) a = addDate(now, 0n, 0n, -7n * n);
    else if (m[2].startsWith('month')) a = addDate(now, 0n, -n, 0n);
    else a = addDate(now, -n, 0n, 0n);
    const { year, month, day } = civil(a);
    rng(date(year, month, day), null, `the last ${n} ${m[2]}`);
    return ' ';
  });
  p.take(reBetween, (m) => {
    const a = parseInt(m[1], 10);
    const b = parseInt(m[2], 10);
    rng(date(a, 1, 1), date(b + 1, 1, 1), m[1] + ' to ' + m[2]);
    return ' ';
  });
  p.take(reSince, (m) => (rng(date(parseInt(m[1], 10), 1, 1), null, 'since ' + m[1]), ' '));
  p.take(reBefore, (m) => (rng(null, date(parseInt(m[1], 10), 1, 1), 'before ' + m[1]), ' '));
  p.take(reMonth, (m) => {
    // "may" is a month only as "in may" or "may 2024".
    if (m[2] === 'may' && m[1] === '' && m[3] === '') return m[0];
    const mi = months.indexOf(m[2]) + 1;
    let yy = y;
    if (m[3] !== '') yy = parseInt(m[3], 10);
    else if (mi > mo) yy--; // a month still to come this year means last year's
    rng(date(yy, mi, 1), date(yy, mi + 1, 1), `${titleCase(m[2])} ${yy}`);
    return ' ';
  });
  p.take(reSeason, (m) => {
    const start = seasonStart.get(m[2])!;
    let yy = y;
    if (m[3] !== '') yy = parseInt(m[3], 10);
    else if (after(date(yy, start + 3, 1), now)) yy--; // not over yet this year: the last one
    rng(date(yy, start, 1), date(yy, start + 3, 1), `${m[2]} ${yy}`);
    return ' ';
  });
  p.take(reYear, (m) => {
    const a = parseInt(m[1], 10);
    rng(date(a, 1, 1), date(a + 1, 1, 1), m[1]);
    return ' ';
  });

  // Where and how it was made.
  p.take(reSource, (m) => ((plan.source = sources.get(m[1])!), (plan.aiOnly = true), ' '));
  p.take(reAI, () => ((plan.aiOnly = true), ' '));
  const models = (t: string) => v.models.filter((m) => m.name.includes(t)).map((m) => m.id);
  const known = (t: string) => families.includes(t) || (blen(t) >= 4 && models(t).length > 0);
  const setModel = (t: string) => {
    plan.model = t;
    plan.modelIds = models(t);
    plan.aiOnly = true;
  };
  p.take(reModelK, (m) => {
    if (!known(m[1])) return m[0];
    setModel(m[1]);
    return ' ';
  });
  p.take(reModelW, (m) => {
    if (plan.model !== '' || !known(m[1])) return m[0];
    setModel(m[1]);
    return ' ';
  });
  p.take(reFamily, (m) => {
    if (plan.model === '') setModel(m[1]);
    return ' ';
  });

  // Characters and people, by name: the longest names first, the full name or a first name of
  // three letters or more. A name that is both goes to the character when the question is about
  // AI work (or there's no such person).
  const genContext = plan.aiOnly || matches(reGen, lower);
  type Hit = { ref: Named; said: string };
  const find = (refs: Named[]): Hit[] => {
    const sorted = [...refs].sort((a, b) => blen(b.name) - blen(a.name));
    const hits: Hit[] = [];
    for (const r of sorted) {
      const full = lowerCase(trimSpace(r.name));
      for (const n of [full, fields(full)[0] ?? '']) {
        if (blen(n) >= 3 && matches(wordRe(n), p.s)) {
          hits.push({ ref: r, said: n });
          break;
        }
      }
    }
    return hits;
  };
  const chars = find(v.characters);
  const people = find(v.people);
  if (chars.length > 0 && (genContext || people.length === 0)) {
    const c = chars[0];
    plan.character = { id: c.ref.id, name: c.ref.name };
    plan.aiOnly = true;
    p.take(wordRe(c.said), () => ' ');
    const parts = fields(lowerCase(c.ref.name));
    if (parts.length > 1) p.take(wordRe(parts.slice(1).join(' ')), () => ' ');
  } else {
    for (const h of people) {
      plan.people.push({ id: h.ref.id, name: h.ref.name });
      p.take(wordRe(h.said), () => ' ');
    }
  }

  const words: string[] = [];
  for (const w of fields(p.s)) {
    if (w !== '' && !stop.has(w) && blen(w) > 1) {
      if (isInt(w)) continue;
      words.push(w);
    }
  }
  plan.text = words.join(' ');
  if (!anything(plan) && plan.text === '') plan.text = trimSpace(q);
  return plan;
}

function anything(p: Plan) {
  return p.type !== '' || p.people.length > 0 || p.character !== null || p.model !== '' || p.source !== '' || p.prompt !== '' || p.ocr !== '' ||
    p.filename !== '' || p.label !== '' || p.favorite || p.when !== '' || p.aiOnly;
}

/** Says whether the plan has filters beyond a description. */
export const structured = (p: Plan) => anything(p);

// refining says a follow-up narrows the last question rather than asking a new one.
const refining = /^(and|only|just|but|what about|how about|now)\b/;

/**
 * Merges a follow-up into the previous plan: "only videos", "and last year" or "in 2024" keep what
 * was asked before; a new subject, or a whole new question, starts over.
 */
export function followUp(prev: Plan, next: Plan, q: string): Plan {
  const hasSubject = next.people.length > 0 || next.character !== null || next.model !== '' || next.prompt !== '' || next.ocr !== '' ||
    next.text !== '' || next.filename !== '' || next.label !== '';
  const short = fields(q).length <= 4;
  if (!refining.test(lowerCase(trimSpace(q))) && (hasSubject || !short)) return next;
  const m: Plan = { ...prev, people: [...prev.people] };
  if (next.type !== '') m.type = next.type;
  if (next.character !== null) m.character = next.character;
  if (next.model !== '') {
    m.model = next.model;
    m.modelIds = next.modelIds;
  }
  for (const k of ['source', 'prompt', 'ocr', 'filename', 'label', 'text'] as const) {
    if (next[k] !== '') m[k] = next[k];
  }
  if (next.people.length > 0) m.people = next.people;
  if (next.when !== '') {
    m.after = next.after;
    m.before = next.before;
    m.when = next.when;
  }
  m.favorite = m.favorite || next.favorite;
  m.aiOnly = m.aiOnly || next.aiOnly;
  m.intent = next.intent;
  return m;
}

/** Drops filters by their chip keys. */
export function without(plan: Plan, ...keys: string[]): Plan {
  const p = { ...plan };
  for (const k of keys) {
    switch (k) {
      case 'type':
        p.type = '';
        break;
      case 'people':
        p.people = [];
        break;
      case 'character':
        p.character = null;
        break;
      case 'model':
        p.model = '';
        p.modelIds = [];
        break;
      case 'source':
        p.source = '';
        break;
      case 'prompt':
        p.prompt = '';
        break;
      case 'ocr':
        p.ocr = '';
        break;
      case 'filename':
        p.filename = '';
        break;
      case 'label':
        p.label = '';
        break;
      case 'favorite':
        p.favorite = false;
        break;
      case 'when':
        p.after = p.before = p.when = '';
        break;
      case 'aiOnly':
        p.aiOnly = false;
        break;
      case 'text':
        p.text = '';
        break;
    }
  }
  // AI-only goes with what implied it.
  if (p.character === null && p.model === '' && p.source === '' && p.prompt === '') {
    for (const k of keys) if (k === 'aiOnly') p.aiOnly = false;
  }
  return p;
}

/** One filter of a plan, shown so it can be removed. */
export type Chip = AskChip;

function labelPlural(l: string) {
  return labelWords.find((w) => w.label === l)?.plural ?? l;
}

/** Lists a plan's filters. */
export function chips(p: Plan): Chip[] {
  const c: Chip[] = [];
  const add = (key: string, kind: string, label: string) => c.push({ key, kind, label });
  if (p.label !== '') add('label', 'Kind', titleCase(labelPlural(p.label)));
  if (p.type !== '') add('type', 'Type', typeLabels.get(p.type) ?? '');
  if (p.character !== null) add('character', 'Character', p.character.name);
  for (const r of p.people) add('people', 'Person', r.name);
  if (p.model !== '') add('model', 'Model or LoRA', p.model);
  if (p.source !== '') add('source', 'Made in', sourceLabel(p.source));
  if (p.aiOnly && p.character === null && p.model === '' && p.source === '' && p.prompt === '') add('aiOnly', 'Kind', 'AI-generated');
  if (p.prompt !== '') add('prompt', 'Prompt mentions', p.prompt);
  if (p.ocr !== '') add('ocr', 'Text in the picture', p.ocr);
  if (p.filename !== '') add('filename', 'File name', p.filename);
  if (p.when !== '') add('when', 'When', p.when);
  if (p.favorite) add('favorite', 'Only', 'Favorites');
  if (p.text !== '') add('text', 'Looks like', p.text);
  return c;
}

/** Says what a plan asks for, as a noun phrase: "favorite videos of Ada from 2024". */
export const describe = (p: Plan) => describeN(p, 2);

/** Says it for n items ("1 video", "3 videos"). */
export function describeN(p: Plan, n: number): string {
  const plainAI = p.aiOnly && p.character === null && p.model === '' && p.source === '' && p.prompt === '';
  const one = n === 1;
  const pick = (single: string, many: string) => (one ? single : many);
  let noun = pick('item', 'items');
  if (p.label !== '') {
    noun = labelPlural(p.label);
    if (one && noun.endsWith('s')) noun = noun.slice(0, -1);
  } else if (p.type === 'video' && plainAI) noun = pick('AI video', 'AI videos');
  else if (p.type === 'video') noun = pick('video', 'videos');
  else if (p.type === 'image' && plainAI) noun = pick('AI image', 'AI images');
  else if (p.type === 'image' && p.aiOnly) noun = pick('image', 'images');
  else if (p.type === 'image') noun = pick('photo', 'photos');
  else if (p.aiOnly) noun = pick('AI image or video', 'AI images and videos');
  if (p.favorite) noun = 'favorite ' + noun;
  const parts = [noun];
  if (p.character !== null) parts.push('of ' + p.character.name);
  if (p.people.length > 0) parts.push('with ' + p.people.map((r) => r.name).join(' and '));
  if (p.model !== '') parts.push('made with ' + p.model);
  if (p.source !== '') parts.push('from ' + sourceLabel(p.source));
  if (p.prompt !== '') parts.push('whose prompt mentions “' + p.prompt + '”');
  if (p.ocr !== '') parts.push('with the text “' + p.ocr + '”');
  if (p.filename !== '') parts.push('named like “' + p.filename + '”');
  if (p.when === 'today' || p.when === 'yesterday') parts.push(p.when);
  else if (p.when !== '') parts.push('from ' + p.when);
  if (p.text !== '') parts.push(pick('that looks like “', 'that look like “') + p.text + '”');
  return parts.join(' ');
}

/** What running a plan found. */
export type Result = {
  total: number;
  exact: boolean; // false when ranked by likeness (a description was matched by meaning)
  order: string; // date or relevance
  newest: string; // local days, YYYY-MM-DD
  oldest: string;
  alt: Alt | null;
};

/** The nearest question that does have answers, when one doesn't. remove is the chip key to drop. */
export type Alt = { remove: string; count: number; describe: string };

const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

function longDay(s: string): string {
  if (!isDay(s)) return 'an unknown day';
  const t = civil(goDate(BigInt(s.slice(0, 4)), BigInt(s.slice(5, 7)), BigInt(s.slice(8)), 0n, 0n, 0n, 0n, ''));
  return `${weekdays[t.weekday]} ${t.day} ${titleCase(months[Number(t.month) - 1])} ${s.slice(0, 4)}`;
}

const plural = (n: number, one: string, many: string) => (n === 1 ? one : many);

function count(n: number): string {
  let s = String(n);
  for (let i = s.length - 3; i > 0; i -= 3) s = s.slice(0, i) + ',' + s.slice(i);
  return s;
}

/** Writes the reply to a question. */
export function answer(p: Plan, r: Result): string {
  let what = describeN(p, r.total);
  if (r.total === 0) {
    what = describe(p);
    if (r.alt !== null) return `I couldn’t find any ${what}, but there ${plural(r.alt.count, 'is', 'are')} ${count(r.alt.count)} ${r.alt.describe}.`;
    return `I couldn’t find any ${what}. Try a broader question.`;
  }
  const n = count(r.total);
  if (p.intent === 'count' && r.exact) return `You have ${n} ${what}.`;
  if (p.intent === 'count') return `I found ${n} close matches for ${what}. Matching by likeness ranks rather than counts, so this isn’t an exact number.`;
  if (p.intent === 'last') return `The most recent was on ${longDay(r.newest)}. There ${plural(r.total, 'is', 'are')} ${n} ${what} in all.`;
  if (p.intent === 'first') return `The earliest was on ${longDay(r.oldest)}. There ${plural(r.total, 'is', 'are')} ${n} ${what} in all.`;
  if (r.order === 'relevance' && r.total === 1) return `Here is the one match for ${describe(p)}, from ${longDay(r.newest)}.`;
  if (r.order === 'relevance') {
    const span = r.oldest !== '' && r.oldest !== r.newest ? ` They range from ${longDay(r.oldest)} to ${longDay(r.newest)}.` : '';
    return `Here are the ${n} best matches for ${what}, closest first.${span}`;
  }
  const span = r.oldest !== '' && r.oldest !== r.newest ? ', going back to ' + longDay(r.oldest) : '';
  return `Here ${plural(r.total, 'is', 'are')} ${n} ${what}. The newest is from ${longDay(r.newest)}${span}.`;
}

/** Lists the chip keys worth trying without, when a question finds nothing. */
export function relaxable(p: Plan): string[] {
  const on: Record<string, boolean> = { type: p.type !== '', when: p.when !== '', model: p.model !== '', source: p.source !== '', prompt: p.prompt !== '',
    favorite: p.favorite, people: p.people.length > 0, ocr: p.ocr !== '', filename: p.filename !== '', label: p.label !== '',
    character: p.character !== null, text: p.text !== '' };
  return ['type', 'when', 'favorite', 'label', 'model', 'source', 'prompt', 'ocr', 'filename', 'people', 'character', 'text'].filter((k) => on[k]);
}

// ---- the plan as JSON, as the server sends and takes it ----

/** The plan as the server writes it: Go's field order, empty values left out. */
export function planJSON(p: Plan): AskPlan {
  const o: AskPlan = { intent: p.intent };
  if (p.type !== '') o.type = p.type;
  if (p.people.length > 0) o.people = p.people.map((r) => ({ id: r.id, name: r.name }));
  if (p.character !== null) o.character = { id: p.character.id, name: p.character.name };
  if (p.model !== '') o.model = p.model;
  if (p.modelIds.length > 0) o.modelIds = [...p.modelIds];
  if (p.source !== '') o.source = p.source;
  if (p.prompt !== '') o.prompt = p.prompt;
  if (p.ocr !== '') o.ocr = p.ocr;
  if (p.filename !== '') o.filename = p.filename;
  if (p.label !== '') o.label = p.label;
  if (p.favorite) o.favorite = true;
  if (p.after !== '') o.after = p.after;
  if (p.before !== '') o.before = p.before;
  if (p.when !== '') o.when = p.when;
  if (p.aiOnly) o.aiOnly = true;
  if (p.text !== '') o.text = p.text;
  return o;
}

// ---- the request, as the server reads it ----
//
// The server's API library (huma) checks a request body against the schema it makes from the Go
// types (askInput in api/ask.go, and ask.Plan), and then encoding/json decodes the body into them;
// whatever either refuses is a 422, "validation failed". Both find a property by its name in any
// case ("PREV", or "ſource" by Go's case folding), and null is the same as leaving an optional
// property out. huma refuses properties it doesn't know, and checks the types, enums and limits of
// those it finds (one spelling of each); encoding/json then decodes every spelling in turn, into
// what's already there: the last string wins, and an object or a list is filled in rather than
// replaced (its legacy merge, which keeps even a list's earlier items for the next list to fill).
// Ids are UUIDs as github.com/google/uuid reads them, and are sent back in their usual form.

type Shape =
  | { kind: 'string'; max?: number; oneOf?: string[] }
  | { kind: 'uuid' } // a string to huma, which uuid.UUID decodes
  | { kind: 'boolean' }
  | { kind: 'list'; of: Shape; max?: number } // huma lets any list be null
  | { kind: 'struct'; fields: Field[] };
type Field = { name: string; shape: Shape; required?: boolean; pointer?: boolean };

const stringShape: Shape = { kind: 'string' };
const namedShape: Shape = { kind: 'struct', fields: [{ name: 'id', shape: { kind: 'uuid' }, required: true }, { name: 'name', shape: stringShape, required: true }] };
const textFields = (...names: string[]): Field[] => names.map((name) => ({ name, shape: stringShape }));
const planShape: Shape = { kind: 'struct', fields: [
  { name: 'intent', shape: { kind: 'string', oneOf: ['list', 'count', 'first', 'last'] }, required: true },
  { name: 'type', shape: { kind: 'string', oneOf: ['image', 'video'] } },
  { name: 'people', shape: { kind: 'list', of: namedShape } },
  { name: 'character', shape: namedShape, pointer: true },
  ...textFields('model'),
  { name: 'modelIds', shape: { kind: 'list', of: { kind: 'uuid' } } },
  ...textFields('source', 'prompt', 'ocr', 'filename', 'label'),
  { name: 'favorite', shape: { kind: 'boolean' } },
  ...textFields('after', 'before', 'when'),
  { name: 'aiOnly', shape: { kind: 'boolean' } },
  ...textFields('text'),
] };
const askShape: Shape = { kind: 'struct', fields: [
  { name: 'q', shape: { kind: 'string', max: 300 }, required: true },
  { name: 'prev', shape: planShape, pointer: true },
  { name: 'exclude', shape: { kind: 'list', of: stringShape, max: 20 } },
] };

// Go's values: a list is a slice, whose array keeps the items past its length.
type GoList = { len: number; items: GoValue[] };
type GoStruct = { [field: string]: GoValue };
type GoValue = string | boolean | GoList | GoStruct | null;

const zeroUUID = '00000000-0000-0000-0000-000000000000';
function zero(s: Shape): GoValue {
  switch (s.kind) {
    case 'string':
      return '';
    case 'uuid':
      return zeroUUID;
    case 'boolean':
      return false;
    case 'list':
      return { len: 0, items: [] };
    case 'struct':
      return Object.fromEntries(s.fields.map((f) => [f.name, f.pointer ? null : zero(f.shape)]));
  }
}

const isObject = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
// strings.EqualFold with a field's name (all ASCII): any case, and k and s also as the Kelvin sign and the long s.
const foldEq = (key: string, name: string) => {
  const k = Array.from(key);
  return k.length === name.length && k.every((c, i) => {
    const n = name[i].toLowerCase();
    return (c < '\x80' && c.toLowerCase() === n) || (n === 'k' && c === '\u212a') || (n === 's' && c === '\u017f');
  });
};
// The field a property names: its own name first, else in another case.
const fieldFor = (fields: Field[], key: string) => fields.find((f) => f.name === key) ?? fields.find((f) => foldEq(key, f.name));
// Go reads JSON's strings as UTF-8: a lone surrogate (an escape like \ud800) is U+FFFD.
const wellFormed = (s: string) => s.replace(/[\ud800-\udbff][\udc00-\udfff]|[\ud800-\udfff]/g, (c) => (c.length === 2 ? c : '\ufffd'));

// valid is huma's check. Of the spellings of a property, it checks the exact one, else the first
// (Go's map order makes that any of them).
function valid(s: Shape, v: unknown): boolean {
  switch (s.kind) {
    case 'boolean':
      return typeof v === 'boolean';
    case 'uuid':
      return typeof v === 'string';
    case 'string':
      return typeof v === 'string' && (s.max === undefined || Array.from(v).length <= s.max) && (s.oneOf === undefined || s.oneOf.includes(v));
    case 'list':
      return v === null || (Array.isArray(v) && (s.max === undefined || v.length <= s.max) && v.every((x) => valid(s.of, x)));
    case 'struct':
      return isObject(v) && Object.keys(v).every((k) => fieldFor(s.fields, k)) && s.fields.every((f) => {
        const k = Object.hasOwn(v, f.name) ? f.name : Object.keys(v).find((x) => foldEq(x, f.name));
        return k === undefined ? !f.required : (v[k] === null && !f.required) || valid(f.shape, v[k]);
      });
  }
}

class Refused extends Error {}

// decode is encoding/json's: null leaves a value as it was (a list or a pointer becomes nil), and
// a value of the wrong type is an error.
function decode(s: Shape, v: unknown, cur: GoValue): GoValue {
  if (v === null) return s.kind === 'list' ? { len: 0, items: [] } : cur;
  switch (s.kind) {
    case 'boolean':
      if (typeof v !== 'boolean') throw new Refused();
      return v;
    case 'string':
      if (typeof v !== 'string') throw new Refused();
      return wellFormed(v);
    case 'uuid': {
      const id = typeof v === 'string' ? parseUUID(v) : null;
      if (id === null) throw new Refused();
      return id;
    }
    case 'list': {
      if (!Array.isArray(v)) throw new Refused();
      if (v.length === 0) return { len: 0, items: [] };
      const items = [...(cur as GoList).items];
      v.forEach((x, i) => (items[i] = decode(s.of, x, i < items.length ? items[i] : zero(s.of))));
      return { len: v.length, items };
    }
    case 'struct': {
      if (!isObject(v)) throw new Refused();
      const o = { ...(cur as GoStruct) };
      for (const [k, x] of Object.entries(v)) {
        const f = fieldFor(s.fields, k);
        if (f?.pointer) o[f.name] = x === null ? null : decode(f.shape, x, o[f.name] ?? zero(f.shape));
        else if (f) o[f.name] = decode(f.shape, x, o[f.name]);
      }
      return o;
    }
  }
}

// parseUUID is uuid.ParseBytes: 36 characters with dashes, or in braces (any two around it), or as
// a URN, or 32 hex digits; any case. It gives the usual form, or null.
function parseUUID(s: string): string | null {
  let b = utf8.encode(s);
  const hex = (at: number[]) => {
    let out = '';
    for (const i of at) {
      const pair = String.fromCharCode(b[i], b[i + 1]);
      if (!/^[0-9a-fA-F]{2}$/.test(pair)) return null;
      out += pair.toLowerCase();
    }
    return out;
  };
  if (b.length === 32) return uuidForm(hex([0, 2, 4, 6, 8, 10, 12, 14, 16, 18, 20, 22, 24, 26, 28, 30]));
  if (b.length === 36 + 9 && String.fromCharCode(...b.subarray(0, 9)).toLowerCase() === 'urn:uuid:') b = b.subarray(9);
  else if (b.length === 36 + 2) b = b.subarray(1);
  else if (b.length !== 36) return null;
  if (b[8] !== 0x2d || b[13] !== 0x2d || b[18] !== 0x2d || b[23] !== 0x2d) return null;
  return uuidForm(hex([0, 2, 4, 6, 9, 11, 14, 16, 19, 21, 24, 26, 28, 30, 32, 34]));
}
const uuidForm = (d: string | null) => d && `${d.slice(0, 8)}-${d.slice(8, 12)}-${d.slice(12, 16)}-${d.slice(16, 20)}-${d.slice(20)}`;

/** An Ask request as the server takes it: the question, the plan it follows up (prev), and the chip keys to drop. */
export type AskRequest = { q: string; prev: Plan | null; exclude: string[] };

/** Reads an Ask request's body as the server does, or null when the server would refuse it (422, validation failed). */
export function askRequest(body: unknown): AskRequest | null {
  if (!valid(askShape, body)) return null;
  let b: GoStruct;
  try {
    b = decode(askShape, body, zero(askShape)) as GoStruct;
  } catch (e) {
    if (e instanceof Refused) return null;
    throw e;
  }
  const list = (x: GoValue) => (x as GoList).items.slice(0, (x as GoList).len);
  const named = (x: GoValue): Named => ({ id: (x as GoStruct).id as string, name: (x as GoStruct).name as string });
  const p = b.prev as GoStruct | null;
  const prev = p && ({ ...p, people: list(p.people).map(named), character: p.character && named(p.character), modelIds: list(p.modelIds) } as Plan);
  return { q: b.q as string, prev, exclude: list(b.exclude) as string[] };
}
