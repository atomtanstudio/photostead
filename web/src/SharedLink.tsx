// A share link's page (/s/{token}): the album, for anyone with the link. No account and none of
// the app: its title and whose it is, a password first if it has one, the photos and videos in a
// grid, a simple viewer, and (when the owner allows) downloads and adding your own.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, ApiError, originalURL, previewURL, type SharedAlbum, type TimelineAsset } from './api';
import { Icon } from './icons';
import { Timeline } from './timeline/Timeline';
import { TimelineModel } from './timeline/model';
import { ItemTalk } from './Talk';

const noSelection = new Set<string>();

export function Viewer({ items, start, download, onClose, album }: { items: TimelineAsset[]; start: number; download: boolean; onClose: () => void; album?: string }) {
  const [i, setI] = useState(start);
  const a = items[i];
  const go = useCallback((d: number) => setI((n) => Math.min(items.length - 1, Math.max(0, n + d))), [items.length]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
      else if (e.key === 'ArrowLeft') go(-1);
      else if (e.key === 'ArrowRight') go(1);
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, [go, onClose]);
  const href = originalURL(a.id).replace(/\/(video|original)$/, '/download');
  return (
    <div className="sh-viewer" role="dialog" aria-label="Photo">
      <div className="sh-stage" onClick={onClose}>
        {a.type === 'video' ? (
          <video key={a.id} src={originalURL(a.id)} poster={previewURL(a.id)} controls autoPlay playsInline onClick={(e) => e.stopPropagation()} />
        ) : (
          <img key={a.id} src={previewURL(a.id)} alt="" onClick={(e) => e.stopPropagation()} />
        )}
      </div>
      <div className="sh-bar">
        <span>{i + 1} of {items.length}</span>
        <span className="grow" />
        {download && (
          <a className="sh-btn" href={href} download title="Download (where it was taken is left out)">
            <Icon.download /> Download
          </a>
        )}
        <button type="button" className="sh-btn" onClick={onClose} aria-label="Close (Esc)"><Icon.x /></button>
      </div>
      {album && <ItemTalk albumId={album} assetId={a.id} dark />}
      {i > 0 && <button type="button" className="sh-nav prev" onClick={() => go(-1)} aria-label="Previous"><Icon.back /></button>}
      {i < items.length - 1 && <button type="button" className="sh-nav next" onClick={() => go(1)} aria-label="Next"><Icon.back /></button>}
    </div>
  );
}

/** Adding photos to a shared album: who they're from, and the files. */
function AddPhotos({ token, onDone }: { token: string; onDone: (n: number) => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [from, setFrom] = useState(() => {
    try {
      return localStorage.getItem('ps.from') ?? '';
    } catch {
      return '';
    }
  });
  const [progress, setProgress] = useState('');
  const send = async (files: FileList) => {
    try {
      localStorage.setItem('ps.from', from);
    } catch {
      /* storage unavailable */
    }
    let n = 0;
    for (const [k, f] of [...files].entries()) {
      setProgress(`Sending ${k + 1} of ${files.length}…`);
      const form = new FormData();
      form.append('meta', new Blob([JSON.stringify({ fileName: f.name, takenAt: new Date(f.lastModified).toISOString() })], { type: 'application/json' }), 'meta.json');
      form.append('file', f, f.name);
      const r = await fetch(`/api/v1/public/links/${token}/upload?from=${encodeURIComponent(from.trim())}`, { method: 'POST', body: form, credentials: 'same-origin' });
      if (r.ok) n++;
    }
    setProgress(n === files.length ? `Added ${n === 1 ? 'your photo' : `your ${n} photos and videos`}. Thank you!` : `${n} of ${files.length} were added.`);
    onDone(n);
  };
  return (
    <div className="sh-add">
      <input value={from} onChange={(e) => setFrom(e.target.value)} placeholder="Your name" aria-label="Your name" maxLength={80} />
      <button type="button" className="pill" onClick={() => input.current?.click()}>
        <Icon.upload /> Add photos
      </button>
      <input ref={input} type="file" accept="image/*,video/*" multiple hidden onChange={(e) => e.target.files?.length && void send(e.target.files)} />
      {progress && <small className="muted">{progress}</small>}
    </div>
  );
}

export function SharedLink({ token }: { token: string }) {
  const [album, setAlbum] = useState<SharedAlbum | null>(null);
  const [items, setItems] = useState<TimelineAsset[] | null>(null);
  const [gone, setGone] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [open, setOpen] = useState<number | null>(null);
  const load = useCallback(async () => {
    try {
      const a = await api.sharedAlbum(token);
      setAlbum(a);
      document.title = `${a.title} · Photostead`;
      if (!a.needsPassword) {
        const r = await api.sharedItems(token);
        setItems(r.items.map((x) => ({ ...x, render: 'done' as const })));
      }
    } catch (e) {
      setGone(e instanceof ApiError ? e.message : 'This link can’t be opened right now.');
    }
  }, [token]);
  useEffect(() => void load(), [load]);
  const model = useMemo(() => (items ? TimelineModel.fromAssets(items) : null), [items]);
  if (gone) {
    return (
      <main className="login"><form className="setup"><div className="brand"><Icon.mark /> <span>Photostead</span></div><h1>This link doesn’t work</h1><p className="lede">{gone}</p></form></main>
    );
  }
  if (album?.needsPassword) {
    return (
      <main className="login">
        <form onSubmit={(e) => {
          e.preventDefault();
          api.unlockShared(token, password).then(load, (err) => setError(err instanceof ApiError ? err.message : 'Try again.'));
        }}>
          <div className="brand"><Icon.mark /> <span>Photostead</span></div>
          <h1 className="login-step">{album.title}</h1>
          <p className="lede">Shared by {album.owner}. Type the password they gave you.</p>
          <label>Password<input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required autoFocus /></label>
          {error && <p className="error" role="alert">{error}</p>}
          <button type="submit">Open</button>
        </form>
      </main>
    );
  }
  if (!album || !model || !items) return <div className="boot" aria-busy="true" />;
  return (
    <div className="shared-page">
      <Timeline
        model={model}
        zoom={2}
        organize={false}
        selected={noSelection}
        onOpen={(a) => setOpen(items.findIndex((x) => x.id === a.id))}
        onToggle={() => {}}
        onSelectDay={() => {}}
        onVisibleMonths={() => {}}
        header={
          <header className="results-head sh-head">
            <div>
              <p className="kicker"><Icon.mark /> Shared by {album.owner}</p>
              <h1>{album.title}</h1>
              <p className="muted">
                {items.length.toLocaleString()} {items.length === 1 ? 'item' : 'items'}
                {album.expiresAt ? ` · until ${new Date(album.expiresAt).toLocaleDateString(undefined, { month: 'long', day: 'numeric' })}` : ''}
              </p>
              {album.description && <p className="h-body">{album.description}</p>}
            </div>
            {album.allowUpload && <div className="h-actions"><AddPhotos token={token} onDone={() => {}} /></div>}
          </header>
        }
      />
      {open !== null && open >= 0 && <Viewer items={items} start={open} download={album.allowDownload} onClose={() => setOpen(null)} />}
    </div>
  );
}
