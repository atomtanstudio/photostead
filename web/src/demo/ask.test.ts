import { describe, expect, it } from 'vitest';
import { askRequest, chips, describe as describePlan, parse, planJSON, trimSpace } from './ask';
import { isDay, loadLocation, nowIn } from './gotime';
import { stemEnglish, textSearch } from './pgtext';

// Expected values are the server's: server/internal/ask run on the same instants, and Postgres.

const none = { people: [], characters: [], models: [] };
const dates = (zone: string, at: string, q: string) => {
  const p = parse(q, nowIn(zone, new Date(at)), none);
  return [p.after, p.before, p.when];
};

describe('dates, as Go works them out', () => {
  it('moves a midnight that a clock change skips, as time.Date does', () => {
    // Santiago's clocks went from 00:00 to 01:00 on Sunday 6 September 2026.
    expect(dates('America/Santiago', '2026-09-06T12:00:00Z', 'today')).toEqual(['2026-09-05', '', 'today']);
    expect(dates('America/Santiago', '2026-09-06T12:00:00Z', 'yesterday')).toEqual(['2026-09-05', '2026-09-05', 'yesterday']);
    expect(dates('America/Santiago', '2026-09-14T15:00:00Z', 'last week')).toEqual(['2026-09-05', '2026-09-12', 'last week']);
    expect(dates('America/Havana', '2026-03-08T15:00:00Z', 'this week')).toEqual(['2026-03-07', '', 'this week']);
  });

  it('counts with int64, as strconv.Atoi and AddDate do', () => {
    const at = '2026-10-22T17:00:00Z';
    expect(dates('America/Los_Angeles', at, 'last 99999999999999999999 days')).toEqual(['2026-10-23', '', 'the last 9223372036854775807 days']);
    expect(dates('America/Los_Angeles', at, 'past 18446744073709551616 years')).toEqual(['2027-10-21', '', 'the last 9223372036854775807 years']);
    expect(dates('America/Los_Angeles', at, 'last 9007199254740993 days')).toEqual(['-109603882209-09-15', '', 'the last 9007199254740993 days']);
    expect(dates('America/Los_Angeles', at, 'last 274000 years')).toEqual(['-271974-10-22', '', 'the last 274000 years']);
    expect(dates('America/Los_Angeles', at, 'last summer')).toEqual(['2026-06-01', '2026-09-01', 'summer 2026']);
  });

  it('uses UTC for a zone Go would not load', () => {
    expect(loadLocation('america/los_angeles')).toBeNull();
    expect(loadLocation('+05:00')).toBeNull();
    expect(loadLocation('US/Pacific')).toBe('US/Pacific');
    expect(loadLocation('')).toBe('');
    // Names Intl knows that the tz database doesn't, and those Go finds as files.
    for (const name of ['US/Pacific-New', 'PST', 'SystemV/AST4', 'Cet', 'US//Pacific', 'Europe/Paris/', 'Europe/../Europe/Paris', '/Europe/Paris']) expect(loadLocation(name)).toBeNull();
    expect(loadLocation('Europe//Paris')).toBe('Europe/Paris');
    expect(loadLocation('./Europe/Paris')).toBe('Europe/Paris');
    expect(loadLocation('posixrules')).toBe('America/New_York');
    expect(loadLocation('Factory')).toBe('');
    expect(dates('america/los_angeles', '2027-01-01T03:00:00Z', 'this year')).toEqual(['2027-01-01', '2028-01-01', '2027']);
    expect(dates('+05:00', '2026-12-31T20:00:00Z', 'this year')).toEqual(['2026-01-01', '2027-01-01', '2026']);
  });

  it('reads a day as time.Parse does', () => {
    expect(isDay('2024-02-29')).toBe(true);
    expect(isDay('2025-02-29')).toBe(false);
    expect(isDay('2025-13-01')).toBe(false);
    expect(isDay('-271974-10-22')).toBe(false);
  });
});

describe('words, as Go trims them', () => {
  it('trims U+0085 and keeps U+FEFF', () => {
    expect(trimSpace('\u0085')).toBe('');
    expect(trimSpace('﻿beach ')).toBe('﻿beach');
  });

  it('lower-cases with Unicode 17, as Go 1.27 does', () => {
    expect(parse('q\ua7ceq \u{16ea0}', nowIn('UTC'), none).text).toBe('q\ua7cfq \u{16ebb}');
  });
});

describe('a plan sent back, as the server reads it', () => {
  const LEO = '9a8b4f25-9127-5d5b-8aed-f41c179232d0', JUNE = 'eee6e1f2-c3fa-5f2e-a345-99ea6626bb97';
  const prev = (p: Record<string, unknown>) => askRequest({ q: '', prev: { intent: 'list', ...p } });

  it('refuses what huma refuses', () => {
    for (const bad of [{ type: 'gif' }, { type: '' }, { people: [{ id: 'leo', name: 'Leo' }] }, { model: 5 }, { favorite: 'yes' }, { foo: 1 },
      { character: { name: 'Nova' } }, { people: [{ id: LEO }] }, { people: [null] }, { intent: 'all' }]) expect(prev(bad)).toBeNull();
    expect(askRequest({ q: 'x'.repeat(301) })).toBeNull();
    expect(askRequest({ q: '😀'.repeat(300) })).not.toBeNull();
    expect(askRequest({ q: 'x', exclude: Array(21).fill('type') })).toBeNull();
    expect(askRequest({ q: null })).toBeNull();
    expect(askRequest(null)).toBeNull();
  });

  it('takes what huma takes, as encoding/json decodes it', () => {
    expect(askRequest({ q: 'x', prev: null, exclude: null })).toEqual({ q: 'x', prev: null, exclude: [] });
    expect(prev({ type: null, people: null })?.prev).toMatchObject({ type: '', people: [] });
    const ids = [LEO.toUpperCase(), `{${LEO}}`, `urn:UUID:${LEO}`, LEO.replaceAll('-', ''), `x${LEO}y`];
    expect(prev({ people: ids.map((id) => ({ id, name: 'Leo' })) })?.prev?.people.map((r) => r.id)).toEqual(ids.map(() => LEO));
    // Any case, the last spelling wins, and lists are filled in place.
    expect(askRequest({ Q: 'a', PREV: { INTENT: 'count', ſource: 'grok' } })).toMatchObject({ q: 'a', prev: { intent: 'count', source: 'grok' } });
    const r = askRequest({ q: 'x', prev: { intent: 'list', people: [{ id: LEO, name: 'A' }, { id: JUNE, name: 'B' }] }, Prev: { People: [{ id: JUNE }] } });
    expect(r?.prev?.people).toEqual([{ id: JUNE, name: 'A' }]);
  });

  it('names a source it does not know as Go does', () => {
    const p = prev({ source: 'constructor' })!.prev!;
    expect(chips(p)).toEqual([{ key: 'source', kind: 'Made in', label: '' }]);
    expect(describePlan(p)).toBe('items from ');
    expect(planJSON(p)).toEqual({ intent: 'list', source: 'constructor' });
  });

  it('capitalizes the first byte, as titleCase does', () => {
    expect(chips(prev({ label: 'élan' })!.prev!)[0].label).toBe('\ufffd\ufffdlan');
    expect(chips(prev({ label: 'receipt' })!.prev!)[0].label).toBe('Receipts');
  });
});

describe('text search, as Postgres does it', () => {
  it('stems English as Snowball does', () => {
    expect(['houses', 'painting', 'crashing', 'glowing', 'skies', 'added', '1970s'].map(stemEnglish)).toEqual(['hous', 'paint', 'crash', 'glow', 'sky', 'add', '1970s']);
  });

  it('matches prompts in English', () => {
    expect(textSearch('english', 'Pastel houses climbing a Lisbon hill', 'house')).toBe(true);
    expect(textSearch('english', 'Hand-painted azulejo tiles, watercolor painting', 'paint')).toBe(true);
    expect(textSearch('english', 'Hand-painted azulejo tiles', '"hand painted"')).toBe(true);
    expect(textSearch('english', 'Hand-painted azulejo tiles', 'hand-painted')).toBe(true);
    expect(textSearch('english', 'fox in the snow', 'fox -snow')).toBe(false);
    expect(textSearch('english', 'a fox at dusk', 'galaxy or fox')).toBe(true);
    expect(textSearch('english', 'retro poster, 1970s style', '1970')).toBe(false);
    expect(textSearch('english', 'the house of cards', '"house cards"')).toBe(false);
    expect(textSearch('english', 'the house of cards', '"house of cards"')).toBe(true);
    expect(textSearch('english', 'the house of cards', 'the')).toBe(false);
  });

  it('matches the text in a picture word for word', () => {
    expect(textSearch('simple', 'Honeycrisp 10 lb $24.00 Apple cider', '24')).toBe(false);
    expect(textSearch('simple', 'Honeycrisp 10 lb $24.00 Apple cider', '24.00')).toBe(true);
    expect(textSearch('simple', 'GORGE VIEW ORCHARD Hood River, OR', 'or')).toBe(true);
    expect(textSearch('simple', 'Apple cider 1/2 gal $9.00', '"1/2 gal"')).toBe(true);
    expect(textSearch('simple', 'Apple cider 1/2 gal $9.00', '2')).toBe(false);
  });
});
