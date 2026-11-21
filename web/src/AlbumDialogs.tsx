import { useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, type Album, type Keyword, type Person, type SmartRules } from './api';
import { Stars, Swatches } from './marks';

/** A dialog's common parts: open as a modal, a title, an error, and Cancel. */
function useDialog() {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
    ref.current?.querySelector('input')?.focus(); // showModal focuses the dialog itself
  }, []);
  return ref;
}

const FLAGS: { value: SmartRules['flag']; label: string }[] = [
  { value: undefined, label: 'Any' },
  { value: 'pick', label: 'Picks' },
  { value: 'unrejected', label: 'Not rejected' },
  { value: 'reject', label: 'Rejects' },
];
const SOURCES: { value: SmartRules['source']; label: string }[] = [
  { value: undefined, label: 'Anything' },
  { value: 'camera', label: 'Camera or phone' },
  { value: 'not_ai', label: 'Not AI' },
  { value: 'ai', label: 'Made with AI' },
];
const TYPES: { value: SmartRules['type']; label: string }[] = [
  { value: undefined, label: 'Both' },
  { value: 'image', label: 'Photos' },
  { value: 'video', label: 'Videos' },
];

function Seg<T>({ options, value, onChange, label }: { options: { value: T; label: string }[]; value: T; onChange: (v: T) => void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((o) => (
        <button key={o.label} type="button" aria-pressed={o.value === value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

/**
 * A smart album: a title and rules. Its items are whatever the rules pick, kept up to date. Opens
 * on an existing one (to change its rules), or new, with rules to start from (the filter in use).
 */
export function SmartDialog({ album, start, parent, onClose, onDone }: {
  album?: Album;
  start?: SmartRules;
  parent?: string;
  onClose: () => void;
  onDone: (a: Album) => void;
}) {
  const dialog = useDialog();
  const [title, setTitle] = useState(album?.title ?? '');
  const [rules, setRules] = useState<SmartRules>(album?.rules ?? start ?? {});
  const [people, setPeople] = useState<Person[]>([]);
  const [words, setWords] = useState<Keyword[]>([]);
  const [keywordText, setKeywordText] = useState((album?.rules?.keywords ?? []).join(', '));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    api.people().then((r) => setPeople(r.people.filter((p) => p.name)), () => {});
    api.keywords().then((r) => setWords(r.keywords), () => {});
  }, []);
  const set = (change: Partial<SmartRules>) => setRules((r) => ({ ...r, ...change }));
  const togglePerson = (id: string) =>
    set({ people: rules.people?.includes(id) ? rules.people.filter((p) => p !== id) : [...(rules.people ?? []), id] });
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const keywords = keywordText.split(',').map((k) => k.trim()).filter(Boolean);
    const clean: SmartRules = Object.fromEntries(
      Object.entries({ ...rules, keywords: keywords.length ? keywords : undefined, people: rules.people?.length ? rules.people : undefined })
        .filter(([, v]) => v !== undefined && v !== '' && v !== false && v !== 0),
    );
    setBusy(true);
    setError('');
    try {
      onDone(album ? await api.editAlbum(album.id, { title: title.trim(), rules: clean }) : await api.createAlbum(title.trim(), [], { parent, rules: clean }));
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t save. Try again.');
      setBusy(false);
    }
  };
  return (
    <dialog ref={dialog} className="zone-dialog ed-dialog" onClose={onClose} aria-labelledby="sm-title">
      <form onSubmit={save}>
        <h2 id="sm-title">{album ? `Rules for ${album.title}` : 'New smart album'}</h2>
        <p className="muted small">Its items are whatever these pick, kept up to date. Leave a rule alone to match everything.</p>
        <label className="ed-field">
          <span>Title</span>
          <input value={title} onChange={(e) => setTitle(e.target.value)} required maxLength={200} placeholder="Best of the year" />
        </label>
        <section className="ed-sec">
          <h3>What</h3>
          <Seg label="Photos or videos" options={TYPES} value={rules.type} onChange={(type) => set({ type })} />
          <Seg label="Where from" options={SOURCES} value={rules.source} onChange={(source) => set({ source })} />
          <label className="check">
            <input type="checkbox" checked={Boolean(rules.favorite)} onChange={(e) => set({ favorite: e.target.checked || undefined })} />
            Favorites only
          </label>
        </section>
        <section className="ed-sec">
          <h3>Stars, flags and labels</h3>
          <Stars value={rules.rating ?? 0} onChange={(rating) => set({ rating: rating || undefined })} />
          <Seg label="Flags" options={FLAGS} value={rules.flag} onChange={(flag) => set({ flag })} />
          <Swatches value={rules.label} onChange={(c) => set({ label: c === 'none' ? undefined : c })} />
        </section>
        {people.length > 0 && (
          <section className="ed-sec">
            <h3>With everyone picked</h3>
            <div className="ed-chips">
              {people.slice(0, 60).map((p) => (
                <button key={p.id} type="button" className="ed-pick" aria-pressed={Boolean(rules.people?.includes(p.id))} onClick={() => togglePerson(p.id)}>
                  {p.name}
                </button>
              ))}
            </div>
          </section>
        )}
        <section className="ed-sec">
          <h3>Keywords (any of them)</h3>
          <input value={keywordText} onChange={(e) => setKeywordText(e.target.value)} list="sm-words" placeholder="beach, sunset" maxLength={400} />
          <datalist id="sm-words">
            {words.slice(0, 200).map((k) => <option key={k.name} value={k.name} />)}
          </datalist>
        </section>
        <section className="ed-sec">
          <h3>Taken between</h3>
          <div className="ed-shift">
            <label>
              <input type="date" value={rules.after ?? ''} onChange={(e) => set({ after: e.target.value || undefined })} />
              <span>from</span>
            </label>
            <label>
              <input type="date" value={rules.before ?? ''} onChange={(e) => set({ before: e.target.value || undefined })} />
              <span>to</span>
            </label>
          </div>
        </section>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={busy || !title.trim()}>
            {album ? 'Save' : 'Make it'}
          </button>
        </div>
      </form>
    </dialog>
  );
}

/** Rename an album, or describe it. */
export function RenameDialog({ album, onClose, onDone }: { album: Album; onClose: () => void; onDone: (a: Album) => void }) {
  const dialog = useDialog();
  const [title, setTitle] = useState(album.title);
  const [description, setDescription] = useState(album.description ?? '');
  const [error, setError] = useState('');
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="rn-title">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          api.editAlbum(album.id, { title: title.trim(), description: description.trim() }).then(onDone, (err) =>
            setError(err instanceof ApiError ? err.message : 'Couldn’t save. Try again.'),
          );
        }}
      >
        <h2 id="rn-title">Rename</h2>
        <label className="ed-field">
          <span>Title</span>
          <input value={title} onChange={(e) => setTitle(e.target.value)} required maxLength={200} />
        </label>
        <label className="ed-field">
          <span>Description</span>
          <textarea className="ed-text" value={description} onChange={(e) => setDescription(e.target.value)} rows={3} maxLength={4000} />
        </label>
        {error && <p className="error" role="alert">{error}</p>}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={!title.trim()}>
            Save
          </button>
        </div>
      </form>
    </dialog>
  );
}

/** Put an album inside another (never inside itself or one of its own), or at the top. */
export function MoveDialog({ album, onClose, onDone }: { album: Album; onClose: () => void; onDone: (a: Album) => void }) {
  const dialog = useDialog();
  const [all, setAll] = useState<Album[] | null>(null);
  const [q, setQ] = useState('');
  const [error, setError] = useState('');
  useEffect(() => {
    api.albums({ all: true }).then((r) => setAll(r.albums), () => setAll([]));
  }, []);
  const t = q.trim().toLowerCase();
  const places = useMemo(
    () => (all ?? [])
      .filter((a) => a.id !== album.id && !a.path?.some((p) => p.id === album.id) && a.id !== album.parent)
      .filter((a) => !t || pathOf(a).toLowerCase().includes(t))
      .sort((x, y) => pathOf(x).localeCompare(pathOf(y))),
    [all, album, t],
  );
  const move = (parent?: string) =>
    api.moveAlbum(album.id, parent).then(onDone, (err) => setError(err instanceof ApiError ? err.message : 'Couldn’t move it.'));
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="mv-title">
      <form method="dialog" onSubmit={(e) => e.preventDefault()}>
        <h2 id="mv-title">Move {album.title}</h2>
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Find an album" aria-label="Album" />
        <ul className="ap-list">
          {album.parent && !t && (
            <li>
              <button type="button" onClick={() => void move()}>
                <span>To the top (Albums)</span>
              </button>
            </li>
          )}
          {places.map((a) => (
            <li key={a.id}>
              <button type="button" onClick={() => void move(a.id)}>
                <span>{pathOf(a)}</span>
                <em>{a.count.toLocaleString()}</em>
              </button>
            </li>
          ))}
        </ul>
        {error && <p className="error" role="alert">{error}</p>}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </dialog>
  );
}

/** "Trips › Japan › Kyoto". */
export const pathOf = (a: Album) => [...(a.path ?? []).map((p) => p.name), a.title].join(' › ');

/** A new album, empty, at the top or inside another. */
export function NewAlbumDialog({ parent, onClose, onDone }: { parent?: Album; onClose: () => void; onDone: (a: Album) => void }) {
  const dialog = useDialog();
  const [title, setTitle] = useState('');
  const [error, setError] = useState('');
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="na-title">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          api.createAlbum(title.trim(), [], { parent: parent?.id }).then(onDone, (err) =>
            setError(err instanceof ApiError ? err.message : 'Couldn’t make it. Try again.'),
          );
        }}
      >
        <h2 id="na-title">{parent ? `New album in ${parent.title}` : 'New album'}</h2>
        <input value={title} onChange={(e) => setTitle(e.target.value)} required maxLength={200} placeholder="Title" aria-label="Title" />
        {error && <p className="error" role="alert">{error}</p>}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={!title.trim()}>
            Make it
          </button>
        </div>
      </form>
    </dialog>
  );
}
