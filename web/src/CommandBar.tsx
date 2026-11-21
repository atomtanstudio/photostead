import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import type { MarkChange, MarkFilter, Marks } from './api';
import { Icon } from './icons';
import { MarkControls, Stars, Swatches, filterActive } from './marks';
import { ZOOMS } from './timeline/Timeline';
import { pickFiles } from './Uploads';

/**
 * A small panel above a button in the bar, closed by Escape or a click elsewhere. It's drawn on the
 * page itself (a portal), since the bars clip and transform what's inside them.
 */
function Pop({ open, onClose, label, anchor, children }: { open: boolean; onClose: () => void; label: string; anchor: RefObject<HTMLElement | null>; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState<{ left: number; bottom: number } | null>(null);
  useLayoutEffect(() => {
    if (!open || !anchor.current) return;
    const r = anchor.current.getBoundingClientRect();
    const half = (ref.current?.offsetWidth ?? 0) / 2;
    setAt({ left: Math.min(Math.max(r.left + r.width / 2, half + 16), window.innerWidth - half - 16), bottom: window.innerHeight - r.top + 14 });
  }, [open, anchor]);
  useEffect(() => {
    if (!open) return;
    const down = (e: MouseEvent) => {
      const t = e.target as Node;
      if (!ref.current?.contains(t) && !anchor.current?.contains(t)) onClose();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('mousedown', down);
    window.addEventListener('keydown', key, true);
    return () => {
      document.removeEventListener('mousedown', down);
      window.removeEventListener('keydown', key, true);
    };
  }, [open, onClose, anchor]);
  if (!open) return null;
  return createPortal(
    <div className="pop" ref={ref} role="dialog" aria-label={label} style={at ? { left: at.left, bottom: at.bottom } : { visibility: 'hidden' }}>
      {children}
    </div>,
    document.body,
  );
}

const FLAGS: { value: MarkFilter['flag']; label: string }[] = [
  { value: undefined, label: 'All' },
  { value: 'pick', label: 'Picks' },
  { value: 'unrejected', label: 'Hide rejects' },
  { value: 'reject', label: 'Rejects' },
];

/** Narrow the timeline by stars, flag and color label. */
function FilterMenu({ filter, onFilter, onSave }: { filter: MarkFilter; onFilter: (f: MarkFilter) => void; onSave?: () => void }) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const on = filterActive(filter);
  return (
    <div className="c-pop">
      <button ref={button} type="button" className={`c-filter${on ? ' on' : ''}`} aria-pressed={on} aria-expanded={open} onClick={() => setOpen((o) => !o)}
        aria-label="Filter by stars, flags and color labels" title="Filter by stars, flags and color labels">
        <Icon.filter />
      </button>
      <Pop open={open} onClose={() => setOpen(false)} label="Filter" anchor={button}>
        <h4>Stars, at least</h4>
        <Stars value={filter.rating ?? 0} onChange={(rating) => onFilter({ ...filter, rating: rating || undefined })} />
        <h4>Flags</h4>
        <div className="seg" role="group" aria-label="Flags">
          {FLAGS.map((f) => (
            <button key={f.label} type="button" aria-pressed={filter.flag === f.value} onClick={() => onFilter({ ...filter, flag: f.value })}>
              {f.label}
            </button>
          ))}
        </div>
        <h4>Color label</h4>
        <Swatches value={filter.label} onChange={(c) => onFilter({ ...filter, label: c === 'none' ? undefined : c })} />
        {on && (
          <div className="pop-row">
            {onSave && (
              <button type="button" className="pill" onClick={() => { setOpen(false); onSave(); }} title="An album that keeps showing whatever this filter picks">
                Save as smart album
              </button>
            )}
            <button type="button" className="pop-clear" onClick={() => onFilter({})}>
              Clear the filter
            </button>
          </div>
        )}
      </Pop>
    </div>
  );
}

type Props = {
  query: string;
  placeholder: string;
  ask: boolean;
  onAsk: () => void;
  zoom: number;
  onSearch: (q: string) => void;
  onZoom: (delta: number) => void;
  filter?: MarkFilter; // with ratings and flags on
  onFilter?: (f: MarkFilter) => void;
  onSaveSmart?: () => void; // keep the filter as a smart album
  uploadAlbum?: string; // uploads go into this album (the one open, when you can add to it)
};

/** The bottom bar: search or ask, thumbnail size, and what's coming. */
export function CommandBar({ query, placeholder, ask, onAsk, zoom, onSearch, onZoom, filter, onFilter, onSaveSmart, uploadAlbum }: Props) {
  const [text, setText] = useState(query);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => setText(query), [query]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = (e.target as HTMLElement).closest('input, textarea, select, dialog');
      if (((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') || (e.key === '/' && !typing)) {
        e.preventDefault();
        input.current?.focus();
        input.current?.select();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  return (
    <form
      className="cmd"
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        onSearch(text.trim());
      }}
    >
      <button type="button" className="c-ask" aria-pressed={ask} onClick={() => {
          onAsk();
          input.current?.focus();
        }}
        title={ask ? 'Asking questions. Click to search instead' : 'Ask a question about your library'}>
        <Icon.spark />
        <span>Ask</span>
      </button>
      <label className="c-q">
        <Icon.search />
        <span className="sr">Search</span>
        <input
          ref={input}
          type="search"
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.stopPropagation();
              setText('');
              onSearch('');
              input.current?.blur();
            }
          }}
          placeholder={placeholder}
          autoComplete="off"
          enterKeyHint={ask ? 'go' : 'search'}
        />
        <kbd>⌘K</kbd>
      </label>
      {filter && onFilter && <FilterMenu filter={filter} onFilter={onFilter} onSave={onSaveSmart} />}
      <div className="c-zoom" role="group" aria-label="Thumbnail size">
        <button type="button" onClick={() => onZoom(-1)} disabled={zoom === 0} aria-label="Smaller thumbnails (−)">
          <Icon.minus />
        </button>
        <button type="button" onClick={() => onZoom(1)} disabled={zoom === ZOOMS.length - 1} aria-label="Larger thumbnails (+)">
          <Icon.plus />
        </button>
      </div>
      <button type="button" className="c-ico" onClick={(e) => pickFiles(e.altKey, uploadAlbum)} data-testid="upload-bar"
        title="Upload photos and videos (Option-click for a folder), or drop them anywhere on the page" aria-label="Upload">
        <Icon.upload />
      </button>
    </form>
  );
}

type SelectionProps = {
  count: number;
  busy: boolean;
  onClear: () => void;
  onDownload: () => void;
  extra?: { label: string; title: string; onClick: () => void }; // an action for the collection on screen
  favorite: boolean; // every selected item is already a favorite
  onFavorite: () => void;
  onAlbum: () => void;
  onTrash: () => void;
  trash?: { onRestore: () => void; onDelete: () => void }; // in the trash, these replace the rest
  marks?: Marks; // with ratings and flags on: what the selection has in common
  onMark?: (ch: MarkChange) => void;
  onEdit?: () => void; // keywords, description, dates
  onStack?: () => void; // stack them by hand
  onVault?: () => void; // move them into the vault
  vault?: { onOut: () => void; onDelete: () => void }; // in the vault: back out, or delete there
  share?: { shared: boolean; onClick: () => void }; // into the Household, or (all shared already) out of it
};

/** Stars, flags and labels for every selected item. */
function MarkMenu({ marks, onMark, busy }: { marks: Marks; onMark: (ch: MarkChange) => void; busy: boolean }) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  return (
    <div className="c-pop">
      <button ref={button} type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} disabled={busy}
        title="Stars (0–5), flags (P, X, U) and color labels (6–9)">
        <Icon.star />
        <span>Mark</span>
      </button>
      <Pop open={open} onClose={() => setOpen(false)} label="Mark" anchor={button}>
        <MarkControls marks={marks} onChange={onMark} />
      </Pop>
    </div>
  );
}

/** Replaces the command bar while items are selected. */
export function SelectionBar({ count, busy, onClear, onDownload, extra, favorite, onFavorite, onAlbum, onTrash, trash, marks, onMark, onEdit, onStack, onVault, vault, share }: SelectionProps) {
  return (
    <div className="selbar" role="toolbar" aria-label="Selection">
      <button type="button" onClick={onClear} aria-label="Clear the selection (Esc)">
        <Icon.x />
      </button>
      <span className="sb-n">{count.toLocaleString()} selected</span>
      <span className="sb-sep" aria-hidden="true" />
      {vault ? (
        <>
          <button type="button" onClick={vault.onOut} disabled={busy} title="Back to the timeline, where everything shows">
            <Icon.unlock />
            <span>Move out</span>
          </button>
          <button type="button" className="sb-del" onClick={vault.onDelete} disabled={busy} title="Delete (Delete): they wait in the vault’s Deleted for 30 days">
            <Icon.trash />
            <span>Delete</span>
          </button>
        </>
      ) : trash ? (
        <>
          <button type="button" onClick={trash.onRestore} disabled={busy} title="Put them back where they were">
            <Icon.restore />
            <span>Restore</span>
          </button>
          <button type="button" className="sb-del" onClick={trash.onDelete} disabled={busy} title="Delete for good: this can’t be undone">
            <Icon.trash />
            <span>Delete forever</span>
          </button>
        </>
      ) : (
        <>
          <button type="button" onClick={onDownload} disabled={busy || count > 5000} title={count > 5000 ? 'Up to 5,000 at a time' : 'Download the originals as a zip'}>
            <Icon.download />
            <span>{busy ? 'Preparing…' : 'Download'}</span>
          </button>
          {extra && (
            <button type="button" onClick={extra.onClick} title={extra.title} disabled={busy}>
              <Icon.x />
              <span>{extra.label}</span>
            </button>
          )}
          <button type="button" onClick={onFavorite} disabled={busy} aria-pressed={favorite} title={favorite ? 'Take them out of Favorites (F)' : 'Add them to Favorites (F)'}>
            <Icon.heart />
            <span>{favorite ? 'Unfavorite' : 'Favorite'}</span>
          </button>
          {marks && onMark && <MarkMenu marks={marks} onMark={onMark} busy={busy} />}
          {onEdit && (
            <button type="button" onClick={onEdit} disabled={busy} title="Keywords, description and dates">
              <Icon.pencil />
              <span>Edit</span>
            </button>
          )}
          {onStack && count > 1 && (
            <button type="button" onClick={onStack} disabled={busy} title="Show them as one item, the earliest on top">
              <Icon.layers />
              <span>Stack</span>
            </button>
          )}
          {share && (
            <button type="button" onClick={share.onClick} disabled={busy}
              title={share.shared ? 'Take them out of the Household (they stay in their owner’s library)' : 'Share them with everyone in the Household'}>
              <Icon.household />
              <span>{share.shared ? 'Unshare' : 'Share'}</span>
            </button>
          )}
          <button type="button" onClick={onAlbum} disabled={busy} title="Add them to an album">
            <Icon.album />
            <span>Album</span>
          </button>
          {onVault && (
            <button type="button" onClick={onVault} disabled={busy} title="Move them to the vault: kept out of everything else, behind a PIN">
              <Icon.lock />
              <span>Vault</span>
            </button>
          )}
          <button type="button" className="sb-del" onClick={onTrash} disabled={busy} title="Move them to the trash (Delete). Items there are deleted for good after 30 days">
            <Icon.trash />
            <span>Delete</span>
          </button>
        </>
      )}
    </div>
  );
}
