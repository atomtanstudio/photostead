import { useEffect, useState } from 'react';
import { api, thumbURL, type Album } from './api';
import { Icon } from './icons';
import { formatSpan } from './format';
import { placeholder } from './thumbhash';

export const plural = (n: number, one: string, many: string) => `${n.toLocaleString()} ${n === 1 ? one : many}`;

// Initials for a cover with no photo to show yet.
export const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]!.toUpperCase())
    .join('');

export function Cover({ id, hash, round, name }: { id?: string; hash?: string; round?: boolean; name: string }) {
  const ph = id ? placeholder(id, hash) : undefined;
  return (
    <span className={`cover${round ? ' round' : ''}`} style={{ backgroundImage: ph ? `url(${ph})` : undefined }}>
      {id ? (
        <img src={thumbURL(id)} alt="" loading="lazy" decoding="async" onLoad={(e) => e.currentTarget.classList.add('ok')} />
      ) : (
        <span className="initials" aria-hidden="true">
          {initials(name)}
        </span>
      )}
    </span>
  );
}

/** One album as a card: cover, title, how many items (and albums inside it) and when. */
export function AlbumCard({ a, onOpen }: { a: Album; onOpen: (id: string) => void }) {
  return (
    <button type="button" className="card" onClick={() => onOpen(a.id)}>
      <Cover id={a.cover} hash={a.coverThumbhash} name={a.title} />
      <b>
        {a.kind === 'smart' && <Icon.filter />}
        {a.title}
      </b>
      <span>
        {[a.children ? plural(a.children, 'album', 'albums') : '', plural(a.count, 'item', 'items')].filter(Boolean).join(' · ')}
        {a.firstDate ? ` · ${formatSpan(a.firstDate, a.lastDate)}` : ''}
      </span>
      {a.owner ? <span className="card-shared"><Icon.household /> Shared by {a.owner.name}</span>
        : a.members ? <span className="card-shared"><Icon.household /> Shared with {plural(a.members, 'person', 'people')}</span> : null}
    </button>
  );
}

/** The albums at the top, as cards; albums inside albums show on their album's page. */
export function AlbumsPage({ onOpen, onImport, onFail, onNew, onNewSmart }: {
  onOpen: (id: string) => void;
  onImport: () => void;
  onFail: (e: unknown) => void;
  onNew: () => void;
  onNewSmart: () => void;
}) {
  const [albums, setAlbums] = useState<Album[] | null>(null);
  useEffect(() => {
    api.albums().then((r) => setAlbums(r.albums), onFail);
  }, [onFail]);
  return (
    <div className="scroller page">
      <header className="results-head">
        <div>
          <p className="kicker">Collections</p>
          <h1>Albums</h1>
          <p className="muted">{albums ? plural(albums.length, 'album', 'albums') : ' '}</p>
        </div>
        <div className="h-actions">
          <button type="button" className="pill" onClick={onNew}>
            <Icon.plus />
            New album
          </button>
          <button type="button" className="pill" onClick={onNewSmart} title="An album whose items are whatever its rules pick, kept up to date">
            <Icon.filter />
            New smart album
          </button>
        </div>
      </header>
      {albums?.length === 0 && (
        <div className="empty page-empty">
          <h1>No albums yet</h1>
          <p>Make one here, or bring your albums over from another app.</p>
          <button type="button" className="pill" onClick={onImport}>
            Import…
          </button>
        </div>
      )}
      <div className="cards">
        {albums?.map((a) => <AlbumCard key={a.id} a={a} onOpen={onOpen} />)}
      </div>
    </div>
  );
}
