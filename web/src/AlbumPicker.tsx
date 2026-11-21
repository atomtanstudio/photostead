import { useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, type Album } from './api';
import { pathOf } from './AlbumDialogs';

type Props = { ids: string[]; onClose: () => void; onDone: (album: Album) => void };

/** Puts the selected items in an album: one of yours, or a new one. */
export function AlbumPicker({ ids, onClose, onDone }: Props) {
  const count = ids.length;
  const dialog = useRef<HTMLDialogElement>(null);
  const [albums, setAlbums] = useState<Album[] | null>(null);
  const [q, setQ] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    dialog.current?.showModal();
    // Any album at any depth, but not a smart one: its items are its rules'.
    api.albums({ all: true }).then((r) => setAlbums(r.albums.filter((a) => a.kind !== 'smart' && a.role !== 'viewer').sort((x, y) => (y.lastDate ?? '').localeCompare(x.lastDate ?? ''))), () => setAlbums([]));
  }, []);
  const shown = useMemo(() => {
    const t = q.trim().toLowerCase();
    return (albums ?? []).filter((a) => !t || pathOf(a).toLowerCase().includes(t));
  }, [albums, q]);
  const exact = (albums ?? []).some((a) => a.title.toLowerCase() === q.trim().toLowerCase());
  const run = async (fn: () => Promise<Album>) => {
    setBusy(true);
    setError('');
    try {
      onDone(await fn());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t add them. Try again.');
      setBusy(false);
    }
  };
  const things = `${count.toLocaleString()} ${count === 1 ? 'item' : 'items'}`;
  return (
    <dialog ref={dialog} className="zone-dialog" onClose={onClose} aria-labelledby="ap-title">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const title = q.trim();
          if (title && !exact) void run(() => api.createAlbum(title, ids));
        }}
      >
        <h2 id="ap-title">Add {things} to an album</h2>
        <div className="ap-new">
          <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Find an album, or name a new one" maxLength={200} autoFocus aria-label="Album" />
          <button type="submit" disabled={busy || !q.trim() || exact}>
            New album
          </button>
        </div>
        {albums === null && <p className="muted small">Loading albums…</p>}
        {albums?.length === 0 && <p className="muted small">No albums yet. Name one above to make it.</p>}
        <ul className="ap-list">
          {shown.map((a) => (
            <li key={a.id}>
              <button type="button" disabled={busy} onClick={() => void run(() => api.addToAlbum(a.id, ids))}>
                <span>{pathOf(a)}</span>
                <em>{a.count.toLocaleString()}</em>
              </button>
            </li>
          ))}
        </ul>
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
        </div>
      </form>
    </dialog>
  );
}
