// The timeline's data: months with counts, filled in as they scroll into view. Kept outside
// React so the viewer and selection can walk across months without re-rendering the grid.
// The same model also holds a fixed list (search results), grouped into months up front.

import { api, type Bucket, type RecentAsset, type Scope, type TimelineAsset } from '../api';

export type MonthState = {
  month: string; // YYYY-MM
  count: number;
  assets?: TimelineAsset[];
  loading?: Promise<void>;
  failed?: boolean;
  label?: string; // a group that isn't a month (ranked search results)
  flat?: boolean; // no day headers: the order is the point
};

export class TimelineModel {
  readonly months: MonthState[];
  readonly total: number;
  readonly scope: Scope;
  private listeners = new Set<() => void>();
  private index = new Map<string, number>(); // asset id -> month index

  constructor(buckets: Bucket[], total: number, scope: Scope = { type: '' }) {
    this.months = buckets.map((b) => ({ month: b.month, count: b.count }));
    this.total = total;
    this.scope = scope;
  }

  /** A model of a fixed list of items (newest first), already loaded. */
  static fromAssets(assets: TimelineAsset[]): TimelineModel {
    const byMonth = new Map<string, TimelineAsset[]>();
    for (const a of assets) {
      const m = a.localDate.slice(0, 7);
      byMonth.set(m, [...(byMonth.get(m) ?? []), a]);
    }
    const model = new TimelineModel([...byMonth].map(([month, list]) => ({ month, count: list.length })), assets.length);
    model.months.forEach((m, i) => {
      m.assets = byMonth.get(m.month)!;
      m.assets.forEach((a) => model.index.set(a.id, i));
    });
    return model;
  }

  /** A ranked list (best match first) as one flat group. */
  static ranked(assets: TimelineAsset[], label: string): TimelineModel {
    const model = new TimelineModel([{ month: 'ranked', count: assets.length }], assets.length);
    model.months[0].assets = assets;
    model.months[0].label = label;
    model.months[0].flat = true;
    assets.forEach((a) => model.index.set(a.id, 0));
    return model;
  }

  /**
   * Recently added (newest-arrived first) as groups by the day each arrived, here: "Today",
   * "Yesterday", the weekday this past week, then the date. Each group keeps the arrival order.
   */
  static added(assets: RecentAsset[], now = new Date()): TimelineModel {
    const groups: { key: string; label: string; list: RecentAsset[] }[] = [];
    for (const a of assets) {
      const day = new Date(a.addedAt);
      const key = `added-${day.getFullYear()}-${day.getMonth() + 1}-${day.getDate()}`;
      if (groups[groups.length - 1]?.key !== key) groups.push({ key, label: addedLabel(day, now), list: [] });
      groups[groups.length - 1].list.push(a);
    }
    const model = new TimelineModel(groups.map((g) => ({ month: g.key, count: g.list.length })), assets.length);
    model.months.forEach((m, i) => {
      m.assets = groups[i].list;
      m.label = groups[i].label;
      m.flat = true;
      m.assets.forEach((a) => model.index.set(a.id, i));
    });
    return model;
  }

  subscribe(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /** Loads one month's items (once; concurrent callers share the request). */
  load(i: number): Promise<void> {
    const m = this.months[i];
    if (!m || m.assets) return Promise.resolve();
    if (m.loading) return m.loading;
    m.failed = false;
    m.loading = api
      .month(m.month, this.scope)
      .then((res) => {
        m.assets = res.assets;
        res.assets.forEach((a) => this.index.set(a.id, i));
      })
      .catch(() => {
        m.failed = true;
      })
      .finally(() => {
        m.loading = undefined;
        this.listeners.forEach((fn) => fn());
      });
    return m.loading;
  }

  /**
   * Changes loaded items in place (marks set here) and redraws them. The objects themselves change:
   * month layouts hold on to them.
   */
  patch(ids: Iterable<string>, change: Partial<TimelineAsset>) {
    for (const id of ids) {
      const a = this.find(id);
      if (a) Object.assign(a, change);
    }
    this.listeners.forEach((fn) => fn());
  }

  find(id: string): TimelineAsset | undefined {
    const mi = this.index.get(id);
    return mi === undefined ? undefined : this.months[mi].assets?.find((a) => a.id === id);
  }

  /** Every loaded item, in timeline order. */
  loaded(): TimelineAsset[] {
    return this.months.flatMap((m) => m.assets ?? []);
  }

  /** The loaded items from a to b inclusive, in timeline order (for shift-click ranges). */
  range(a: string, b: string): string[] {
    const ids = this.loaded().map((x) => x.id);
    const i = ids.indexOf(a);
    const j = ids.indexOf(b);
    if (i < 0 || j < 0) return [b];
    return ids.slice(Math.min(i, j), Math.max(i, j) + 1);
  }

  /** The item before (-1) or after (+1) this one in timeline order, loading the next month if needed. */
  async neighbor(id: string, dir: 1 | -1): Promise<TimelineAsset | undefined> {
    const mi = this.index.get(id);
    if (mi === undefined) return undefined;
    const list = this.months[mi].assets!;
    const ai = list.findIndex((a) => a.id === id);
    if (list[ai + dir]) return list[ai + dir];
    for (let j = mi + dir; j >= 0 && j < this.months.length; j += dir) {
      await this.load(j);
      const next = this.months[j].assets;
      if (next?.length) return dir === 1 ? next[0] : next[next.length - 1];
    }
    return undefined;
  }
}

/** What a day items arrived on is called in Recently added. */
export function addedLabel(day: Date, now = new Date()): string {
  const midnight = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((midnight(now) - midnight(day)) / 86400000);
  if (days <= 0) return 'Added today';
  if (days === 1) return 'Added yesterday';
  if (days < 7) return `Added ${day.toLocaleDateString(undefined, { weekday: 'long' })}`;
  const sameYear = day.getFullYear() === now.getFullYear();
  return `Added ${day.toLocaleDateString(undefined, { day: 'numeric', month: 'long', ...(sameYear ? {} : { year: 'numeric' }) })}`;
}
