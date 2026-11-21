import { useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, thumbURL, type AIModel, type Character, type Sample } from './api';
import { formatSpan } from './format';
import { Icon } from './icons';
import { placeholder } from './thumbhash';

const n = (x: number) => x.toLocaleString();

/** A big image and two small ones, stills first. */
function Mosaic({ samples }: { samples: Sample[] }) {
  return (
    <span className="ch-mosaic" aria-hidden="true">
      {[0, 1, 2].map((i) => {
        const s = samples[i];
        if (!s) return <i key={i} />;
        const ph = placeholder(s.id, s.thumbhash);
        return <img key={s.id} src={thumbURL(s.id)} alt="" loading="lazy" decoding="async" style={{ backgroundImage: ph ? `url(${ph})` : undefined }} />;
      })}
    </span>
  );
}

/** AI characters as cards: who they are, how many items show them, and their LoRAs. */
export function CharactersPage({ onOpen, onNew, onFail }: { onOpen: (id: string) => void; onNew: () => void; onFail: (e: unknown) => void }) {
  const [list, setList] = useState<Character[] | null>(null);
  useEffect(() => {
    api.characters().then((r) => setList(r.characters), onFail);
  }, [onFail]);
  const total = list?.reduce((x, c) => x + c.count, 0) ?? 0;
  return (
    <div className="scroller page">
      <header className="results-head cr-head">
        <div>
          <p className="kicker">Browse</p>
          <h1>Characters</h1>
          <p className="muted">
            {list ? `${n(list.length)} ${list.length === 1 ? 'character' : 'characters'} across ${n(total)} items, recognized from their LoRAs and faces` : ' '}
          </p>
        </div>
        <div className="cr-tools">
          <button type="button" className="pill" onClick={onNew}>
            <Icon.plus />
            New character
          </button>
        </div>
      </header>
      {list?.length === 0 && (
        <div className="empty page-empty">
          <h1>No characters yet</h1>
          <p>
            A character is someone who appears across your AI images and videos. Make one from their LoRAs, and everything made with those
            LoRAs shows up here, along with videos made from those images. They stay apart from People and private to you.
          </p>
          <button type="button" className="pill" onClick={onNew}>
            <Icon.plus />
            New character
          </button>
        </div>
      )}
      <div className="ch-grid">
        {list?.map((c) => (
          <button key={c.id} type="button" className="ch-card" onClick={() => onOpen(c.id)}>
            <Mosaic samples={c.samples} />
            <b>{c.name}</b>
            <span className="ch-meta">
              {n(c.count)} {c.count === 1 ? 'item' : 'items'}
              {c.lastDate ? ` · last ${formatSpan(c.lastDate, c.lastDate)}` : ''}
            </span>
            {c.loras.length > 0 && (
              <span className="ch-chips">
                {c.loras.slice(0, 2).map((l) => (
                  <em key={l}>{l}</em>
                ))}
              </span>
            )}
          </button>
        ))}
      </div>
    </div>
  );
}

const exactly = (name: string) => `^${name.toLowerCase().replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`;
const isExact = (p: string) => /^\^[^()[\]|*+?{}]*\$$/.test(p.replace(/\\./g, ''));

type DialogProps = { character?: Character; onClose: () => void; onSaved: (c: Character | null) => void };

/**
 * Make or change a character: a name, and the LoRAs that make them (picked from the catalog, or
 * as patterns for families of LoRAs with versions).
 */
export function CharacterDialog({ character, onClose, onSaved }: DialogProps) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [name, setName] = useState(character?.name ?? '');
  const [patterns, setPatterns] = useState<string[]>(character?.patterns ?? []);
  const [loras, setLoras] = useState<AIModel[] | null>(null);
  const [q, setQ] = useState('');
  const [advanced, setAdvanced] = useState(() => (character?.patterns ?? []).some((p) => !isExact(p)));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    dialog.current?.showModal();
    api.models().then((r) => setLoras(r.loras), () => setLoras([]));
  }, []);
  const matching = useMemo(() => {
    const res = patterns.flatMap((p) => {
      try {
        return [new RegExp(p, 'i')];
      } catch {
        return [];
      }
    });
    return (name: string) => res.some((re) => re.test(name.toLowerCase()));
  }, [patterns]);
  const shown = useMemo(() => {
    const t = q.trim().toLowerCase();
    return (loras ?? []).filter((l) => !t || l.name.toLowerCase().includes(t)).slice(0, 80);
  }, [loras, q]);
  const picked = (loras ?? []).filter((l) => matching(l.name));
  const toggle = (l: AIModel) => {
    const p = exactly(l.name);
    if (!patterns.includes(p) && matching(l.name)) return; // chosen by a pattern: change it under Patterns
    setPatterns((ps) => (ps.includes(p) ? ps.filter((x) => x !== p) : [...ps, p]));
  };

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const c = character ? await api.updateCharacter(character.id, name.trim(), patterns) : await api.createCharacter(name.trim(), patterns);
      onSaved(c);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t save. Try again.');
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!character || !window.confirm(`Delete ${character.name}? Their items stay in your library.`)) return;
    setBusy(true);
    try {
      await api.deleteCharacter(character.id);
      onSaved(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t delete. Try again.');
      setBusy(false);
    }
  };

  return (
    <dialog ref={dialog} className="zone-dialog ch-dialog" onClose={onClose} aria-labelledby="ch-title">
      <form onSubmit={save}>
        <h2 id="ch-title">{character ? `Edit ${character.name}` : 'New character'}</h2>
        <p className="lede">Everything made with these LoRAs shows this character, and so do videos made from those images.</p>
        <label className="ch-field">
          <span>Name</span>
          <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={100} autoFocus={!character} />
        </label>
        <div className="ch-field">
          <span>
            LoRAs <em>{picked.length ? `${picked.length} chosen · ${n(picked.reduce((x, l) => x + l.uses, 0))} uses` : 'none chosen'}</em>
          </span>
          {picked.length > 0 && (
            <div className="ch-picked">
              {picked.map((l) => (
                <button key={l.id} type="button" className="g-chip" onClick={() => toggle(l)} title={isExact(exactly(l.name)) && patterns.includes(exactly(l.name)) ? 'Remove' : 'Matched by a pattern'}>
                  {l.name}
                  {patterns.includes(exactly(l.name)) && <Icon.x />}
                </button>
              ))}
            </div>
          )}
          <label className="cr-filter ch-search">
            <Icon.search />
            <span className="sr">Find a LoRA</span>
            <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Find a LoRA" autoComplete="off" spellCheck={false} />
          </label>
          <div className="ch-list" role="listbox" aria-label="LoRAs" aria-multiselectable="true">
            {loras === null && <p className="muted small">Loading LoRAs…</p>}
            {loras?.length === 0 && <p className="muted small">No LoRAs in your library’s AI details yet.</p>}
            {shown.map((l) => {
              const on = matching(l.name);
              return (
                <button key={l.id} type="button" role="option" aria-selected={on} className={`ch-opt${on ? ' on' : ''}`} onClick={() => toggle(l)}
                  title={on && !patterns.includes(exactly(l.name)) ? 'Chosen by a pattern below' : undefined}>
                  <span className="ch-check">{on && <Icon.check />}</span>
                  <span className="ch-name">{l.name}</span>
                  <span className="ch-uses">{n(l.uses)}</span>
                </button>
              );
            })}
          </div>
        </div>
        <details className="ch-adv" open={advanced} onToggle={(e) => setAdvanced((e.target as HTMLDetailsElement).open)}>
          <summary>Patterns</summary>
          <p className="muted small">
            One per line, matched against LoRA names, ignoring case. <code>^ada_</code> catches every version of ada_…; picking a LoRA above adds{' '}
            <code>^its_name$</code>.
          </p>
          <textarea value={patterns.join('\n')} onChange={(e) => setPatterns(e.target.value.split('\n'))} rows={4} spellCheck={false} />
        </details>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          {character && (
            <button type="button" className="secondary danger" onClick={() => void remove()} disabled={busy}>
              Delete
            </button>
          )}
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={busy || !name.trim() || (!patterns.some((p) => p.trim()) && !character?.faceGroups)}>
            {busy ? 'Saving…' : 'Save'}
          </button>
        </div>
      </form>
    </dialog>
  );
}
