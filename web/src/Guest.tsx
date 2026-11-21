// A guest's Photostead: someone an album is shared with, who has no library of their own. Only
// the albums shared with them, each album's photos and videos, a simple viewer, and adding photos
// where they're a contributor.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { api, ApiError, type Album, type TimelineAsset, type User } from './api';
import { AlbumCard } from './Collections';
import { Icon } from './icons';
import { AlbumUpload } from './ShareLinks';
import { Viewer } from './SharedLink';
import { AlbumTalk } from './Talk';
import { Timeline } from './timeline/Timeline';
import { TimelineModel } from './timeline/model';

const noSelection = new Set<string>();

function AlbumView({ album, onBack }: { album: Album; onBack: () => void }) {
  const [model, setModel] = useState<TimelineModel | null>(null);
  const [open, setOpen] = useState<{ items: TimelineAsset[]; start: number } | null>(null);
  const [talk, setTalk] = useState(false);
  const scope = useMemo(() => ({ type: '' as const, collection: `album:${album.id}` }), [album.id]);
  const load = () => api.buckets(scope).then((b) => setModel(new TimelineModel(b.buckets, b.total, scope)), () => {});
  useEffect(() => void load(), [scope]);
  if (!model) return <div className="boot" aria-busy="true" />;
  return (
    <>
      <Timeline
        model={model}
        zoom={2}
        organize={false}
        selected={noSelection}
        onOpen={(a) => {
          const items = model.loaded();
          setOpen({ items, start: Math.max(0, items.findIndex((x) => x.id === a.id)) });
        }}
        onToggle={() => {}}
        onSelectDay={() => {}}
        onVisibleMonths={() => {}}
        header={
          <header className="results-head sh-head">
            <div>
              <p className="kicker crumbs"><span><button type="button" onClick={onBack}>Shared with you</button> ›</span></p>
              <h1>{album.title}</h1>
              <p className="muted">
                {model.total.toLocaleString()} {model.total === 1 ? 'item' : 'items'} · shared by {album.owner?.name}
                {album.role === 'contributor' ? ' · you can add to it' : ''}
              </p>
            </div>
            <div className="h-actions">
              <button type="button" className="pill" onClick={() => setTalk(!talk)} aria-pressed={talk} data-testid="activity"><Icon.household /> Activity</button>
              {album.role === 'contributor' && <AlbumUpload albumId={album.id} onDone={() => void load()} />}
            </div>
          </header>
        }
      />
      {talk && (
        <AlbumTalk albumId={album.id} owner={false} onClose={() => setTalk(false)} onOpen={(id) => {
          const items = model.loaded();
          const at = items.findIndex((x) => x.id === id);
          if (at >= 0) {
            setTalk(false);
            setOpen({ items, start: at });
          }
        }} />
      )}
      {open && <Viewer items={open.items} start={open.start} download={false} album={album.id} onClose={() => setOpen(null)} />}
    </>
  );
}

export function GuestApp({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  const [albums, setAlbums] = useState<Album[] | null>(null);
  const [open, setOpen] = useState<Album | null>(null);
  useEffect(() => {
    api.albums().then((r) => setAlbums(r.albums), () => setAlbums([]));
  }, []);
  return (
    <div className="shared-page guest">
      <div className="guest-bar">
        <span className="brand"><Icon.mark /> <span>Photostead</span></span>
        <span className="grow" />
        <span className="muted">{user.displayName || user.email}</span>
        <button type="button" className="link" onClick={() => api.logout().finally(onSignedOut)}>Sign out</button>
      </div>
      {open ? (
        <AlbumView key={open.id} album={open} onBack={() => setOpen(null)} />
      ) : (
        <div className="scroller page">
          <header className="results-head">
            <div>
              <p className="kicker">Photostead</p>
              <h1>Shared with you</h1>
              <p className="muted">{albums ? `${albums.length} ${albums.length === 1 ? 'album' : 'albums'}` : ' '}</p>
            </div>
          </header>
          {albums?.length === 0 && (
            <div className="empty page-empty"><h1>Nothing yet</h1><p>Albums shared with you show here.</p></div>
          )}
          <div className="cards">
            {albums?.map((a) => <AlbumCard key={a.id} a={a} onOpen={() => setOpen(a)} />)}
          </div>
        </div>
      )}
    </div>
  );
}

/** A personal link (/p/{token}): signs this device in as the guest it's for, then opens their
 *  albums. Signed in here as someone else, it says so rather than switching. */
export function PersonalLinkPage({ token }: { token: string }) {
  const [problem, setProblem] = useState<{ status: number; message: string } | null>(null);
  const open = useCallback(() => {
    setProblem(null);
    api.openPersonalLink(token).then(
      () => window.location.replace('/'),
      (e) => setProblem(e instanceof ApiError ? { status: e.status, message: e.message } : { status: 0, message: 'This link can’t be opened right now. Try again in a moment.' }),
    );
  }, [token]);
  useEffect(open, [open]);
  if (!problem) return <div className="boot" aria-busy="true" />;
  return (
    <main className="login">
      <form className="setup" onSubmit={(e) => e.preventDefault()}>
        <div className="brand"><Icon.mark /> <span>Photostead</span></div>
        <h1>{problem.status === 409 ? 'You’re signed in as someone else' : 'This link doesn’t open here'}</h1>
        <p className="lede">{problem.message}</p>
        {problem.status === 409 && <button type="button" onClick={() => void api.logout().finally(open)}>Sign out and open it</button>}
      </form>
    </main>
  );
}
