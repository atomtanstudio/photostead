// Health, for the server's admin: what needs a look on this server, in plain words, each with what
// to do about it (the photo share, disk space, memory, folders indexed in place, the AI work,
// backups and remote access). What needs a look comes first; the sidebar flags it.

import { useEffect, useRef, useState } from 'react';
import { ApiError, type HealthCheck } from './api';

const ORDER = { problem: 0, warn: 1, ok: 2 } as const;
const STATE_LABEL = { ok: 'Fine', warn: 'Worth a look', problem: 'Needs fixing' } as const;

/** How many checks need a look (a warning or a problem). */
export const needingLook = (checks: HealthCheck[] | null) => (checks ?? []).filter((c) => c.state !== 'ok').length;

export function HealthDialog({ checks, onCheck, onOpen, onClose }: {
  checks: HealthCheck[] | null;
  onCheck: () => Promise<void>; // look again
  onOpen: (id: 'backups' | 'remote') => void; // the dialog where a check is dealt with
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const again = () => {
    setBusy(true);
    setError('');
    onCheck().catch((e) => setError(e instanceof ApiError ? e.message : 'Couldn’t look again.')).finally(() => setBusy(false));
  };
  useEffect(() => {
    ref.current?.showModal();
    ref.current?.querySelector('h2')?.focus(); // not the first button, at the bottom: opened at the top
    again(); // fresh, not as it was when the sidebar last looked
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const n = needingLook(checks);
  const sorted = [...(checks ?? [])].sort((a, b) => ORDER[a.state] - ORDER[b.state]);
  return (
    <dialog ref={ref} className="zone-dialog sec-dialog health-dialog" onClose={onClose} aria-labelledby="health-title">
      <h2 id="health-title" tabIndex={-1}>Health</h2>
      <p className="lede" role="status">
        {!checks ? 'Looking…' : n === 0 ? 'Everything looks fine.' : n === 1 ? 'One thing needs a look.' : `${n} things need a look.`}
      </p>
      {error && <p className="error" role="alert">{error}</p>}
      <ul className="health-list">
        {sorted.map((c) => (
          <li key={c.id} className={`health-${c.state}`}>
            <i className="health-dot" aria-label={STATE_LABEL[c.state]} role="img" />
            <div>
              <b>{c.title}</b>
              <p>{c.detail}</p>
              {c.fix && (
                <p className="health-fix">
                  <span>What to do</span> {c.fix}
                </p>
              )}
              {c.state !== 'ok' && (c.id === 'backups' || c.id === 'remote') && (
                <button type="button" className="pill" onClick={() => onOpen(c.id as 'backups' | 'remote')}>
                  {c.id === 'backups' ? 'Open Backups' : 'Open Devices & remote access'}
                </button>
              )}
            </div>
          </li>
        ))}
      </ul>
      <div className="actions">
        <button type="button" className="secondary" disabled={busy} onClick={again}>{busy ? 'Looking…' : 'Look again'}</button>
        <button type="button" onClick={() => ref.current?.close()}>Done</button>
      </div>
    </dialog>
  );
}
