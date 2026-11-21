import { describe, expect, it } from 'vitest';
import type { RecentAsset } from '../api';
import { TimelineModel, addedLabel } from './model';

const at = (id: string, addedAt: Date, localDate = '2019-05-01'): RecentAsset => ({
  id, type: 'image', takenAt: `${localDate}T12:00:00Z`, localDate, width: 4, height: 3, render: 'done', addedAt: addedAt.toISOString(),
});

describe('Recently added', () => {
  const now = new Date(2026, 10, 6, 18, 0); // Friday 6 November 2026, 6 pm here

  it('names the day each arrived', () => {
    expect(addedLabel(new Date(2026, 10, 6, 0, 5), now)).toBe('Added today');
    expect(addedLabel(new Date(2026, 10, 5, 23, 59), now)).toBe('Added yesterday');
    expect(addedLabel(new Date(2026, 10, 2, 9), now)).toMatch(/^Added Monday$/);
    expect(addedLabel(new Date(2026, 9, 17, 9), now)).toMatch(/17/);
    expect(addedLabel(new Date(2026, 9, 17, 9), now)).not.toMatch(/2026/);
    expect(addedLabel(new Date(2025, 9, 17, 9), now)).toMatch(/2025/);
  });

  it('groups by the day they arrived, in arrival order, whatever the day they were taken', () => {
    const m = TimelineModel.added([
      at('a', new Date(2026, 10, 6, 17), '2019-01-01'),
      at('b', new Date(2026, 10, 6, 9), '2026-11-06'),
      at('c', new Date(2026, 10, 4, 12), '2001-07-04'),
    ], now);
    expect(m.total).toBe(3);
    expect(m.months.map((g) => [g.label, g.flat, g.assets?.map((x) => x.id)])).toEqual([
      ['Added today', true, ['a', 'b']],
      ['Added Wednesday', true, ['c']],
    ]);
    expect(m.find('c')?.localDate).toBe('2001-07-04');
    expect(m.range('a', 'c')).toEqual(['a', 'b', 'c']);
  });
});
