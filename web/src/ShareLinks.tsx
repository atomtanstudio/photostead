// An album's share links: make one (a password, a last day, downloads, uploads), copy it, change
// or delete it. A link works from the internet when Funnel is on (Devices & remote access).

import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type Album, type AlbumPerson, type LinkSettings, type PersonalLink, type ShareLink } from './api';
import { Copy } from './copy';
import { Icon } from './icons';

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
const LASTS: { label: string; days: number }[] = [
  { label: 'until I delete it', days: 0 },
  { label: 'for a day', days: 1 },
  { label: 'for a week', days: 7 },
  { label: 'for a month', days: 30 },
];
const until = (days: number) => (days ? new Date(Date.now() + days * 86400000).toISOString() : undefined);
const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });


/** Adding photos to an album shared with you as a contributor (they go into its owner's library). */
export function AlbumUpload({ albumId, onDone }: { albumId: string; onDone: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [note, setNote] = useState('');
  const send = async (files: FileList) => {
    let n = 0;
    for (const [k, f] of [...files].entries()) {
      setNote(`Sending ${k + 1} of ${files.length}…`);
      const form = new FormData();
      form.append('meta', new Blob([JSON.stringify({ fileName: f.name, takenAt: new Date(f.lastModified).toISOString() })], { type: 'application/json' }), 'meta.json');
      form.append('file', f, f.name);
      const r = await fetch(`/api/v1/albums/${albumId}/upload`, { method: 'POST', body: form, credentials: 'same-origin' });
      if (r.ok) n++;
    }
    setNote(n === files.length ? `Added ${n === 1 ? '1 item' : `${n} items`}; they appear once they’re processed.` : `${n} of ${files.length} were added.`);
    onDone();
  };
  return (
    <>
      <button type="button" className="pill" onClick={() => input.current?.click()}>
        <Icon.upload /> Add photos
      </button>
      <input ref={input} type="file" accept="image/*,video/*" multiple hidden onChange={(e) => e.target.files?.length && void send(e.target.files)} />
      {note && <small className="muted">{note}</small>}
    </>
  );
}

/** Who an album is shared with: the household, and guests (by personal link: no account to
 *  make), as viewers or contributors. */
function People({ album }: { album: Album }) {
  type Data = { members: AlbumPerson[]; candidates: AlbumPerson[]; invites: { id: string; name?: string; role: string; expiresAt: string }[] };
  const [data, setData] = useState<Data | null>(null);
  const [pick, setPick] = useState('');
  const [name, setName] = useState('');
  const [fresh, setFresh] = useState<AlbumPerson | null>(null); // just added: their link to send
  const [error, setError] = useState('');
  const reload = () => api.albumPeople(album.id).then(setData, (e) => setError(message(e, 'Couldn’t read who it’s shared with.')));
  useEffect(() => void reload(), []);
  const act = (f: () => Promise<unknown>) => {
    setError('');
    f().then(reload).catch((e) => setError(message(e, 'That didn’t work.')));
  };
  const add = () => {
    setError('');
    const before = new Set(data?.members.map((m) => m.id));
    api.shareWithSomeoneNew(album.id, name.trim()).then((r) => {
      setData(r);
      setFresh(r.members.find((m) => !before.has(m.id)) ?? null);
      setName('');
    }, (e) => setError(message(e, 'That didn’t work.')));
  };
  if (!data) return null;
  const devices = (l: PersonalLink) =>
    l.devices ? `open on ${l.devices} of ${l.maxDevices} devices · last used ${day(l.lastSeenAt)}` : 'not open on any device yet';
  return (
    <section>
      <h3>People</h3>
      <p className="muted">They see this album (not the rest of your library); contributors can add to it too.</p>
      <ul className="sec-list">
        {data.members.map((m) => (
          <li key={m.id}>
            <Icon.person />
            <span>
              <b>{m.name}{m.guest ? ' · guest' : ''}</b>
              <small>{m.link ? `Personal link · ${devices(m.link)}` : m.email}</small>
              {m.link && (
                <span className="sec-row sl-toggles">
                  <Copy text={m.link.url} label="Copy link" />
                  {m.link.devices > 0 && <button type="button" className="link" onClick={() => act(() => api.signOutLinkDevices(m.id))}>Sign out its devices</button>}
                  <button type="button" className="link" onClick={() => window.confirm(`Make ${m.name} a new link? The one you sent stops working.`) && act(() => api.replacePersonalLink(m.id))}>New link</button>
                </span>
              )}
            </span>
            <select value={m.role} onChange={(e) => act(() => api.shareAlbumWith(album.id, m.id, e.target.value as 'viewer' | 'contributor'))} aria-label={`What ${m.name} may do`}>
              <option value="viewer">Can see</option>
              <option value="contributor">Can add</option>
            </select>
            <button type="button" className="link" onClick={() => act(() => api.unshareAlbumWith(album.id, m.id))}>Remove</button>
          </li>
        ))}
        {data.invites.map((i) => (
          <li key={i.id}>
            <Icon.clock />
            <span><b>{i.name || 'Guest'} · invited</b><small>not opened yet · works until {day(i.expiresAt)}</small></span>
            <button type="button" className="link" onClick={() => act(() => api.cancelGuestInvite(album.id, i.id))}>Remove</button>
          </li>
        ))}
      </ul>
      {fresh?.link && (
        <div className="sec-confirm">
          <p><b>Send {fresh.name} this link.</b> It opens what you share with them on the first {fresh.link.maxDevices} devices they use. No account to make.</p>
          <div className="dev-addr"><span className="link-text">{fresh.link.url}</span><Copy text={fresh.link.url} /></div>
          {!fresh.link.public && (
            <small className="muted">It works only on the network you’re on now. To send it to someone away from home, turn on Funnel under Devices &amp; remote access; their link then works from anywhere.</small>
          )}
          <button type="button" className="link" onClick={() => setFresh(null)}>Done</button>
        </div>
      )}
      {data.candidates.length > 0 && (
        <div className="sec-row">
          <select value={pick} onChange={(e) => setPick(e.target.value)} aria-label="Someone to share with">
            <option value="">Share with…</option>
            {data.candidates.map((c) => <option key={c.id} value={c.id}>{c.name}{c.guest ? ' (guest)' : ''}</option>)}
          </select>
          <button type="button" className="pill" disabled={!pick} onClick={() => act(async () => { await api.shareAlbumWith(album.id, pick, 'viewer'); setPick(''); })}>Share</button>
        </div>
      )}
      <div className="sec-row">
        <input className="hh-input" value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && name.trim() && add()}
          placeholder="Someone new (their name)" aria-label="Their name" maxLength={100} />
        <button type="button" className="pill" disabled={!name.trim()} onClick={add}>Make their link</button>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
    </section>
  );
}

function NewLink({ onMake, busy }: { onMake: (s: LinkSettings) => void; busy: boolean }) {
  const [password, setPassword] = useState('');
  const [days, setDays] = useState(0);
  const [download, setDownload] = useState(false);
  const [upload, setUpload] = useState(false);
  return (
    <div className="hh-rule">
      <div className="sec-row">
        <span className="muted">Works</span>
        <select value={days} onChange={(e) => setDays(Number(e.target.value))} aria-label="How long it works">
          {LASTS.map((l) => <option key={l.days} value={l.days}>{l.label}</option>)}
        </select>
      </div>
      <input className="hh-input" type="text" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Password (optional)" aria-label="Password" autoComplete="off" />
      <label className="check"><input type="checkbox" checked={download} onChange={(e) => setDownload(e.target.checked)} /><span>Visitors can download the photos (where they were taken is left out)</span></label>
      <label className="check"><input type="checkbox" checked={upload} onChange={(e) => setUpload(e.target.checked)} /><span>Visitors can add their own photos to the album</span></label>
      <button type="button" className="pill primary" disabled={busy} onClick={() => onMake({ password: password || undefined, expiresAt: until(days), allowDownload: download, allowUpload: upload })}>
        <Icon.share /> Make the link
      </button>
    </div>
  );
}

/** A card to print for an event (a table, a wall): the album's name, the link's QR code, and what
 *  scanning it does. Opens the browser's print window. */
function printCard(title: string, l: ShareLink) {
  const w = window.open('', '_blank', 'width=520,height=720');
  if (!w) return;
  const esc = (s: string) => s.replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!);
  w.document.write(`<!doctype html><meta charset="utf-8"><title>${esc(title)}</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;margin:0;text-align:center;color:#111}
.card{padding:40px 48px;border:1px solid #ddd;border-radius:24px}h1{font-size:28px;margin:0 0 8px}p{margin:6px 0;color:#555}img{display:block;width:260px;height:260px;margin:18px auto;image-rendering:pixelated}
small{display:block;max-width:260px;margin:0 auto;font-size:12px;line-height:1.45;color:#888;overflow-wrap:anywhere}@media print{.card{border:0}}</style>
<div class="card"><h1>${esc(title)}</h1><p>Scan to see the photos${l.allowUpload ? ' and add yours' : ''}</p><img src="${esc(l.qr ?? "")}" alt="QR code">
${l.hasPassword ? '<p>Ask for the password</p>' : ''}<small>${esc(l.url)}</small></div>`);
  w.document.close();
  // Printed from here: the page's own script would be refused (the window shares this one's policy).
  const img = w.document.querySelector('img');
  const go = () => setTimeout(() => { w.focus(); w.print(); }, 150);
  if (img && !img.complete) img.addEventListener('load', go, { once: true });
  else go();
}

export function ShareLinksDialog({ album, onClose }: { album: Album; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [links, setLinks] = useState<ShareLink[] | null>(null);
  const [adding, setAdding] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const reload = () => api.albumLinks(album.id).then((r) => { setLinks(r.links); setAdding(r.links.length === 0); }, (e) => setError(message(e, 'Couldn’t read the links.')));
  useEffect(() => {
    ref.current?.showModal();
    void reload();
  }, []);
  const act = (f: () => Promise<unknown>) => {
    setBusy(true);
    setError('');
    f().then(reload).catch((e) => setError(message(e, 'That didn’t work. Try again.'))).finally(() => setBusy(false));
  };
  const local = links?.some((l) => !l.public);
  return (
    <dialog ref={ref} className="zone-dialog sec-dialog" onClose={onClose} aria-labelledby="sl-title">
      <h2 id="sl-title">Share “{album.title}”</h2>
      {album.kind === 'manual' && <People album={album} />}
      <h3 className="sl-links-head">Links</h3>
      <p className="muted">Anyone with a link sees this album, as it is now and as it changes, with no account. Nothing else of yours.</p>
      {local && (
        <p className="sec-note muted">These links work only where you are now. To send them to anyone, turn on Funnel under Devices & remote access.</p>
      )}
      {links && links.length > 0 && (
        <section>
          <ul className="sec-list">
            {links.map((l) => (
              <li key={l.id} className="sl-link">
                <Icon.share />
                <span>
                  <b className="sl-url">{l.url.replace(/^https?:\/\//, '')}</b>
                  <small>
                    {[l.hasPassword && 'password', l.expiresAt ? `until ${day(l.expiresAt)}` : '', l.allowDownload && 'downloads', l.allowUpload && 'uploads',
                      `${l.views.toLocaleString()} ${l.views === 1 ? 'view' : 'views'}`].filter(Boolean).join(' · ')}
                  </small>
                  <span className="sec-row sl-toggles">
                    <label className="check"><input type="checkbox" checked={l.allowDownload} disabled={busy}
                      onChange={(e) => act(() => api.editLink(l.id, { expiresAt: l.expiresAt, allowDownload: e.target.checked, allowUpload: l.allowUpload }))} /> Downloads</label>
                    <label className="check"><input type="checkbox" checked={l.allowUpload} disabled={busy}
                      onChange={(e) => act(() => api.editLink(l.id, { expiresAt: l.expiresAt, allowDownload: l.allowDownload, allowUpload: e.target.checked }))} /> Uploads</label>
                  </span>
                </span>
                <Copy text={l.url} />
                {l.qr && <button type="button" className="link" onClick={() => printCard(album.title, l)} data-testid="print-card">Print a card</button>}
                <button type="button" className="link" disabled={busy} onClick={() => window.confirm('Delete this link? It stops working at once.') && act(() => api.deleteLink(l.id))}>Delete</button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {adding ? (
        <section>
          <h3>{links?.length ? 'Another link' : 'A link'}</h3>
          <NewLink busy={busy} onMake={(s) => act(() => api.newLink(album.id, s))} />
        </section>
      ) : (
        <button type="button" className="pill" onClick={() => setAdding(true)}>Make another link</button>
      )}
      {error && <p className="error" role="alert">{error}</p>}
      <div className="actions">
        <span className="grow" />
        <button type="button" onClick={() => ref.current?.close()}>Done</button>
      </div>
    </dialog>
  );
}
