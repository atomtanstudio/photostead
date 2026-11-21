import { useEffect, useState } from 'react';
import { api, type Folder, type Library } from './api';
import { plural } from './Collections';
import { Icon } from './icons';

/** The libraries, to browse by the folders on disk (read-only: nothing here changes them). */
export function FoldersPage({ onOpen, onFail }: { onOpen: (library: string) => void; onFail: (e: unknown) => void }) {
  const [libs, setLibs] = useState<Library[] | null>(null);
  useEffect(() => {
    api.libraries().then((r) => setLibs(r.libraries), onFail);
  }, [onFail]);
  return (
    <div className="scroller page">
      <header className="results-head">
        <div>
          <p className="kicker">Browse</p>
          <h1>Folders</h1>
          <p className="muted">The folders on disk as they are. Browsing them changes nothing.</p>
        </div>
      </header>
      <FolderList
        folders={(libs ?? []).map((l) => ({ name: l.name, path: l.id, items: l.items, note: l.kind === 'managed' ? 'Photostead’s share' : 'read-only' }))}
        onOpen={onOpen}
      />
    </div>
  );
}

/** Folders as rows: name, what's under it. */
export function FolderList({ folders, onOpen }: { folders: (Folder & { note?: string })[]; onOpen: (path: string) => void }) {
  if (!folders.length) return null;
  return (
    <ul className="fd-list">
      {folders.map((f) => (
        <li key={f.path}>
          <button type="button" onClick={() => onOpen(f.path)}>
            <Icon.folder />
            <span>{f.name}</span>
            <em>{[f.note, plural(f.items, 'item', 'items')].filter(Boolean).join(' · ')}</em>
          </button>
        </li>
      ))}
    </ul>
  );
}
