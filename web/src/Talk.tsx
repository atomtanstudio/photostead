import { useCallback, useEffect, useRef, useState } from 'react';
import { thumbURL } from './api';
import { Icon } from './icons';

/**
 * Talking about a shared album, and picking from it (proofing): the album's activity (who added
 * what, and comments) with a box to say something, everyone's picks for the owner (your own for
 * others), and, in the viewer, an item's comments with a Pick button. For the owner, the people
 * it's shared with, and guests through their personal links.
 */

type Comment = { id: string; assetId?: string; author: string; guest?: boolean; mine: boolean; body: string; createdAt: string };
type Activity = { kind: 'added' | 'comment'; id?: string; mine?: boolean; who: string; at: string; items?: number; assets?: string[]; body?: string; assetId?: string };
type Pick = { assetId: string; people: string[]; mine: boolean };

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method, credentials: 'same-origin',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) {
    let why = `Photostead said no (${r.status}).`;
    try {
      const p = (await r.json()) as { detail?: string };
      if (p.detail) why = p.detail;
    } catch {
      // keep the status
    }
    throw new Error(why);
  }
  return (r.status === 204 ? undefined : await r.json()) as T;
}

const ago = (at: string) => {
  const s = (Date.now() - Date.parse(at)) / 1000;
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return new Date(at).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: s > 300 * 86400 ? 'numeric' : undefined });
};

const thumb = (id: string) => thumbURL(id);

function Say({ onSay, placeholder }: { onSay: (text: string) => Promise<void>; placeholder: string }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!text.trim()) return;
    setBusy(true);
    setError('');
    try {
      await onSay(text.trim());
      setText('');
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form className="talk-say" onSubmit={send}>
      <input value={text} onChange={(e) => setText(e.target.value)} placeholder={placeholder} maxLength={2000} aria-label={placeholder} data-testid="talk-input" />
      <button type="submit" disabled={busy || !text.trim()}>Send</button>
      {error && <small className="r-warn">{error}</small>}
    </form>
  );
}

/** An album's activity and picks, in a panel beside the album. */
export function AlbumTalk({ albumId, owner, onOpen, onClose }: { albumId: string; owner: boolean; onOpen: (assetId: string) => void; onClose: () => void }) {
  const [tab, setTab] = useState<'activity' | 'picks'>('activity');
  const [activity, setActivity] = useState<Activity[] | null>(null);
  const [picks, setPicks] = useState<Pick[] | null>(null);
  const base = `/api/v1/albums/${albumId}`;
  const load = useCallback(async () => {
    const [a, p] = await Promise.all([
      call<{ activity: Activity[] }>('GET', `${base}/activity`),
      call<{ picks: Pick[] }>('GET', `${base}/picks`),
    ]);
    setActivity(a.activity);
    setPicks(p.picks);
  }, [base]);
  useEffect(() => {
    void load().catch(() => setActivity([]));
    const t = setInterval(() => void load().catch(() => {}), 20000);
    return () => clearInterval(t);
  }, [load]);
  return (
    <aside className="talk" aria-label="Activity" data-testid="album-talk">
      <header>
        <div className="talk-tabs" role="tablist">
          <button type="button" role="tab" aria-selected={tab === 'activity'} onClick={() => setTab('activity')}>Activity</button>
          <button type="button" role="tab" aria-selected={tab === 'picks'} onClick={() => setTab('picks')}>
            Picks{picks && picks.length > 0 ? ` · ${picks.length}` : ''}
          </button>
        </div>
        <button type="button" className="up-x" onClick={onClose} aria-label="Close">✕</button>
      </header>
      {tab === 'activity' ? (
        <>
          <Say placeholder="Say something about this album" onSay={async (body) => { await call('POST', `${base}/comments`, { body }); await load(); }} />
          {activity === null ? (
            <p className="muted">Loading…</p>
          ) : activity.length === 0 ? (
            <p className="muted">Nothing yet. Comments, and who adds what, show here.</p>
          ) : (
            <ul className="talk-list">
              {activity.map((a, i) => {
                return (
                  <li key={i}>
                    <p>
                      <b>{a.who || 'Someone'}</b>{' '}
                      {a.kind === 'added' ? `added ${a.items === 1 ? 'an item' : `${a.items} items`}` : <span className="talk-body">{a.body}</span>}
                      <small className="muted"> · {ago(a.at)}</small>
                      {a.id && a.mine && (
                        <button type="button" className="link" onClick={() => void call('DELETE', `${base}/comments/${a.id}`).then(load)}>Delete</button>
                      )}
                    </p>
                    {a.kind === 'added' && a.assets && a.assets.length > 0 && (
                      <div className="talk-thumbs">
                        {a.assets.map((id) => <button key={id} type="button" onClick={() => onOpen(id)}><img src={thumb(id)} alt="" loading="lazy" /></button>)}
                      </div>
                    )}
                    {a.kind === 'comment' && a.assetId && (
                      <div className="talk-thumbs"><button type="button" onClick={() => onOpen(a.assetId!)}><img src={thumb(a.assetId)} alt="" loading="lazy" /></button></div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </>
      ) : (
        <>
          <p className="muted small">{owner ? 'What the people you shared this with picked. Open an item and pick it there.' : 'Your picks, for whoever shared this. Open an item and pick it there.'}</p>
          {picks && picks.length > 0 ? (
            <ul className="talk-picks">
              {picks.map((p) => (
                <li key={p.assetId}>
                  <button type="button" onClick={() => onOpen(p.assetId)}><img src={thumb(p.assetId)} alt="" loading="lazy" /></button>
                  <small>{owner ? p.people.join(', ') : 'Picked'}</small>
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">No picks yet.</p>
          )}
        </>
      )}
    </aside>
  );
}

/** An item's comments in an album, and picking it, for the viewer. */
export function ItemTalk({ albumId, assetId, dark }: { albumId: string; assetId: string; dark?: boolean }) {
  const [comments, setComments] = useState<Comment[]>([]);
  const [picked, setPicked] = useState(false);
  const [open, setOpen] = useState(false);
  const base = `/api/v1/albums/${albumId}`;
  const seq = useRef(0);
  const load = useCallback(async () => {
    const n = ++seq.current;
    const [c, p] = await Promise.all([
      call<{ comments: Comment[] }>('GET', `${base}/comments?asset=${assetId}`),
      call<{ picks: Pick[] }>('GET', `${base}/picks`),
    ]);
    if (n !== seq.current) return; // moved on to another item meanwhile
    setComments(c.comments);
    setPicked(p.picks.some((x) => x.assetId === assetId && x.mine));
  }, [base, assetId]);
  useEffect(() => void load().catch(() => {}), [load]);
  const pick = async () => {
    setPicked(!picked);
    await call(picked ? 'DELETE' : 'PUT', `${base}/picks/${assetId}`).catch(() => setPicked(picked));
  };
  return (
    <div className={`item-talk${dark ? ' dark' : ''}`} data-testid="item-talk" onClick={(e) => e.stopPropagation()}>
      <div className="item-talk-bar">
        <button type="button" className={`pick${picked ? ' on' : ''}`} onClick={() => void pick()} aria-pressed={picked} data-testid="pick">
          <Icon.check /> {picked ? 'Picked' : 'Pick'}
        </button>
        <button type="button" className="link" onClick={() => setOpen(!open)} aria-expanded={open} data-testid="comments-toggle">
          {comments.length ? `${comments.length} ${comments.length === 1 ? 'comment' : 'comments'}` : 'Comment'}
        </button>
      </div>
      {open && (
        <div className="item-talk-body">
          <ul className="talk-list">
            {comments.map((c) => (
              <li key={c.id}>
                <p>
                  <b>{c.author}</b> <span className="talk-body">{c.body}</span> <small className="muted">· {ago(c.createdAt)}</small>
                  {c.mine && <button type="button" className="link" onClick={() => void call('DELETE', `${base}/comments/${c.id}`).then(load)}>Delete</button>}
                </p>
              </li>
            ))}
          </ul>
          <Say placeholder="Say something about this one" onSay={async (body) => { await call('POST', `${base}/comments`, { body, assetId }); await load(); }} />
        </div>
      )}
    </div>
  );
}
