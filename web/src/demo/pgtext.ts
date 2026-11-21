// Postgres full-text search, as Ask's prompt and picture-text filters use it
// (server/internal/store/queries/ask.sql): to_tsvector(cfg, doc) @@ websearch_to_tsquery(cfg, q),
// with 'english' for prompts and 'simple' for the text read from a picture. A port of Postgres 18
// (the server's, deploy/docker) in a database made with --locale=C.UTF-8: the default parser
// (wparser_def.c), the simple and english_stem dictionaries (Snowball's English stemmer and its
// stop words), websearch_to_tsquery (tsquery.c, to_tsany.c, tsquery_cleanup.c) and @@
// (tsvector_op.c). So "houses" finds "house", "$24.00" is one word, "hand-painted" is a word and
// its parts, and "-word", "or" and "a phrase" mean what they mean to the server.
//
// Letters, digits and spaces are classed as glibc's C.UTF-8 does, by Unicode property here: the
// two agree on every character of the Unicode version they share.

export type Config = 'english' | 'simple';

// ---- characters ----

const reAlpha = /[\p{Alphabetic}\p{Nd}]/u; // iswalpha: letters, and digits other than 0-9
const reZeroWidth = /[\p{Mn}\p{Me}\p{Cf}]/u; // pg_dsplen 0
const isDigit = (c: number) => c >= 48 && c <= 57;
const isAlpha = (c: number) => (c < 128 ? (c | 32) >= 97 && (c | 32) <= 122 : reAlpha.test(String.fromCodePoint(c)));
const isAlnum = (c: number) => isDigit(c) || isAlpha(c);
const isXdigit = (c: number) => isDigit(c) || ((c | 32) >= 97 && (c | 32) <= 102);
const isSpace = (c: number) => c === 32 || (c >= 9 && c <= 13) || c === 0x1680 || (c >= 0x2000 && c <= 0x200a && c !== 0x2007) ||
  c === 0x2028 || c === 0x2029 || c === 0x205f || c === 0x3000;
// Inside a word but not a letter: zero-width marks, and the signs wparser_def.c lists (most of them are letters already).
const isSpecial = (c: number) => c === 0 || reZeroWidth.test(String.fromCodePoint(c)) || [0xf3e, 0xf3f, 0x1b44, 0x1baa, 0xa953].includes(c);
const isUrlChar = (c: number) => c > 0x20 && c < 0x7f && !'"<>\\^`{|}'.includes(String.fromCharCode(c));
// towlower, a letter at a time.
const lower = (s: string) =>
  Array.from(s, (ch) => {
    const l = ch === 'İ' ? 'i' : ch.toLowerCase();
    return [...l].length === 1 ? l : ch;
  }).join('');
const fromCodes = (codes: number[]) => codes.map((c) => String.fromCodePoint(c)).join('');
const utf8 = new TextEncoder();
const MAXSTRLEN = 2047; // bytes in a word

// ---- the default parser (wparser_def.c) ----

// Its state machine, from Postgres's table: per state, the tests tried in order, each written
// "test [FLAGS][>next state][:token type][!special]". A test is a character, a class (EOF, alpha,
// digit…) or * for anything.
const STATES: Record<string, string> = {
  Base: "EOF, < PUSH>InTagFirst, ignore >InSpace, asclet >InAsciiWord, alpha >InWord, digit >InUnsignedInt, - PUSH>InSignedIntFirst, + PUSH>InSignedIntFirst, & PUSH>InXMLEntityFirst, ~ PUSH>InFileTwiddle, / PUSH>InFileFirst, . PUSH>InPathFirstFirst, * >InSpace",
  InNumWord: "EOF BINGO>Base:numword, alnum >InNumWord, special >InNumWord, @ PUSH>InEmail, / PUSH>InFileFirst, . PUSH>InFileNext, - PUSH>InHyphenNumWordFirst, * BINGO>Base:numword",
  InAsciiWord: "EOF BINGO>Base:asciiword, asclet, . PUSH>InHostFirstDomain, . PUSH>InFileNext, - PUSH>InHostFirstAN, - PUSH>InHyphenAsciiWordFirst, _ PUSH>InHostFirstAN, @ PUSH>InEmail, : PUSH>InProtocolFirst, / PUSH>InFileFirst, digit PUSH>InHost, digit >InNumWord, alpha >InWord, special >InWord, * BINGO>Base:asciiword",
  InWord: "EOF BINGO>Base:word, alpha, special, digit >InNumWord, - PUSH>InHyphenWordFirst, * BINGO>Base:word",
  InUnsignedInt: "EOF BINGO>Base:uint, digit, . PUSH>InHostFirstDomain, . PUSH>InUDecimalFirst, e PUSH>InMantissaFirst, E PUSH>InMantissaFirst, - PUSH>InHostFirstAN, _ PUSH>InHostFirstAN, @ PUSH>InEmail, asclet PUSH>InHost, alpha >InNumWord, special >InNumWord, / PUSH>InFileFirst, * BINGO>Base:uint",
  InSignedIntFirst: "EOF POP, digit CLEAR>InSignedInt, * POP",
  InSignedInt: "EOF BINGO>Base:int, digit, . PUSH>InDecimalFirst, e PUSH>InMantissaFirst, E PUSH>InMantissaFirst, * BINGO>Base:int",
  InSpace: "EOF BINGO>Base:blank, < BINGO>Base:blank, ignore, - BINGO>Base:blank, + BINGO>Base:blank, & BINGO>Base:blank, / BINGO>Base:blank, notalnum >InSpace, * BINGO>Base:blank",
  InUDecimalFirst: "EOF POP, digit CLEAR>InUDecimal, * POP",
  InUDecimal: "EOF BINGO>Base:float, digit >InUDecimal, . PUSH>InVersionFirst, e PUSH>InMantissaFirst, E PUSH>InMantissaFirst, * BINGO>Base:float",
  InDecimalFirst: "EOF POP, digit CLEAR>InDecimal, * POP",
  InDecimal: "EOF BINGO>Base:float, digit >InDecimal, . PUSH>InVerVersion, e PUSH>InMantissaFirst, E PUSH>InMantissaFirst, * BINGO>Base:float",
  InVerVersion: "EOF POP, digit RERUN>InSVerVersion!VerVersion, * POP",
  InSVerVersion: "EOF POP, digit BINGO|CLRALL>InUnsignedInt:blank, *",
  InVersionFirst: "EOF POP, digit CLEAR>InVersion, * POP",
  InVersion: "EOF BINGO>Base:version, digit >InVersion, . PUSH>InVersionFirst, * BINGO>Base:version",
  InMantissaFirst: "EOF POP, digit CLEAR>InMantissa, + >InMantissaSign, - >InMantissaSign, * POP",
  InMantissaSign: "EOF POP, digit CLEAR>InMantissa, * POP",
  InMantissa: "EOF BINGO>Base:sfloat, digit >InMantissa, * BINGO>Base:sfloat",
  InXMLEntityFirst: "EOF POP, # >InXMLEntityNumFirst, asclet >InXMLEntity, : >InXMLEntity, _ >InXMLEntity, * POP",
  InXMLEntity: "EOF POP, alnum >InXMLEntity, : >InXMLEntity, _ >InXMLEntity, . >InXMLEntity, - >InXMLEntity, ; >InXMLEntityEnd, * POP",
  InXMLEntityNumFirst: "EOF POP, x >InXMLEntityHexNumFirst, X >InXMLEntityHexNumFirst, digit >InXMLEntityNum, * POP",
  InXMLEntityNum: "EOF POP, digit >InXMLEntityNum, ; >InXMLEntityEnd, * POP",
  InXMLEntityHexNumFirst: "EOF POP, xdigit >InXMLEntityHexNum, * POP",
  InXMLEntityHexNum: "EOF POP, xdigit >InXMLEntityHexNum, ; >InXMLEntityEnd, * POP",
  InXMLEntityEnd: "* BINGO|CLEAR>Base:entity",
  InTagFirst: "EOF POP, / PUSH>InTagCloseFirst, ! PUSH>InCommentFirst, ? PUSH>InXMLBegin, asclet PUSH>InTagName, : PUSH>InTagName, _ PUSH>InTagName, * POP",
  InXMLBegin: "EOF POP, x >InTag, * POP",
  InTagCloseFirst: "EOF POP, asclet >InTagName, * POP",
  InTagName: "EOF POP, / >InTagBeginEnd, > >InTagEnd!Tags, space >InTag!Tags, alnum, :, _, ., -, * POP",
  InTagBeginEnd: "EOF POP, > >InTagEnd, * POP",
  InTag: "EOF POP, > >InTagEnd!Tags, ' >InTagEscapeK, \" >InTagEscapeKK, asclet, digit, =, -, _, #, /, :, ., &, ?, %, ~, space !Tags, * POP",
  InTagEscapeK: "EOF POP, \\ PUSH>InTagBackSleshed, ' >InTag, * >InTagEscapeK",
  InTagEscapeKK: "EOF POP, \\ PUSH>InTagBackSleshed, \" >InTag, * >InTagEscapeKK",
  InTagBackSleshed: "EOF POP, * MERGE",
  InTagEnd: "* BINGO|CLRALL>Base:tag",
  InCommentFirst: "EOF POP, - >InCommentLast, D >InTag, d >InTag, * POP",
  InCommentLast: "EOF POP, - >InComment, * POP",
  InComment: "EOF POP, - >InCloseCommentFirst, *",
  InCloseCommentFirst: "EOF POP, - >InCloseCommentLast, * >InComment",
  InCloseCommentLast: "EOF POP, -, > >InCommentEnd, * >InComment",
  InCommentEnd: "* BINGO|CLRALL>Base:tag",
  InHostFirstDomain: "EOF POP, asclet >InHostDomainSecond, digit >InHost, * POP",
  InHostDomainSecond: "EOF POP, asclet >InHostDomain, digit PUSH>InHost, - PUSH>InHostFirstAN, _ PUSH>InHostFirstAN, . PUSH>InHostFirstDomain, @ PUSH>InEmail, * POP",
  InHostDomain: "EOF BINGO|CLRALL>Base:host, asclet >InHostDomain, digit PUSH>InHost, : PUSH>InPortFirst, - PUSH>InHostFirstAN, _ PUSH>InHostFirstAN, . PUSH>InHostFirstDomain, @ PUSH>InEmail, digit POP, stophost BINGO|CLRALL>InURLPathStart:host, / PUSH>InFURL, * BINGO|CLRALL>Base:host",
  InPortFirst: "EOF POP, digit >InPort, * POP",
  InPort: "EOF BINGO|CLRALL>Base:host, digit >InPort, stophost BINGO|CLRALL>InURLPathStart:host, / PUSH>InFURL, * BINGO|CLRALL>Base:host",
  InHostFirstAN: "EOF POP, digit >InHost, asclet >InHost, * POP",
  InHost: "EOF POP, digit >InHost, asclet >InHost, @ PUSH>InEmail, . PUSH>InHostFirstDomain, - PUSH>InHostFirstAN, _ PUSH>InHostFirstAN, * POP",
  InEmail: "stophost POP, host BINGO|CLRALL>Base:email, * POP",
  InFileFirst: "EOF POP, asclet >InFile, digit >InFile, . >InPathFirst, _ >InFile, ~ PUSH>InFileTwiddle, * POP",
  InFileTwiddle: "EOF POP, asclet >InFile, digit >InFile, _ >InFile, / >InFileFirst, * POP",
  InPathFirst: "EOF POP, asclet >InFile, digit >InFile, _ >InFile, . >InPathSecond, / >InFileFirst, * POP",
  InPathFirstFirst: "EOF POP, . >InPathSecond, / >InFileFirst, * POP",
  InPathSecond: "EOF BINGO|CLEAR>Base:file, / PUSH>InFileFirst, / BINGO|CLEAR>Base:file, space BINGO|CLEAR>Base:file, * POP",
  InFile: "EOF BINGO>Base:file, asclet >InFile, digit >InFile, . PUSH>InFileNext, _ >InFile, - >InFile, / PUSH>InFileFirst, * BINGO>Base:file",
  InFileNext: "EOF POP, asclet CLEAR>InFile, digit CLEAR>InFile, _ CLEAR>InFile, * POP",
  InURLPathFirst: "EOF POP, urlchar >InURLPath, * POP",
  InURLPathStart: "* >InURLPath",
  InURLPath: "EOF BINGO>Base:url_path, urlchar >InURLPath, * BINGO>Base:url_path",
  InFURL: "EOF POP, URLPath BINGO|CLRALL>Base:url!FURL, * POP",
  InProtocolFirst: "EOF POP, / >InProtocolSecond, * POP",
  InProtocolSecond: "EOF POP, / >InProtocolEnd, * POP",
  InProtocolEnd: "* BINGO|CLRALL>Base:protocol",
  InHyphenAsciiWordFirst: "EOF POP, asclet >InHyphenAsciiWord, alpha >InHyphenWord, digit >InHyphenDigitLookahead, * POP",
  InHyphenAsciiWord: "EOF BINGO|CLRALL>InParseHyphen:asciihword!Hyphen, asclet >InHyphenAsciiWord, alpha >InHyphenWord, special >InHyphenWord, digit >InHyphenNumWord, - PUSH>InHyphenAsciiWordFirst, * BINGO|CLRALL>InParseHyphen:asciihword!Hyphen",
  InHyphenWordFirst: "EOF POP, alpha >InHyphenWord, digit >InHyphenDigitLookahead, * POP",
  InHyphenWord: "EOF BINGO|CLRALL>InParseHyphen:hword!Hyphen, alpha >InHyphenWord, special >InHyphenWord, digit >InHyphenNumWord, - PUSH>InHyphenWordFirst, * BINGO|CLRALL>InParseHyphen:hword!Hyphen",
  InHyphenNumWordFirst: "EOF POP, alpha >InHyphenNumWord, digit >InHyphenDigitLookahead, * POP",
  InHyphenNumWord: "EOF BINGO|CLRALL>InParseHyphen:numhword!Hyphen, alnum >InHyphenNumWord, special >InHyphenNumWord, - PUSH>InHyphenNumWordFirst, * BINGO|CLRALL>InParseHyphen:numhword!Hyphen",
  InHyphenDigitLookahead: "EOF POP, digit >InHyphenDigitLookahead, alpha >InHyphenNumWord, special >InHyphenNumWord, * POP",
  InParseHyphen: "EOF RERUN>Base, asclet >InHyphenAsciiWordPart, alpha >InHyphenWordPart, digit PUSH>InHyphenUnsignedInt, - PUSH>InParseHyphenHyphen, * RERUN>Base",
  InParseHyphenHyphen: "EOF POP, alnum BINGO|CLEAR>InParseHyphen:blank, special BINGO|CLEAR>InParseHyphen:blank, * POP",
  InHyphenWordPart: "EOF BINGO>Base:hword_part, alpha >InHyphenWordPart, special >InHyphenWordPart, digit >InHyphenNumWordPart, * BINGO>InParseHyphen:hword_part",
  InHyphenAsciiWordPart: "EOF BINGO>Base:hword_asciipart, asclet >InHyphenAsciiWordPart, alpha >InHyphenWordPart, special >InHyphenWordPart, digit >InHyphenNumWordPart, * BINGO>InParseHyphen:hword_asciipart",
  InHyphenNumWordPart: "EOF BINGO>Base:hword_numpart, alnum >InHyphenNumWordPart, special >InHyphenNumWordPart, * BINGO>InParseHyphen:hword_numpart",
  InHyphenUnsignedInt: "EOF POP, digit, alpha CLEAR>InHyphenNumWordPart, special CLEAR>InHyphenNumWordPart, * POP",
};

const NAMES = Object.keys(STATES);
const BINGO = 1, POP = 2, PUSH = 4, RERUN = 8, CLEAR = 16, MERGE = 32, CLRALL = 64;
const FLAG: Record<string, number> = { BINGO, POP, PUSH, RERUN, CLEAR, MERGE, CLRALL };
type Item = { test: string; flags: number; to: number; type: string; special: string };
const TABLE: Item[][] = NAMES.map((name) =>
  STATES[name].split(', ').map((s) => {
    const sp = s.indexOf(' ');
    const test = sp < 0 ? s : s.slice(0, sp);
    const [, flags, to, type, special] = /^([A-Z|]*)(?:>(\w+))?(?::(\w+))?(?:!(\w+))?$/.exec(sp < 0 ? '' : s.slice(sp + 1))!;
    return {
      test, flags: flags.split('|').reduce((f, x) => f | (FLAG[x] ?? 0), 0), to: to ? NAMES.indexOf(to) : -1, type: type ?? '', special: special ?? '',
    };
  }),
);
const BASE = NAMES.indexOf('Base'), URL_PATH_FIRST = NAMES.indexOf('InURLPathFirst');

type Position = { pos: number; charlen: number; lenchars: number; state: number; prev: Position | null; pushedAt: number };

class TParser {
  state: Position;
  ignore = false;
  wanthost = false;
  token = 0; // where the token found starts
  lenchars = 0;
  type = '';

  constructor(readonly s: number[], start: number) {
    this.state = { pos: start, charlen: 0, lenchars: 0, state: BASE, prev: null, pushedAt: -1 };
  }

  private test(t: string): boolean {
    const st = this.state;
    const c = st.pos < this.s.length ? this.s[st.pos] : 0;
    switch (t) {
      case 'EOF': return st.pos === this.s.length || st.charlen === 0;
      case 'alnum': return isAlnum(c);
      case 'notalnum': return !isAlnum(c);
      case 'alpha': return isAlpha(c);
      case 'digit': return isDigit(c);
      case 'space': return isSpace(c);
      case 'xdigit': return isXdigit(c);
      case 'asclet': return c < 128 && isAlpha(c);
      case 'special': return isSpecial(c);
      case 'urlchar': return isUrlChar(c);
      case 'ignore': return this.ignore;
      case 'stophost':
        if (!this.wanthost) return false;
        this.wanthost = false;
        return true;
      case 'host': return this.sub(BASE, true, 'host');
      case 'URLPath': return this.sub(URL_PATH_FIRST, false, 'url_path');
      default: return c === t.charCodeAt(0);
    }
  }

  // p_ishost and p_isURLPath: a parser started here finds a whole host (or URL path) first.
  private sub(state: number, wanthost: boolean, type: string): boolean {
    const p = new TParser(this.s, this.state.pos);
    p.wanthost = wanthost;
    if (state !== BASE) p.state = { ...p.state, prev: p.state, state };
    if (!p.get() || p.type !== type) return false;
    this.state.pos += p.lenchars;
    this.state.lenchars += p.lenchars;
    this.state.charlen = p.state.charlen;
    return true;
  }

  private special(name: string) {
    const st = this.state;
    if (name === 'Tags') {
      // <script> and <style> start text to skip, until their closing tags
      const tag = st.lenchars <= 8 ? fromCodes(this.s.slice(this.token, this.token + st.lenchars)).replace(/[A-Z]/g, (x) => x.toLowerCase()) : '';
      if (tag === '</script' || tag === '</style') this.ignore = false;
      else if (tag === '<script' || tag === '<style') this.ignore = true;
      return;
    }
    if (name === 'FURL') this.wanthost = true;
    st.pos -= st.lenchars; // go back to parse the token again, in parts
    if (name === 'VerVersion') st.lenchars = 0;
  }

  /** TParserGet: finds the next token, its type and length. */
  get(): boolean {
    if (this.state.pos >= this.s.length) return false;
    this.token = this.state.pos;
    this.state.pushedAt = -1;
    let item: Item | null = null;
    while (this.state.pos <= this.s.length) {
      const st = this.state;
      st.charlen = st.pos === this.s.length ? 0 : 1;
      const items = TABLE[st.state];
      let i = 0;
      if (st.pushedAt >= 0) {
        i = st.pushedAt + 1; // after a POP, carry on from the next test
        st.pushedAt = -1;
      }
      while (items[i].test !== '*' && !this.test(items[i].test)) i++;
      item = items[i];
      if (item.special) this.special(item.special);
      if (item.flags & BINGO) {
        this.lenchars = this.state.lenchars;
        this.state.lenchars = 0;
        this.type = item.type;
      }
      if (item.flags & POP) {
        this.state = this.state.prev!;
      } else if (item.flags & PUSH) {
        this.state.pushedAt = i;
        this.state = { ...this.state, prev: this.state, pushedAt: -1 };
      } else if (item.flags & CLEAR) {
        this.state.prev = this.state.prev!.prev;
      } else if (item.flags & CLRALL) {
        this.state.prev = null;
      } else if (item.flags & MERGE) {
        const top = this.state;
        this.state = top.prev!;
        Object.assign(this.state, { pos: top.pos, charlen: top.charlen, lenchars: top.lenchars });
      }
      if (item.to >= 0) this.state.state = item.to;
      if (item.flags & BINGO || (this.state.pos >= this.s.length && !(item.flags & RERUN))) break;
      if (item.flags & (RERUN | POP)) continue;
      if (this.state.charlen) {
        this.state.pos++;
        this.state.lenchars++;
      }
    }
    return item !== null && (item.flags & BINGO) !== 0;
  }
}

// ---- Snowball's English stemmer (stem_UTF_8_english.c) ----

type Z = { p: string[]; c: number; l: number; lb: number; bra: number; ket: number; I: [number, number, number] };
const V = new Set('aeiouy'), V_WXY = new Set('aeiouywxY'), VALID_LI = new Set('cdeghkmnrt'), AEO = new Set('aeo');

// Groupings, one letter at the cursor (forwards) or before it (backwards); true moves past it.
const inG = (z: Z, g: Set<string>) => z.c < z.l && g.has(z.p[z.c]) && ++z.c > 0;
const inGB = (z: Z, g: Set<string>) => z.c > z.lb && g.has(z.p[z.c - 1]) && --z.c >= 0;
const outGB = (z: Z, g: Set<string>) => z.c > z.lb && !g.has(z.p[z.c - 1]) && --z.c >= 0;
// gopast: on to the first letter in (or out of) g, and past it.
function gopast(z: Z, g: Set<string>, inside: boolean): boolean {
  while (z.c < z.l && g.has(z.p[z.c]) !== inside) z.c++;
  return z.c < z.l && ++z.c > 0;
}
function gopastB(z: Z, g: Set<string>): boolean {
  while (z.c > z.lb && !g.has(z.p[z.c - 1])) z.c--;
  return z.c > z.lb && --z.c >= 0;
}
// among: the longest of the strings at the cursor (or ending at it), and its result.
function among(z: Z, a: Record<string, number>): number {
  let best = '';
  for (const s in a) if (s.length >= best.length && z.c + s.length <= z.l && z.p.slice(z.c, z.c + s.length).join('') === s) best = s;
  if (!(best in a)) return 0;
  z.c += best.length;
  return a[best];
}
function amongB(z: Z, a: Record<string, number>): number {
  let best: string | null = null;
  for (const s in a) if ((best === null || s.length > best.length) && z.c - s.length >= z.lb && z.p.slice(z.c - s.length, z.c).join('') === s) best = s;
  if (best === null) return 0;
  z.c -= best.length;
  return a[best];
}
// slice_from: the letters from bra to ket become s.
function sliceFrom(z: Z, s: string) {
  const adj = s.length - (z.ket - z.bra);
  z.p.splice(z.bra, z.ket - z.bra, ...s);
  if (adj !== 0) {
    z.l += adj;
    if (z.c >= z.ket) z.c += adj;
    else if (z.c > z.bra) z.c = z.bra;
  }
}

const A1 = { "'": 1, "'s'": 1, "'s": 1 };
const A2 = { ied: 2, s: 3, ies: 2, sses: 1, ss: -1, us: -1 };
const A3 = { '': 3, bb: 2, dd: 2, ff: 2, gg: 2, bl: 1, mm: 2, nn: 2, pp: 2, rr: 2, at: 1, tt: 2, iz: 1 };
const A4 = { ed: 2, eed: 1, ing: 2, edly: 2, eedly: 1, ingly: 2 };
const A5 = { anci: 3, enci: 2, ogi: 13, li: 15, bli: 12, abli: 4, alli: 8, fulli: 9, lessli: 14, ousli: 10, entli: 5, aliti: 8, biliti: 12,
  iviti: 11, tional: 1, ational: 7, alism: 8, ation: 7, ization: 6, izer: 6, ator: 7, iveness: 11, fulness: 9, ousness: 10 };
const A5_TO = ['', 'tion', 'ence', 'ance', 'able', 'ent', 'ize', 'ate', 'al', 'ful', 'ous', 'ive', 'ble'];
const A6 = { icate: 4, ative: 6, alize: 3, iciti: 4, ical: 4, tional: 1, ational: 2, ful: 5, ness: 5 };
const A7 = { ic: 1, ance: 1, ence: 1, able: 1, ible: 1, ate: 1, ive: 1, ize: 1, iti: 1, al: 1, ism: 1, ion: 2, er: 1, ous: 1, ant: 1, ent: 1,
  ment: 1, ement: 1 };
const A8 = { e: 1, l: 2 };
const A9 = { succeed: -1, proceed: -1, exceed: -1, canning: -1, inning: -1, earring: -1, herring: -1, outing: -1 };
const A10 = { andes: -1, atlas: -1, bias: -1, cosmos: -1, dying: 3, early: 9, gently: 7, howe: -1, idly: 6, lying: 4, news: -1, only: 10,
  singly: 11, skies: 2, skis: 1, sky: -1, tying: 5, ugly: 8 };
const A10_TO = ['', 'ski', 'sky', 'die', 'lie', 'tie', 'idl', 'gentl', 'ugli', 'earli', 'onli', 'singl'];

const R1 = (z: Z) => z.I[1] <= z.c;
const R2 = (z: Z) => z.I[0] <= z.c;

function prelude(z: Z) {
  z.I[2] = 0;
  let c = z.c;
  z.bra = z.c;
  if (z.c < z.l && z.p[z.c] === "'") {
    z.ket = ++z.c;
    sliceFrom(z, '');
  }
  z.c = c;
  z.bra = z.c;
  if (z.c < z.l && z.p[z.c] === 'y') {
    z.ket = ++z.c;
    sliceFrom(z, 'Y');
    z.I[2] = 1;
  }
  z.c = c;
  // a y after a vowel is a consonant: Y
  for (;;) {
    const c4 = z.c;
    let found = false;
    for (;;) {
      const c5 = z.c;
      if (inG(z, V)) {
        z.bra = z.c;
        if (z.c < z.l && z.p[z.c] === 'y') {
          z.ket = ++z.c;
          z.c = c5;
          found = true;
          break;
        }
      }
      z.c = c5;
      if (z.c >= z.l) break;
      z.c++;
    }
    if (!found) {
      z.c = c4;
      break;
    }
    sliceFrom(z, 'Y');
    z.I[2] = 1;
  }
  z.c = c;
}

function markRegions(z: Z) {
  z.I[1] = z.l;
  z.I[0] = z.l;
  const c1 = z.c;
  regions: {
    if (!among(z, { arsen: -1, commun: -1, gener: -1 }) && !(gopast(z, V, true) && gopast(z, V, false))) break regions;
    z.I[1] = z.c;
    if (!gopast(z, V, true) || !gopast(z, V, false)) break regions;
    z.I[0] = z.c;
  }
  z.c = c1;
}

function shortv(z: Z): boolean {
  const m = z.l - z.c;
  if (outGB(z, V_WXY) && inGB(z, V) && outGB(z, V)) return true;
  z.c = z.l - m;
  return outGB(z, V) && inGB(z, V) && z.c <= z.lb;
}

function step1a(z: Z) {
  const m = z.l - z.c;
  z.ket = z.c;
  if (amongB(z, A1)) {
    z.bra = z.c;
    sliceFrom(z, '');
  } else z.c = z.l - m;
  z.ket = z.c;
  const v = amongB(z, A2);
  if (!v) return;
  z.bra = z.c;
  if (v === 1) sliceFrom(z, 'ss');
  else if (v === 2) sliceFrom(z, z.c - 2 >= z.lb ? 'i' : 'ie');
  else if (v === 3) {
    if (z.c - 1 < z.lb) return;
    z.c--;
    if (!gopastB(z, V)) return;
    sliceFrom(z, '');
  }
}

function step1b(z: Z) {
  z.ket = z.c;
  const v = amongB(z, A4);
  if (!v) return;
  z.bra = z.c;
  if (v === 1) {
    if (R1(z)) sliceFrom(z, 'ee');
    return;
  }
  const m1 = z.l - z.c;
  if (!gopastB(z, V)) return;
  z.c = z.l - m1;
  sliceFrom(z, '');
  z.ket = z.bra = z.c;
  const m2 = z.l - z.c;
  const v2 = amongB(z, A3);
  if (v2 === 1) return sliceFrom(z, 'e');
  if (v2 === 2) {
    const m3 = z.l - z.c;
    if (inGB(z, AEO) && z.c <= z.lb) return;
    z.c = z.l - m3;
  } else if (v2 === 3) {
    if (z.c !== z.I[1]) return;
    const m4 = z.l - z.c;
    if (!shortv(z)) return;
    z.c = z.l - m4;
    return sliceFrom(z, 'e');
  }
  z.c = z.l - m2;
  z.ket = z.c;
  if (z.c - 1 < z.lb) return;
  z.bra = --z.c;
  sliceFrom(z, '');
}

function step1c(z: Z) {
  z.ket = z.c;
  if (z.c > z.lb && (z.p[z.c - 1] === 'y' || z.p[z.c - 1] === 'Y')) z.c--;
  else return;
  z.bra = z.c;
  if (outGB(z, V) && z.c > z.lb) sliceFrom(z, 'i');
}

function step2(z: Z) {
  z.ket = z.c;
  const v = amongB(z, A5);
  if (!v) return;
  z.bra = z.c;
  if (!R1(z)) return;
  if (v <= 12) sliceFrom(z, A5_TO[v]);
  else if (v === 13) {
    if (z.c > z.lb && z.p[z.c - 1] === 'l') sliceFrom(z, 'og');
  } else if (v === 14) sliceFrom(z, 'less');
  else if (inGB(z, VALID_LI)) sliceFrom(z, '');
}

function step3(z: Z) {
  z.ket = z.c;
  const v = amongB(z, A6);
  if (!v) return;
  z.bra = z.c;
  if (!R1(z)) return;
  if (v === 1) sliceFrom(z, 'tion');
  else if (v === 2) sliceFrom(z, 'ate');
  else if (v === 3) sliceFrom(z, 'al');
  else if (v === 4) sliceFrom(z, 'ic');
  else if (v === 5) sliceFrom(z, '');
  else if (R2(z)) sliceFrom(z, '');
}

function step4(z: Z) {
  z.ket = z.c;
  const v = amongB(z, A7);
  if (!v) return;
  z.bra = z.c;
  if (!R2(z)) return;
  if (v === 1 || (z.c > z.lb && (z.p[z.c - 1] === 's' || z.p[z.c - 1] === 't'))) sliceFrom(z, '');
}

function step5(z: Z) {
  z.ket = z.c;
  const v = amongB(z, A8);
  if (!v) return;
  z.bra = z.c;
  if (v === 1) {
    if (!R2(z)) {
      if (!R1(z)) return;
      const m = z.l - z.c;
      if (shortv(z)) return;
      z.c = z.l - m;
    }
    sliceFrom(z, '');
  } else if (R2(z) && z.c > z.lb && z.p[z.c - 1] === 'l') sliceFrom(z, '');
}

function postlude(z: Z) {
  if (!z.I[2]) return;
  for (;;) {
    const c1 = z.c;
    while (z.c < z.l && z.p[z.c] !== 'Y') z.c++;
    if (z.c >= z.l) {
      z.c = c1;
      return;
    }
    z.bra = z.c;
    z.ket = z.c + 1;
    sliceFrom(z, 'y');
  }
}

/** Snowball's English stemmer, on a lower-case word. */
export function stemEnglish(word: string): string {
  const p = Array.from(word);
  const z: Z = { p, c: 0, l: p.length, lb: 0, bra: 0, ket: 0, I: [0, 0, 0] };
  // exception1: a few whole words
  z.bra = 0;
  const e = among(z, A10);
  if (e && z.c === z.l) {
    z.ket = z.c;
    if (e > 0) sliceFrom(z, A10_TO[e]);
    return z.p.join('');
  }
  if (z.l < 3) return word;
  z.c = 0;
  prelude(z);
  markRegions(z);
  z.lb = z.c;
  z.c = z.l;
  const steps = [step1b, step1c, step2, step3, step4, step5];
  let m = z.l - z.c;
  step1a(z);
  z.c = z.l - m;
  // exception2: left alone after step 1a
  m = z.l - z.c;
  z.ket = z.c;
  const whole = amongB(z, A9) !== 0 && z.c <= z.lb;
  z.c = z.l - m;
  if (!whole) {
    for (const step of steps) {
      m = z.l - z.c;
      step(z);
      z.c = z.l - m;
    }
  }
  z.c = z.lb;
  postlude(z);
  return z.p.join('');
}

// ---- dictionaries and configurations ----

// english_stem's stop words (Postgres's english.stop).
const STOP = new Set(`i me my myself we our ours ourselves you your yours yourself yourselves he him his himself she her hers herself it its
  itself they them their theirs themselves what which who whom this that these those am is are was were be been being have has had having do
  does did doing a an the and but if or because as until while of at by for with about against between into through during before after above
  below to from up down in out on off over under again further then once here there when where why how all any both each few more most other
  some such no nor not only own same so than too very s t can will just don should now`.split(/\s+/));

// Token types and the dictionary each configuration looks them up in (tag, blank, entity and protocol aren't indexed).
const STEMMED = new Set(['asciiword', 'word', 'hword_part', 'hword_asciipart', 'asciihword', 'hword']);
const SIMPLE = new Set(['numword', 'email', 'url', 'host', 'sfloat', 'version', 'hword_numpart', 'numhword', 'url_path', 'file', 'float', 'int', 'uint']);

/** A token's lexeme in a configuration: null for a stop word, undefined when the type isn't indexed. */
function lexize(cfg: Config, type: string, token: string): string | null | undefined {
  if (!STEMMED.has(type) && !SIMPLE.has(type)) return undefined;
  const txt = lower(token);
  if (cfg === 'simple' || SIMPLE.has(type)) return txt === '' ? null : txt;
  if (utf8.encode(token).length > 1000) return txt;
  if (txt === '' || STOP.has(txt)) return null;
  return stemEnglish(txt) || txt;
}

/** parsetext: each lexeme and its position (stop words take a position too). */
function parseText(cfg: Config, text: string): { word: string; pos: number }[] {
  const s = Array.from(text, (ch) => ch.codePointAt(0)!);
  const p = new TParser(s, 0);
  const words: { word: string; pos: number }[] = [];
  let pos = 0;
  while (p.get()) {
    const token = fromCodes(s.slice(p.token, p.token + p.lenchars));
    if (utf8.encode(token).length > MAXSTRLEN) continue; // "word is too long to be indexed"
    const lex = lexize(cfg, p.type, token);
    if (lex === undefined) continue;
    pos++;
    if (lex !== null && utf8.encode(lex).length <= MAXSTRLEN) words.push({ word: lex, pos: Math.min(pos, 16383) });
  }
  return words;
}

/** to_tsvector: each lexeme's positions. */
function toTsvector(cfg: Config, text: string): Map<string, number[]> {
  const v = new Map<string, number[]>();
  for (const { word, pos } of parseText(cfg, text).sort((a, b) => a.pos - b.pos)) {
    const at = v.get(word);
    if (!at) v.set(word, [pos]);
    else if (at.length < 255 && at[at.length - 1] !== 16383 && at[at.length - 1] !== pos) at.push(pos);
  }
  return v;
}

// ---- websearch_to_tsquery ----

type Op = 'not' | 'and' | 'or' | 'phrase';
type Q = { val: string } | { stop: true } | { op: Op; distance: number; left: Q | null; right: Q };
const PRIORITY: Record<Op, number> = { not: 4, phrase: 3, and: 2, or: 1 };
const isOperator = (ch: string) => '!&|()<'.includes(ch) && ch !== '';
const isAsciiSpace = (ch: string) => ch !== '' && ' \t\n\v\f\r'.includes(ch);

function websearchToTsquery(cfg: Config, query: string): Q | null {
  const b = Array.from(query);
  const at = (i: number) => (i < b.length ? b[i] : '');
  let i = 0;
  let waiting: 'first' | 'operand' | 'operator' = 'first';
  // Postfix, as Postgres builds it: operands, then their operator.
  const pol: (Q | { op: Op; distance: number })[] = [];
  const opstack: Op[] = [];
  const pushOp = (op: Op, distance = 0) => pol.push({ op, distance });
  const cleanOpStack = (op: Op) => {
    while (opstack.length) {
      const top = opstack[opstack.length - 1];
      if ((op !== 'not' && PRIORITY[op] > PRIORITY[top]) || (op === 'not' && PRIORITY[op] >= PRIORITY[top])) break;
      pushOp(opstack.pop()!);
    }
  };
  // pushval_morph: a word or a quoted phrase, as its lexemes in a phrase (a stop word keeps its place).
  const pushval = (text: string) => {
    const words = parseText(cfg, text);
    if (!words.length) return pol.push({ stop: true });
    let pos = 0, cntpos = 0;
    for (let k = 0; k < words.length; ) {
      if (pos > 0) {
        for (; pos + 1 < words[k].pos; pos++, cntpos++) {
          pol.push({ stop: true });
          if (cntpos) pushOp('phrase', 1);
        }
      }
      pos = words[k].pos;
      for (let cnt = 0; k < words.length && words[k].pos === pos; cnt++, k++) {
        pol.push({ val: words[k].word });
        if (cnt) pushOp('and');
      }
      if (cntpos) pushOp('phrase', 1);
      cntpos++;
    }
  };
  // parse_or_operator: "or" that isn't part of a word, with something after it.
  const orAhead = () => {
    if (!/^[oO][rR]$/.test(at(i) + at(i + 1))) return false;
    let p = i + 2;
    const c = at(p);
    if (c === '' || c === '-' || c === '_' || isAlnum(c.codePointAt(0)!)) return false;
    do {
      p++;
      if (at(p) === '') return false;
    } while (isAsciiSpace(at(p)));
    i += 2;
    return true;
  };
  for (;;) {
    const ch = at(i);
    let token: { val: string } | { op: Op } | null = null;
    if (waiting !== 'operator') {
      if (ch === '-') {
        i++;
        waiting = 'operand';
        token = { op: 'not' };
      } else if (ch === '"') {
        const start = ++i;
        while (at(i) !== '' && at(i) !== '"') i++;
        token = { val: b.slice(start, i).join('') };
        if (at(i) !== '') i++;
        waiting = 'operator';
      } else if (isOperator(ch)) {
        i++;
        waiting = 'operand';
        continue;
      } else if (!isAsciiSpace(ch)) {
        if (ch === '') {
          if (waiting === 'operand') pol.push({ stop: true }); // an operand is still owed
          break;
        }
        // gettoken_tsvector: up to a space, an operator, a quote or a colon
        const start = i++;
        while (at(i) !== '' && !isAsciiSpace(at(i)) && !isOperator(at(i)) && at(i) !== '"' && at(i) !== ':') i++;
        token = { val: b.slice(start, i).join('') };
        waiting = 'operator';
      }
    } else {
      if (ch === '') break;
      if (orAhead()) {
        waiting = 'operand';
        token = { op: 'or' };
      } else if (isOperator(ch)) {
        i++;
        continue;
      } else if (!isAsciiSpace(ch)) {
        waiting = 'operand';
        token = { op: 'and' };
      }
    }
    if (!token) {
      i++;
      continue;
    }
    if ('val' in token) pushval(token.val);
    else {
      cleanOpStack(token.op);
      opstack.push(token.op);
    }
  }
  cleanOpStack('or');
  if (!pol.length) return null;
  // From Polish notation to a tree.
  const stack: Q[] = [];
  for (const it of pol) {
    if (!('op' in it) || 'right' in it) stack.push(it as Q);
    else {
      const right = stack.pop()!;
      const left = it.op === 'not' ? null : stack.pop()!;
      stack.push({ op: it.op, distance: it.distance, left, right });
    }
  }
  return cleanStopwords(stack.pop()!);
}

// clean_stopword_intree: takes the stop words out, keeping the distances they stood for in phrases.
function cleanStopwords(root: Q): Q | null {
  const clean = (n: Q): [Q | null, number, number] => {
    if ('val' in n) return [n, 0, 0];
    if ('stop' in n) return [null, 0, 0];
    if (n.op === 'not') {
      const [r, l, rr] = clean(n.right);
      return r ? [{ ...n, right: r }, l, rr] : [null, l, rr];
    }
    const [left, lladd, lradd] = clean(n.left!);
    const [right, rladd, rradd] = clean(n.right);
    const isPhrase = n.op === 'phrase';
    const d = isPhrase ? n.distance : 0;
    if (!left && !right) {
      const add = isPhrase ? lladd + d + rladd : Math.max(lladd, rladd);
      return [null, add, add];
    }
    if (!left) return isPhrase ? [right, lladd + d + rladd, rradd] : [right, rladd, rradd];
    if (!right) return isPhrase ? [left, lladd, lradd + d + rradd] : [left, lladd, lradd];
    if (isPhrase) return [{ ...n, left, right, distance: n.distance + lradd + rladd }, lladd, rradd];
    return [{ ...n, left, right }, 0, 0];
  };
  return clean(root)[0];
}

// ---- @@ (TS_execute) ----

type Phrase = { pos: number[]; negate: boolean; width: number };

function execute(n: Q, v: Map<string, number[]>): boolean {
  if ('val' in n) return v.has(n.val);
  if ('stop' in n) return false;
  switch (n.op) {
    case 'not': return !execute(n.right, v);
    case 'and': return execute(n.left!, v) && execute(n.right, v);
    case 'or': return execute(n.left!, v) || execute(n.right, v);
    default: return phrase(n, v, { pos: [], negate: false, width: 0 });
  }
}

// TS_phrase_output: merges the two sides' positions, keeping those emit asks for.
const L_ONLY = 1, R_ONLY = 2, BOTH = 4;
function output(data: Phrase, l: Phrase, r: Phrase, emit: number, loffset: number, roffset: number): boolean {
  let li = 0, ri = 0;
  while (li < l.pos.length || ri < r.pos.length) {
    let lpos: number, rpos: number;
    if (li < l.pos.length) lpos = l.pos[li] + loffset;
    else if (emit & R_ONLY) lpos = Infinity;
    else break;
    if (ri < r.pos.length) rpos = r.pos[ri] + roffset;
    else if (emit & L_ONLY) rpos = Infinity;
    else break;
    let out = 0;
    if (lpos < rpos) {
      if (emit & L_ONLY) out = lpos;
      li++;
    } else if (lpos === rpos) {
      if (emit & BOTH) out = rpos;
      li++;
      ri++;
    } else {
      if (emit & R_ONLY) out = rpos;
      ri++;
    }
    if (out > 0) data.pos.push(out);
  }
  return data.pos.length > 0;
}

// TS_phrase_execute: whether a phrase is there, and where (positions where it ends).
function phrase(n: Q, v: Map<string, number[]>, data: Phrase): boolean {
  if ('val' in n) {
    const at = v.get(n.val);
    if (!at) return false;
    data.pos = [...at];
    return true;
  }
  if ('stop' in n) return false;
  const l: Phrase = { pos: [], negate: false, width: 0 }, r: Phrase = { pos: [], negate: false, width: 0 };
  switch (n.op) {
    case 'not':
      if (!phrase(n.right, v, data)) {
        data.negate = true; // matches everywhere
        return true;
      }
      if (data.pos.length > 0) {
        data.negate = !data.negate;
        return true;
      }
      data.negate = false;
      return false;
    case 'phrase':
    case 'and': {
      if (!phrase(n.left!, v, l) || !phrase(n.right, v, r)) return false;
      let loffset: number, roffset: number;
      if (n.op === 'phrase') {
        loffset = n.distance + r.width;
        roffset = 0;
        data.width = n.distance + l.width + r.width;
      } else {
        data.width = Math.max(l.width, r.width);
        loffset = data.width - l.width;
        roffset = data.width - r.width;
      }
      if (l.negate && r.negate) {
        output(data, l, r, BOTH | L_ONLY | R_ONLY, loffset, roffset);
        data.negate = true;
        return true;
      }
      if (l.negate) return output(data, l, r, R_ONLY, loffset, roffset);
      if (r.negate) return output(data, l, r, L_ONLY, loffset, roffset);
      return output(data, l, r, BOTH, loffset, roffset);
    }
    case 'or': {
      const lm = phrase(n.left!, v, l), rm = phrase(n.right, v, r);
      if (!lm && !rm) return false;
      if (!lm) l.width = 0;
      if (!rm) r.width = 0;
      data.width = Math.max(l.width, r.width);
      const loffset = data.width - l.width, roffset = data.width - r.width;
      if (l.negate && r.negate) output(data, l, r, BOTH, loffset, roffset);
      else if (l.negate) output(data, l, r, L_ONLY, loffset, roffset);
      else if (r.negate) output(data, l, r, R_ONLY, loffset, roffset);
      else return output(data, l, r, BOTH | L_ONLY | R_ONLY, loffset, roffset);
      data.negate = true;
      return true;
    }
  }
}

// ---- the match ----

const vectors = new Map<string, Map<string, number[]>>();
const queries = new Map<string, Q | null>();

/** to_tsvector(cfg, doc) @@ websearch_to_tsquery(cfg, query). */
export function textSearch(cfg: Config, doc: string, query: string): boolean {
  let v = vectors.get(cfg + '\0' + doc);
  if (!v) vectors.set(cfg + '\0' + doc, (v = toTsvector(cfg, doc)));
  let q = queries.get(cfg + '\0' + query);
  if (q === undefined) queries.set(cfg + '\0' + query, (q = websearchToTsquery(cfg, query)));
  return q !== null && execute(q, v);
}

/**
 * text ILIKE pattern, as Postgres does it in a C.UTF-8 database: both lower-cased a letter at a
 * time (towlower), then LIKE: % is any run of letters, _ any one, and \ takes the next literally.
 * Postgres refuses a pattern that ends in a lone \ (the query fails); that's false here.
 */
export function ilike(text: string, pattern: string): boolean {
  const literal = (c: string) => c.replace(/[\\^$.*+?()[\]{}|/]/g, '\\$&');
  const p = Array.from(lower(pattern));
  let re = '';
  for (let i = 0; i < p.length; i++) {
    if (p[i] === '\\') {
      if (++i === p.length) return false;
      re += literal(p[i]);
    } else re += p[i] === '%' ? '[^]*' : p[i] === '_' ? '[^]' : literal(p[i]);
  }
  return new RegExp(`^${re}$`, 'u').test(lower(text));
}
