import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError, type ImmichCheck, type ImmichImport, type ImportSummary } from './api';

const n = (x: number) => x.toLocaleString();
const plural = (x: number, one: string, many: string) => `${n(x)} ${x === 1 ? one : many}`;

type Props = { onClose: () => void; onImported: () => void };

/**
 * Import from Immich: connect a server with a key (checked before it's saved, and never shown
 * again), run the import, follow it, and read what it did.
 */
export function ImportDialog({ onClose, onImported }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [state, setState] = useState<ImmichImport | null>(null);
  const [editing, setEditing] = useState(false);
  const [url, setUrl] = useState('');
  const [key, setKey] = useState('');
  const [check, setCheck] = useState<ImmichCheck | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const wasRunning = useRef(false);
  const imported = useRef(onImported);
  imported.current = onImported;

  useEffect(() => {
    dialog.current?.showModal();
  }, []);

  const refresh = useCallback(async () => {
    const s = await api.immich();
    setState(s);
    const running = s.run?.state === 'running';
    if (wasRunning.current && !running) imported.current();
    wasRunning.current = running;
    return s;
  }, []);

  // Once, on opening: later refreshes must not reset what's being typed.
  useEffect(() => {
    refresh()
      .then((s) => {
        setUrl(s.url ?? `http://${window.location.hostname}:2283`);
        setEditing(!s.connected);
      })
      .catch(() => setError('Couldn’t load the import settings.'));
  }, [refresh]);

  // Follow a running import.
  const running = state?.run?.state === 'running';
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void refresh().catch(() => {}), 1500);
    return () => clearInterval(t);
  }, [running, refresh]);

  const connect = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError('');
    setCheck(null);
    try {
      const r = await api.connectImmich(url.trim(), key.trim());
      setCheck(r.check);
      if (r.saved) {
        setKey('');
        setEditing(false);
        await refresh();
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t check the key. Try again.');
    } finally {
      setBusy(false);
    }
  };

  const start = async () => {
    setError('');
    try {
      setState(await api.runImmich());
      wasRunning.current = true;
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Couldn’t start the import.');
    }
  };

  const forget = async () => {
    await api.forgetImmich();
    setCheck(null);
    setEditing(true);
    await refresh();
  };

  const run = state?.run;
  return (
    <dialog ref={dialog} className="zone-dialog import-dialog" onClose={onClose} aria-labelledby="import-title">
      <h2 id="import-title">Import from Immich</h2>
      <p className="lede">
        Brings your favorites, archived items, albums, people’s names, descriptions and tags over to the same photos here. Photostead only reads from Immich and never
        changes it. Import again any time to bring over what changed since.
      </p>

      {state && editing && (
        <form onSubmit={connect} className="im-form">
          <label>
            <span>Immich address</span>
            <input type="url" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://192.168.1.10:2283" required />
          </label>
          <label>
            <span>API key</span>
            <input type="password" value={key} onChange={(e) => setKey(e.target.value)} autoComplete="off" spellCheck={false} required />
          </label>
          <p className="muted small">
            In Immich, open Account Settings → API Keys → New API Key, and give it <code>asset.read</code>, <code>album.read</code>,{' '}
            <code>person.read</code> and <code>tag.read</code>. The key stays on your Photostead server and isn’t shown again.
          </p>
          {check && <CheckList check={check} />}
          <div className="actions">
            {state.connected && (
              <button type="button" className="secondary" onClick={() => setEditing(false)}>
                Keep the saved key
              </button>
            )}
            <button type="submit" disabled={busy}>
              {busy ? 'Checking…' : 'Check and save'}
            </button>
          </div>
        </form>
      )}

      {state && !editing && (
        <>
          <div className="im-conn">
            <span>
              Connected to <b>{state.url}</b>
            </span>
            <span className="im-links">
              <button type="button" className="link" onClick={() => setEditing(true)}>
                Change key
              </button>
              <button type="button" className="link" onClick={() => void forget()}>
                Forget
              </button>
            </span>
          </div>
          {check && <CheckList check={check} />}
          {run && (
            <div className="im-run" role="status">
              {run.state === 'running' && (
                <p className="im-step">
                  <i className="spin" aria-hidden="true" />
                  {run.step || 'Starting'}…
                </p>
              )}
              {run.state === 'failed' && <p className="error">The last import stopped: {run.error}</p>}
              {run.state === 'done' && run.summary && <Summary s={run.summary} finished={run.finishedAt} />}
            </div>
          )}
        </>
      )}

      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <div className="actions">
        <button type="button" className="secondary" onClick={onClose}>
          Close
        </button>
        {state?.connected && !editing && (
          <button type="button" onClick={() => void start()} disabled={running}>
            {running ? 'Importing…' : run ? 'Import again' : 'Import'}
          </button>
        )}
      </div>
    </dialog>
  );
}

function CheckList({ check }: { check: ImmichCheck }) {
  const row = (label: string, v: string) => (
    <li className={v === 'ok' ? 'ok' : 'no'}>
      <b>{label}</b> {v === 'ok' ? 'can be read' : v}
    </li>
  );
  return (
    <ul className="im-check">
      <li className="ok">
        <b>Immich {check.version}</b> answered
      </li>
      {row('Photos and videos', check.items)}
      {row('Albums', check.albums)}
      {row('People', check.people)}
      {check.tags !== undefined && row('Tags', check.tags)}
    </ul>
  );
}

function Summary({ s, finished }: { s: ImportSummary; finished?: string }) {
  const when = finished ? new Date(finished).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' }) : '';
  const changes = [
    s.favoritesSet && `${plural(s.favoritesSet, 'new favorite', 'new favorites')}`,
    s.favoritesOff && `${n(s.favoritesOff)} no longer favorites`,
    s.archivedSet && `${n(s.archivedSet)} archived`,
    s.archivedOff && `${n(s.archivedOff)} back in the timeline`,
    s.albumsNew && `${plural(s.albumsNew, 'new album', 'new albums')}`,
    s.peopleNew && `${plural(s.peopleNew, 'new person', 'new people')}`,
    s.descriptionsSet && `${plural(s.descriptionsSet, 'description', 'descriptions')} brought over`,
    s.ratingsSet && `${plural(s.ratingsSet, 'rating', 'ratings')} brought over`,
  ].filter(Boolean);
  return (
    <div className="im-summary">
      <p className="kicker">Last import{when ? ` · ${when}` : ''}</p>
      <p>
        Found <b>{n(s.matched)}</b> of {n(s.items)} Immich photos and videos here.
        {s.unmatched > 0 && ` ${n(s.unmatched)} aren’t in Photostead, usually because they’re in folders left out of it.`}
      </p>
      <dl>
        <dt>Favorites</dt>
        <dd>{n(s.favorites)}</dd>
        <dt>Archived</dt>
        <dd>{n(s.archived)}</dd>
        <dt>Albums</dt>
        <dd>
          {n(s.albums)}
          {s.albums > 0 && <span className="muted"> · {plural(s.albumItems, 'item', 'items')}{s.albumsMissing ? `, ${n(s.albumsMissing)} not here` : ''}</span>}
        </dd>
        <dt>People</dt>
        <dd>
          {n(s.people)} named{s.people > 0 && <span className="muted"> · {plural(s.peopleItems, 'suggestion', 'suggestions')}</span>}
        </dd>
        {s.descriptions !== undefined && (
          <>
            <dt>Descriptions</dt>
            <dd>{n(s.descriptions)}</dd>
            {s.ratings !== undefined && (
              <>
                <dt>Ratings</dt>
                <dd>{n(s.ratings)}</dd>
              </>
            )}
            <dt>Tags</dt>
            <dd>
              {n(s.tags ?? 0)}
              {(s.tags ?? 0) > 0 && <span className="muted"> · on {plural(s.tagItems ?? 0, 'item', 'items')}</span>}
            </dd>
          </>
        )}
      </dl>
      <p className="muted small">{changes.length ? `This time: ${changes.join(', ')}.` : 'Nothing had changed since the import before.'}</p>
      {s.skipped?.map((x) => (
        <p key={x} className="error small">
          {x}. Add it to the key in Immich, then use Change key here.
        </p>
      ))}
      {(s.unmatchedExamples?.length || s.folders?.length) && (
        <details>
          <summary>Details</summary>
          {s.folders && s.folders.length > 0 && (
            <>
              <p className="small">Immich folders found in Photostead (items matched):</p>
              <ul className="mono small">
                {s.folders.map((f) => (
                  <li key={f}>{f}</li>
                ))}
              </ul>
            </>
          )}
          {s.unmatchedExamples && s.unmatchedExamples.length > 0 && (
            <>
              <p className="small">Some that aren’t here:</p>
              <ul className="mono small break">
                {s.unmatchedExamples.map((f) => (
                  <li key={f}>{f}</li>
                ))}
              </ul>
            </>
          )}
        </details>
      )}
    </div>
  );
}

/**
 * Bringing photos in, by where they are now: Google Photos (a Takeout folder), Apple Photos (an
 * export with XMP, or the iPhone app), Immich (its own import), or any folder. Dropping a folder
 * onto the page does the rest (Uploads.tsx reads the sidecars).
 */
export function ImportGuide({ onClose, onImmich, onPick }: { onClose: () => void; onImmich: () => void; onPick: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog ref={dialog} className="zone-dialog import-dialog" onClose={onClose} aria-labelledby="guide-title" data-testid="import-guide">
      <h2 id="guide-title">Bring your photos in</h2>
      <p className="lede">Drop a folder anywhere on this page, or pick one. Photostead keeps one copy of each file, so bringing something in twice is harmless.</p>
      <section className="guide">
        <h3>From Google Photos</h3>
        <ol>
          <li>At <b>takeout.google.com</b>, choose only <b>Google Photos</b> and export (zip files, as large as offered).</li>
          <li>Download the zips and open them: they make a <b>Takeout</b> folder.</li>
          <li>Drop the <b>Takeout</b> folder here. Dates, descriptions, places, favorites, archived items and albums come with the photos; what was in Google’s trash stays behind.</li>
        </ol>
        <h3>From Apple Photos</h3>
        <ol>
          <li>On a Mac, choose the photos in Photos, then <b>File › Export › Export Unmodified Originals</b>, with <b>Export IPTC as XMP</b> ticked.</li>
          <li>Drop the folder here. Captions, keywords, places and dates come along.</li>
          <li>Or use the Photostead iPhone app: it backs up the phone’s library, iCloud originals included.</li>
        </ol>
        <h3>From Immich</h3>
        <p className="muted">Photostead can read Immich’s upload folder, and bring over its favorites, albums, people and tags.</p>
        <button type="button" className="secondary" onClick={onImmich}>Import from Immich…</button>
        <h3>Any other folder</h3>
        <p className="muted">Photos and videos, with XMP sidecars if there are any (Lightroom, darktable).</p>
        <h3>A folder already on the server</h3>
        <p className="muted">
          No need to copy it: give Photostead the folder, read-only, and it’s indexed where it is and watched for new files. On Unraid, open
          the <b>Docker</b> tab, click <b>Photostead › Edit</b>, choose <b>Add another Path</b>, and set <b>Container Path</b> to{' '}
          <code>/libraries/</code> and a name (<code>/libraries/pictures</code>, say), the folder as <b>Host Path</b>, and <b>Access Mode</b> to
          Read Only. It shows under Folders once Photostead restarts.
        </p>
      </section>
      <div className="actions">
        <button type="button" className="secondary" onClick={onClose}>Close</button>
        <button type="button" onClick={onPick}>Choose a folder…</button>
      </div>
    </dialog>
  );
}
