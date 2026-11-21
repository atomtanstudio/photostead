// Timeline layout: justified rows grouped by day, months stacked, and estimates for months that
// haven't loaded yet so the scrollbar spans the whole library from the start.

import type { TimelineAsset } from '../api';

export const GAP = 2;
export const MONTH_HEAD = 104; // month title block (includes the space above it)
export const DAY_HEAD = 36;
export const DAY_GAP = 22;

export type Cell = { asset: TimelineAsset; x: number; w: number };
export type Row = { y: number; h: number; cells: Cell[] };
export type Day = { key: string; y: number; count: number; rows: Row[] };
export type MonthLayout = { height: number; days: Day[] };

/** Aspect ratio used for layout; unknown sizes are treated as 3:2. */
export function ratio(a: TimelineAsset): number {
  if (!a.width || !a.height) return 1.5;
  return Math.min(3.2, Math.max(0.42, a.width / a.height));
}

/**
 * Fills rows left to right, keeping each item's shape. A row closes as soon as it is no taller
 * than `target`; the last row stays at `target` height rather than being stretched.
 */
export function justify(items: TimelineAsset[], width: number, target: number, gap = GAP): Row[] {
  const rows: Row[] = [];
  let row: TimelineAsset[] = [];
  let sum = 0;
  let y = 0;
  const place = (members: TimelineAsset[], h: number, fill: boolean) => {
    const widths = members.map((a) => ratio(a) * h);
    if (fill) {
      const used = widths.reduce((s, w) => s + w, 0) + gap * (members.length - 1);
      widths[widths.length - 1] += width - used; // absorb rounding so the row ends flush
    }
    let x = 0;
    const cells = members.map((asset, i) => {
      const cell = { asset, x, w: widths[i] };
      x += widths[i] + gap;
      return cell;
    });
    rows.push({ y, h, cells });
    y += h + gap;
  };
  for (const a of items) {
    row.push(a);
    sum += ratio(a);
    const h = (width - gap * (row.length - 1)) / sum;
    if (h <= target) {
      place(row, h, true);
      row = [];
      sum = 0;
    }
  }
  if (row.length) place(row, Math.min(target, (width - gap * (row.length - 1)) / sum), false);
  return rows;
}

/**
 * Lays out one month: its days in order (assets arrive newest first), each a block of justified
 * rows. A flat group (search results, best match first) is one block with no day headers.
 */
export function layoutMonth(assets: TimelineAsset[], width: number, target: number, flat = false): MonthLayout {
  if (flat) {
    const rows = justify(assets, width, target);
    const body = rows.length ? rows[rows.length - 1].y + rows[rows.length - 1].h : 0;
    return { height: MONTH_HEAD + body + DAY_GAP, days: [{ key: '', y: MONTH_HEAD, count: assets.length, rows: rows.map((r) => ({ ...r, y: r.y + MONTH_HEAD })) }] };
  }
  const days: Day[] = [];
  let y = MONTH_HEAD;
  let i = 0;
  while (i < assets.length) {
    const key = assets[i].localDate;
    let j = i;
    while (j < assets.length && assets[j].localDate === key) j++;
    const rows = justify(assets.slice(i, j), width, target);
    const top = y;
    const body = rows.length ? rows[rows.length - 1].y + rows[rows.length - 1].h : 0;
    days.push({ key, y: top, count: j - i, rows: rows.map((r) => ({ ...r, y: r.y + top + DAY_HEAD })) });
    y = top + DAY_HEAD + body + DAY_GAP;
    i = j;
  }
  return { height: y, days };
}

/** A month's height before its items have loaded: about 1.3 items per row-height square, plus day headers. */
export function estimateMonth(count: number, width: number, target: number): number {
  const perRow = Math.max(1, width / (target * 1.3));
  const rows = Math.ceil(count / perRow);
  const days = Math.min(count, Math.max(1, Math.round(count / 12)), 31);
  return MONTH_HEAD + rows * (target + GAP) + days * (DAY_HEAD + DAY_GAP);
}
