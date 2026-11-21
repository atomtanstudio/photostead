import { describe, expect, it } from 'vitest';
import type { TimelineAsset } from '../api';
import { DAY_HEAD, GAP, MONTH_HEAD, justify, layoutMonth } from './layout';

const asset = (id: string, w: number, h: number, day = '2026-10-16'): TimelineAsset => ({
  id, type: 'image', takenAt: `${day}T12:00:00Z`, localDate: day, width: w, height: h, render: 'done',
});

describe('justify', () => {
  it('fills full rows edge to edge and keeps shapes', () => {
    const items = Array.from({ length: 9 }, (_, i) => asset(String(i), 300, 200));
    const rows = justify(items, 1000, 230);
    const full = rows.slice(0, -1);
    expect(full.length).toBeGreaterThan(0);
    for (const r of full) {
      const last = r.cells[r.cells.length - 1];
      expect(last.x + last.w).toBeCloseTo(1000, 5);
      expect(r.h).toBeLessThanOrEqual(230);
      for (const c of r.cells.slice(0, -1)) expect(c.w / r.h).toBeCloseTo(1.5, 5);
    }
  });

  it('does not stretch a short last row', () => {
    const rows = justify([asset('a', 300, 200)], 1000, 230);
    expect(rows).toHaveLength(1);
    expect(rows[0].h).toBe(230);
    expect(rows[0].cells[0].w).toBeCloseTo(345, 5);
  });

  it('treats unknown sizes as 3:2', () => {
    const rows = justify([asset('a', 0, 0)], 1000, 200);
    expect(rows[0].cells[0].w).toBeCloseTo(300, 5);
  });
});

describe('layoutMonth', () => {
  it('groups by day in order with headers', () => {
    const items = [asset('a', 300, 200, '2026-10-16'), asset('b', 300, 200, '2026-10-16'), asset('c', 200, 300, '2026-10-02')];
    const m = layoutMonth(items, 1000, 230);
    expect(m.days.map((d) => [d.key, d.count])).toEqual([['2026-10-16', 2], ['2026-10-02', 1]]);
    expect(m.days[0].y).toBe(MONTH_HEAD);
    expect(m.days[0].rows[0].y).toBe(MONTH_HEAD + DAY_HEAD);
    expect(m.days[1].y).toBeGreaterThan(m.days[0].rows[0].y + m.days[0].rows[0].h + GAP);
    expect(m.height).toBeGreaterThan(m.days[1].rows[0].y + m.days[1].rows[0].h);
  });
});
