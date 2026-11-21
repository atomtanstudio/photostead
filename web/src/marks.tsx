// Stars, pick/reject flags and color labels: the photographer's tools, each person's own, shown
// only to those who turn them on (Appearance → Ratings & flags). Keys follow Lightroom: 0–5 stars,
// P pick, X reject, U unflag, 6–9 red, yellow, green, blue.

import type { Color, MarkChange, MarkFilter, Marks } from './api';
import { Icon } from './icons';

export const COLORS: { name: Color; title: string; key?: string }[] = [
  { name: 'red', title: 'Red', key: '6' },
  { name: 'yellow', title: 'Yellow', key: '7' },
  { name: 'green', title: 'Green', key: '8' },
  { name: 'blue', title: 'Blue', key: '9' },
  { name: 'purple', title: 'Purple' },
];

/** The change a key makes to an item with these marks, or null for a key that isn't a mark. */
export function markKey(key: string, current: Marks): MarkChange | null {
  if (key >= '0' && key <= '5') return { rating: Number(key) };
  const k = key.toLowerCase();
  if (k === 'p') return { flag: current.flag === 'pick' ? 'none' : 'pick' };
  if (k === 'x') return { flag: current.flag === 'reject' ? 'none' : 'reject' };
  if (k === 'u') return { flag: 'none' };
  const c = COLORS.find((x) => x.key === key);
  if (c) return { label: current.label === c.name ? 'none' : c.name };
  return null;
}

/** Marks after a change. */
export function applyMarks(m: Marks, ch: MarkChange): Marks {
  const out = { ...m };
  if (ch.rating !== undefined) out.rating = ch.rating || undefined;
  if (ch.flag !== undefined) out.flag = ch.flag === 'none' ? undefined : ch.flag;
  if (ch.label !== undefined) out.label = ch.label === 'none' ? undefined : ch.label;
  return out;
}

/** "4 stars", "Pick", "Red label"… for a toast or the viewer's flash. */
export function describeChange(ch: MarkChange): string {
  const parts: string[] = [];
  if (ch.rating !== undefined) parts.push(ch.rating ? `${'★'.repeat(ch.rating)}` : 'No stars');
  if (ch.flag !== undefined) parts.push(ch.flag === 'pick' ? 'Pick' : ch.flag === 'reject' ? 'Rejected' : 'Unflagged');
  if (ch.label !== undefined) parts.push(ch.label === 'none' ? 'No label' : `${COLORS.find((c) => c.name === ch.label)!.title} label`);
  return parts.join(' · ');
}

/** The change that puts back what an item had before. */
export function undoChange(before: Marks, ch: MarkChange): MarkChange {
  const back: MarkChange = {};
  if (ch.rating !== undefined) back.rating = before.rating ?? 0;
  if (ch.flag !== undefined) back.flag = before.flag ?? 'none';
  if (ch.label !== undefined) back.label = before.label ?? 'none';
  return back;
}

export function filterActive(f?: MarkFilter): boolean {
  return Boolean(f && (f.rating || f.flag || f.label));
}

export function describeFilter(f: MarkFilter): string {
  return [
    f.rating && `${'★'.repeat(f.rating)}${f.rating < 5 ? ' or more' : ''}`,
    f.flag === 'pick' ? 'Picks' : f.flag === 'reject' ? 'Rejected' : f.flag === 'unrejected' ? 'Rejects hidden' : '',
    f.label && `${COLORS.find((c) => c.name === f.label)!.title} label`,
  ].filter(Boolean).join(' · ');
}

/** Stars to click: the one already set clears it. */
export function Stars({ value, onChange, size }: { value: number; onChange: (n: number) => void; size?: 'small' }) {
  return (
    <span className={`mk-stars${size === 'small' ? ' small' : ''}`} role="group" aria-label="Stars">
      {[1, 2, 3, 4, 5].map((n) => (
        <button key={n} type="button" className={n <= value ? 'on' : undefined} aria-pressed={n <= value}
          aria-label={`${n} ${n === 1 ? 'star' : 'stars'} (${n})`} title={n === value ? 'Clear the stars (0)' : `${n} ${n === 1 ? 'star' : 'stars'} (${n})`}
          onClick={() => onChange(n === value ? 0 : n)}>
          <Icon.star />
        </button>
      ))}
    </span>
  );
}

/** Pick and reject: one at a time, clicking the one set clears it. */
export function Flags({ value, onChange }: { value?: Marks['flag']; onChange: (f: MarkChange['flag']) => void }) {
  return (
    <span className="mk-flags" role="group" aria-label="Flag">
      <button type="button" aria-pressed={value === 'pick'} className="pick" title={value === 'pick' ? 'Unflag (U)' : 'Pick (P)'}
        onClick={() => onChange(value === 'pick' ? 'none' : 'pick')}>
        <Icon.flag />
        Pick
      </button>
      <button type="button" aria-pressed={value === 'reject'} className="reject" title={value === 'reject' ? 'Unflag (U)' : 'Reject (X)'}
        onClick={() => onChange(value === 'reject' ? 'none' : 'reject')}>
        <Icon.x />
        Reject
      </button>
    </span>
  );
}

/** The five color labels; clicking the one set clears it. */
export function Swatches({ value, onChange }: { value?: Color; onChange: (c: Color | 'none') => void }) {
  return (
    <span className="mk-colors" role="group" aria-label="Color label">
      {COLORS.map((c) => (
        <button key={c.name} type="button" className={`sw ${c.name}`} aria-pressed={value === c.name}
          aria-label={`${c.title} label`} title={`${c.title}${c.key ? ` (${c.key})` : ''}${value === c.name ? ': click to clear' : ''}`}
          onClick={() => onChange(value === c.name ? 'none' : c.name)} />
      ))}
    </span>
  );
}

/** Stars, flags and labels together (the viewer's details, the selection's Mark menu). */
export function MarkControls({ marks, onChange }: { marks: Marks; onChange: (ch: MarkChange) => void }) {
  return (
    <div className="mk-controls">
      <Stars value={marks.rating ?? 0} onChange={(rating) => onChange({ rating })} />
      <Flags value={marks.flag} onChange={(flag) => onChange({ flag })} />
      <Swatches value={marks.label} onChange={(label) => onChange({ label })} />
    </div>
  );
}
