// Memories: a strip of cards above the timeline. A day in each earlier year ("3 years ago") and a
// named person or pet over the years. Each card opens its best items in the viewer; "Not this one"
// keeps that day, or that person, out of memories.

import { useEffect, useRef, useState } from 'react';
import { api, ApiError, thumbURL, type HiddenDays, type Memory, type Person, type User } from './api';
import { Icon } from './icons';
import { placeholder } from './thumbhash';
import { personName } from './People';

export function MemoryStrip({ memories, onOpen, onHide }: { memories: Memory[]; onOpen: (m: Memory) => void; onHide: (m: Memory) => void }) {
  return (
    <section className="mem-strip" aria-label="Memories">
      {memories.map((m) => {
        const cover = m.assets[0];
        const ph = placeholder(cover.id, cover.thumbhash);
        return (
          <div key={m.key} className="mem-card" style={{ backgroundImage: ph ? `url(${ph})` : undefined }}>
            <button type="button" className="mem-open" onClick={() => onOpen(m)} aria-label={`${m.title}, ${m.subtitle}: ${m.count.toLocaleString()} ${m.count === 1 ? 'item' : 'items'}`}>
              <img src={thumbURL(cover.id)} alt="" loading="lazy" onLoad={(e) => e.currentTarget.classList.add('ok')} />
              <span className="mem-shade" />
              <span className="mem-text">
                <b>{m.title}</b>
                <small>{m.subtitle}</small>
              </span>
            </button>
            <button type="button" className="mem-hide" onClick={() => onHide(m)}
              title={m.person ? `Keep ${personName(m.person)} out of memories` : 'Not this one: keep this day out of memories'}
              aria-label="Not this one">
              <Icon.x />
            </button>
          </div>
        );
      })}
    </section>
  );
}

const fmt = (day: string) =>
  new Date(`${day}T12:00:00Z`).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
const span = (d: HiddenDays) => (d.from === d.to ? fmt(d.from) : `${fmt(d.from)} – ${fmt(d.to)}`);

/** Memories' settings: shown or not, AI-made images in or out, and the days and people kept out. */
export function MemoriesDialog({ user, onClose, onChanged }: { user: User; onClose: () => void; onChanged: (u?: User) => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [days, setDays] = useState<HiddenDays[] | null>(null);
  const [people, setPeople] = useState<Person[]>([]);
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [error, setError] = useState('');
  const shown = !user.settings?.hideMemories;
  const ai = Boolean(user.settings?.memoriesAi);
  const reload = () =>
    api.memorySettings().then((r) => {
      setDays(r.hiddenDays);
      setPeople(r.hiddenPeople);
    }, () => setError('Couldn’t read your memory settings.'));
  useEffect(() => {
    ref.current?.showModal();
    void reload();
  }, []);
  const fail = (e: unknown) => setError(e instanceof ApiError ? e.message : 'Couldn’t change that. Try again.');
  const set = (change: { hideMemories?: boolean; memoriesAi?: boolean }) => api.setSettings(change).then((u) => onChanged(u), fail);
  return (
    <dialog ref={ref} className="zone-dialog mem-dialog" onClose={onClose} aria-labelledby="mem-title">
      <form method="dialog">
        <h2 id="mem-title">Memories</h2>
        <label className="check">
          <input type="checkbox" checked={shown} onChange={(e) => void set({ hideMemories: !e.target.checked })} />
          <span>Show memories above the timeline</span>
        </label>
        <label className="check">
          <input type="checkbox" checked={ai} onChange={(e) => void set({ memoriesAi: e.target.checked })} />
          <span>Include AI-made images</span>
        </label>
        <p className="muted">Screenshots, documents, receipts, rejects and the vault are always left out.</p>

        <h3>Days kept out</h3>
        {days?.length === 0 && <p className="muted">None. “Not this one” on a memory keeps its day out.</p>}
        <ul className="mem-list">
          {days?.map((d) => (
            <li key={d.id}>
              <span>{span(d)}</span>
              <button type="button" className="link" onClick={() => api.unhideMemoryDays(d.id).then(() => { void reload(); onChanged(); }, fail)}>
                Let back in
              </button>
            </li>
          ))}
        </ul>
        <div className="mem-range">
          <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="From" />
          <span>to</span>
          <input type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} aria-label="To (optional)" />
          <button type="button" className="pill" disabled={!from}
            onClick={() => api.hideMemoryDays(from, to || undefined).then(() => { setFrom(''); setTo(''); void reload(); onChanged(); }, fail)}>
            Keep out
          </button>
        </div>

        <h3>People and pets kept out</h3>
        {people.length === 0 && <p className="muted">None. A person’s or pet’s page can keep them out, with every item they’re in.</p>}
        <ul className="mem-list">
          {people.map((p) => (
            <li key={p.id}>
              <span>{personName(p)}</span>
              <button type="button" className="link" onClick={() => api.editPerson(p.id, { inMemories: true }).then(() => { void reload(); onChanged(); }, fail)}>
                Let back in
              </button>
            </li>
          ))}
        </ul>
        {error && <p className="error" role="alert">{error}</p>}
        <div className="actions">
          <span className="grow" />
          <button type="submit">Done</button>
        </div>
      </form>
    </dialog>
  );
}
