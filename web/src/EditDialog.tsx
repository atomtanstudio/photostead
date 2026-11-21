import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type Keyword } from './api';
import { Icon } from './icons';

type Props = {
  ids: string[];
  /** For one item: when it was taken, as its clock read (YYYY-MM-DDTHH:MM), to start the date from. */
  takenAt?: string;
  description?: string;
  onClose: () => void;
  /** Something changed: what, for the toast, an undo, and whether dates moved (the timeline reorders). */
  onChanged: (text: string, undo?: () => Promise<unknown>, dates?: boolean) => void;
};

const plural = (n: number, one: string, many: string) => `${n.toLocaleString()} ${n === 1 ? one : many}`;

/**
 * Keywords, a description and dates for one item or many at once: only your own items. Keywords
 * apply as soon as they're added or removed; the description and dates when saved.
 */
export function EditDialog({ ids, takenAt, description, onClose, onChanged }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const many = ids.length > 1;
  const things = plural(ids.length, 'item', 'items');
  const [on, setOn] = useState<Keyword[] | null>(null);
  const [all, setAll] = useState<Keyword[]>([]);
  const [word, setWord] = useState('');
  const [text, setText] = useState(description ?? '');
  const [mode, setMode] = useState<'shift' | 'set'>(many ? 'shift' : 'set');
  const [shift, setShift] = useState({ days: 0, hours: 0, minutes: 0 });
  const [when, setWhen] = useState(takenAt ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const refresh = () => api.selectionKeywords(ids).then((r) => setOn(r.keywords), () => setOn([]));
  useEffect(() => {
    dialog.current?.showModal();
    void refresh();
    api.keywords().then((r) => setAll(r.keywords), () => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try {
      await fn();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t save that. Try again.');
    } finally {
      setBusy(false);
    }
  };
  const addWords = () => {
    const names = word.split(',').map((w) => w.trim()).filter(Boolean);
    if (!names.length) return;
    void run(async () => {
      await api.editKeywords(ids, { add: names });
      setWord('');
      await refresh();
      onChanged(`Added ${names.map((n) => `“${n}”`).join(', ')} to ${things}`, () => api.editKeywords(ids, { remove: names }));
    });
  };
  const removeWord = (name: string) =>
    void run(async () => {
      const had = (await api.selectionKeywords(ids)).keywords.find((k) => k.name === name)?.items ?? 0;
      await api.editKeywords(ids, { remove: [name] });
      await refresh();
      // Undo puts it back only where it was, which only one item can say for sure.
      onChanged(`Took “${name}” off ${plural(had, 'item', 'items')}`, !many ? () => api.editKeywords(ids, { add: [name] }) : undefined);
    });
  const saveText = () =>
    void run(async () => {
      await api.setDescription(ids, text.trim());
      onChanged(text.trim() ? `Described ${things}` : `Cleared the description of ${things}`);
    });
  const seconds = ((shift.days * 24 + shift.hours) * 60 + shift.minutes) * 60;
  const moveDates = () =>
    void run(async () => {
      const r = await api.moveDates(ids, mode === 'shift' ? { shiftSeconds: seconds } : { setEarliest: when });
      if (!r.shiftSeconds) return;
      const back = -r.shiftSeconds;
      onChanged(`Moved ${plural(r.changed, 'item', 'items')} in time`, () => api.moveDates(ids, { shiftSeconds: back }), true);
      onClose();
    });
  const known = new Set((on ?? []).map((k) => k.name.toLowerCase()));
  return (
    <dialog ref={dialog} className="zone-dialog ed-dialog" onClose={onClose} aria-labelledby="ed-title">
      <form method="dialog" onSubmit={(e) => e.preventDefault()}>
        <h2 id="ed-title">Edit {things}</h2>

        <section className="ed-sec">
          <h3>Keywords</h3>
          {on === null ? (
            <p className="muted small">Loading…</p>
          ) : on.length === 0 ? (
            <p className="muted small">{many ? 'None of them has keywords yet.' : 'No keywords yet.'}</p>
          ) : (
            <div className="ed-chips">
              {on.map((k) => (
                <span key={k.name} className="ed-chip">
                  {k.name}
                  {many && <em>{k.items === ids.length ? 'all' : `${k.items} of ${ids.length}`}</em>}
                  <button type="button" onClick={() => removeWord(k.name)} disabled={busy} aria-label={`Remove ${k.name}`} title={`Take “${k.name}” off`}>
                    <Icon.x />
                  </button>
                </span>
              ))}
            </div>
          )}
          <div className="ap-new">
            <input value={word} onChange={(e) => setWord(e.target.value)} list="ed-words" placeholder="Add keywords, separated by commas"
              onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addWords(); } }} aria-label="Keyword" maxLength={200} autoFocus />
            <datalist id="ed-words">
              {all.filter((k) => !known.has(k.name.toLowerCase())).slice(0, 200).map((k) => <option key={k.name} value={k.name} />)}
            </datalist>
            <button type="button" onClick={addWords} disabled={busy || !word.trim()}>
              Add
            </button>
          </div>
        </section>

        <section className="ed-sec">
          <h3>Description</h3>
          <textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} maxLength={8000}
            placeholder={many ? 'One description for all of them' : 'What’s in it, where, who'} />
          <div className="ed-row">
            <span className="muted small">{many ? 'Replaces each item’s own description.' : 'Shown instead of the file’s own.'}</span>
            <button type="button" className="pill" onClick={saveText} disabled={busy || (!text.trim() && !description)}>
              {text.trim() ? 'Save description' : 'Clear description'}
            </button>
          </div>
        </section>

        <section className="ed-sec">
          <h3>Date and time</h3>
          <div className="seg" role="group" aria-label="How">
            <button type="button" aria-pressed={mode === 'set'} onClick={() => setMode('set')}>{many ? 'Set the earliest' : 'Set'}</button>
            <button type="button" aria-pressed={mode === 'shift'} onClick={() => setMode('shift')}>Shift by</button>
          </div>
          {mode === 'set' ? (
            <label className="ed-field">
              <span>{many ? 'The earliest was taken at — the rest keep their spacing' : 'Taken at'}</span>
              <input type="datetime-local" value={when} onChange={(e) => setWhen(e.target.value)} />
            </label>
          ) : (
            <div className="ed-shift">
              {(['days', 'hours', 'minutes'] as const).map((k) => (
                <label key={k}>
                  <input type="number" value={shift[k]} step={1} onChange={(e) => setShift((s) => ({ ...s, [k]: Math.trunc(Number(e.target.value) || 0) }))} />
                  <span>{k}</span>
                </label>
              ))}
            </div>
          )}
          <div className="ed-row">
            <span className="muted small">For a camera clock that was off. Files in the photostead share move to the new day’s folder.</span>
            <button type="button" className="pill" onClick={moveDates} disabled={busy || (mode === 'shift' ? seconds === 0 : !when)}>
              Move {things}
            </button>
          </div>
        </section>

        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <span className="grow" />
          <button type="button" onClick={onClose}>
            Done
          </button>
        </div>
      </form>
    </dialog>
  );
}
