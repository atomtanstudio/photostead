// Backups of the database, for the server's admin: the last copy and how it went, the copies
// kept, when the next one runs, and backing up now. The photos aren't in it (they're in their
// folders); what is, is everything Photostead knows about them that's nowhere else.

import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type Backups } from './api';

const size = (n: number) => (n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : `${Math.max(1, Math.round(n / (1 << 20)))} MB`);

function when(iso: string) {
  const d = new Date(iso);
  const time = d.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  const days = Math.round((new Date().setHours(0, 0, 0, 0) - new Date(d).setHours(0, 0, 0, 0)) / 86400000);
  if (days === 0) return `today at ${time}`;
  if (days === 1) return `yesterday at ${time}`;
  if (days === -1) return `tomorrow at ${time}`;
  return `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} at ${time}`;
}

export function BackupsDialog({ onClose }: { onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [b, setB] = useState<Backups | null>(null);
  const [error, setError] = useState('');
  const load = () => api.backups().then(setB, (e) => setError(e instanceof ApiError ? e.message : 'Couldn’t read the backups.'));
  useEffect(() => {
    ref.current?.showModal();
    void load();
  }, []);
  useEffect(() => { // while one runs, see it finish
    if (!b?.running) return;
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
  }, [b?.running]);
  const last = b?.status.last;
  return (
    <dialog ref={ref} className="zone-dialog sec-dialog" onClose={onClose} aria-labelledby="bk-title">
      <h2 id="bk-title">Backups</h2>
      <p className="muted">
        Every night at 3:30, Photostead copies its database: your albums, people’s names, sharing and links, descriptions and settings. Your photos and
        videos aren’t in it; they stay in their folders, and thumbnails can be made again.
      </p>
      {b && !b.on && <p className="sec-note muted">Backups are off on this server (PHOTOSTEAD_BACKUP_DIR=off).</p>}
      {b?.on && (
        <>
          {b.problem && <p className="error" role="alert">{b.problem}</p>}
          <section>
            <ul className="sec-list">
              <li>
                <span>
                  <b>{last ? `Last copy ${when(last.at)}` : 'No copy yet'}</b>
                  <small>
                    {last ? `${size(last.bytes)}${b.status.seconds ? ` · took ${Math.max(1, Math.round(b.status.seconds))} seconds` : ''} · ` : ''}
                    next {when(b.nextAt)}
                  </small>
                </span>
                <button type="button" className="pill" disabled={b.running} onClick={() => api.backUpNow().then(setB, (e) => setError(e instanceof ApiError ? e.message : 'That didn’t work.'))}>
                  {b.running ? (b.problem ? 'Trying again…' : 'Backing up…') : 'Back up now'}
                </button>
              </li>
            </ul>
          </section>
          <section>
            <h3>Copies kept</h3>
            <p className="muted">The newest {b.keep}, in <code>{b.dir}</code>{b.dir.startsWith('/photos-array') ? ' (on Unraid, the photostead share’s backups folder, on the array)' : ''}.</p>
            {b.copies.length > 0 && (
              <ul className="sec-list bk-copies">
                {b.copies.map((c) => (
                  <li key={c.name}><span><b>{when(c.at)}</b></span><small>{size(c.bytes)}</small></li>
                ))}
              </ul>
            )}
          </section>
          {b.files && <FilesCheck b={b} onChange={setB} onError={setError} />}
          <p className="muted small">
            A copy is read back in full before it counts. Beside each copy is a list of every file with its hash (<code>….files.tsv.gz</code>), to check
            restored photos against. To prove one restores, run <code>photostead backup check</code> on the server; the steps to bring one back
            are in the docs (<code>docs/backups.md</code>).
          </p>
        </>
      )}
      {error && <p className="error" role="alert">{error}</p>}
      <div className="actions">
        <span className="grow" />
        <button type="button" onClick={() => ref.current?.close()}>Done</button>
      </div>
    </dialog>
  );
}

/** The integrity check: files read again each night and compared with their hashes. */
function FilesCheck({ b, onChange, onError }: { b: Backups; onChange: (b: Backups) => void; onError: (s: string) => void }) {
  const f = b.files;
  const pct = f.files ? Math.round((f.checked / f.files) * 100) : 0;
  return (
    <section data-testid="files-check">
      <h3>Checking files</h3>
      <p className="muted">
        Every night at 4:15, for up to an hour, Photostead reads files again and checks each still matches what it was when it came in: a disk going bad shows
        here before it’s too late. A whole library takes a week or so.
      </p>
      {f.damaged.length > 0 && (
        <div className="error" role="alert">
          <b>{f.damaged.length === 1 ? 'A file no longer matches what it was.' : `${f.damaged.length} files no longer match what they were.`}</b> Bring them back
          from another copy (a backup of the share, the camera, the cloud):
          <ul className="bk-damaged">
            {f.damaged.map((d) => <li key={d.library + d.path}><code>{d.library}/{d.path}</code></li>)}
          </ul>
        </div>
      )}
      <ul className="sec-list">
        <li>
          <span>
            <b>{f.files ? `${f.checked.toLocaleString()} of ${f.files.toLocaleString()} checked in the last 30 days (${pct}%)` : 'Nothing to check yet'}</b>
            <small>
              {f.last.at ? `Last ${when(f.last.at)}: ${(f.last.checked ?? 0).toLocaleString()} checked, ${size(f.last.bytes ?? 0)} read` : 'Not run yet'}
              {f.last.changed ? ` · ${f.last.changed.toLocaleString()} changed since they were read (read again by the rescan)` : ''} · next {when(f.nextAt)}
            </small>
          </span>
          <button type="button" className="pill" disabled={f.running} onClick={() => api.checkFilesNow().then(onChange, (e) => onError(e instanceof ApiError ? e.message : 'That didn’t work.'))}>
            {f.running ? 'Checking…' : 'Check now'}
          </button>
        </li>
      </ul>
    </section>
  );
}
