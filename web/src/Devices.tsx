// Devices & remote access: reaching Photostead from away (its own node on the owner's Tailscale
// network, over HTTPS), pairing a phone or the Capture extension with a code, and where you're
// signed in (with a way to sign any of them out).

import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type Addresses, type LastingCode, type Pairing, type Remote, type SignedIn, type User } from './api';
import { Copy } from './copy';
import { Icon } from './icons';

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

/** "Chrome on macOS", "Safari on iPhone"… from a user agent. */
function browserOf(ua = '') {
  const b = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : ua ? ua.split('/')[0] : 'A browser';
  const os = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'macOS' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  return os ? `${b} on ${os}` : b;
}

function ago(iso: string) {
  const min = Math.round((Date.now() - new Date(iso).getTime()) / 60000);
  if (min < 2) return 'active now';
  if (min < 60) return `${min} minutes ago`;
  if (min < 48 * 60) return `${Math.round(min / 60)} hours ago`;
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
const devices = (n: number) => (n === 1 ? '1 device' : `${n} devices`);
/** A number as typed, kept within 1 and most (usual when it's left empty or isn't one). */
const count = (v: string, most: number, usual: number) => {
  const n = Math.round(Number(v));
  return v.trim() === '' || !Number.isFinite(n) ? usual : Math.min(most, Math.max(1, n));
};

/**
 * Pairing codes that last, made by the admin for someone who can't come to the home network while a
 * 10-minute code is on screen: Apple's App Review, or family far away. The admin picks whose account
 * its devices pair into, for how many days and devices, with a short note to tell it apart; the code
 * shows once.
 */
function LastingCodes({ user, addresses }: { user: User; addresses?: Addresses }) {
  const [codes, setCodes] = useState<LastingCode[]>([]);
  const [members, setMembers] = useState<{ id: string; name: string }[]>([]);
  const [form, setForm] = useState<{ userId: string; days: string; devices: string; note: string } | null>(null); // as typed
  const [made, setMade] = useState<(Pairing & { lasting: LastingCode }) | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const load = () => api.lastingCodes().then((r) => setCodes(r.codes), () => {});
  useEffect(() => {
    void load();
  }, []);
  const open = () => {
    setMade(null);
    setError('');
    setForm({ userId: user.id, days: '14', devices: '5', note: '' });
    const me = [{ id: user.id, name: `${user.displayName} (you)` }];
    api.household().then((h) => setMembers(h.members.length ? h.members.map((x) => ({ id: x.id, name: x.me ? `${x.name} (you)` : x.name })) : me), () => setMembers(me));
  };
  const make = () => {
    if (!form) return;
    setBusy(true);
    setError('');
    api.makeLastingCode({ userId: form.userId, days: count(form.days, 90, 14), devices: count(form.devices, 50, 5), note: form.note.trim() || undefined }).then((r) => {
      setMade(r);
      setForm(null);
      void load();
    }, (e) => setError(message(e, 'Couldn’t make a code.'))).finally(() => setBusy(false));
  };
  const revoke = (c: LastingCode) => {
    const left = c.devices ? ` The ${devices(c.devices)} it paired stay${c.devices === 1 ? 's' : ''} signed in.` : '';
    if (!window.confirm(`Revoke the code ${c.note ? `“${c.note}” ` : ''}for ${c.name}? No more devices can pair with it.${left}`)) return;
    api.revokeLastingCode(c.id).then(load, (e) => setError(message(e, 'Couldn’t revoke it.')));
  };
  return (
    <div className="dev-lasting">
      {made ? (
        <div className="dev-pair">
          <img src={made.qr} alt="Pairing QR code" width={168} height={168} />
          <div>
            <b className="dev-code">{made.code}</b>
            <small className="muted">
              Pairs {devices(made.lasting.maxDevices)} into {made.lasting.name}’s account until {day(made.lasting.expiresAt)}. It’s shown this once: copy it now.
            </small>
            <span className="dev-link"><Copy text={made.code} label="Copy the code" /> · <Copy text={made.link} label="Copy the link" /></span>
            <button type="button" className="link dev-done" onClick={() => setMade(null)}>Done</button>
          </div>
        </div>
      ) : form ? (
        <div className="sec-confirm">
          <p className="muted">
            Devices that pair with it sign in to the account you pick,{' '}
            {addresses?.public ? 'pairing at the public address or at home.' : addresses?.tailnet ? 'pairing on your tailnet or at home.' : 'pairing at home only: this server has no public or tailnet address.'}
          </p>
          <div className="dev-lasting-form">
            <label>
              Account
              <select value={form.userId} onChange={(e) => setForm({ ...form, userId: e.target.value })}>
                {(members.length ? members : [{ id: user.id, name: `${user.displayName} (you)` }]).map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </select>
            </label>
            <label>
              Days
              <input type="number" min={1} max={90} value={form.days} onChange={(e) => setForm({ ...form, days: e.target.value })} />
            </label>
            <label>
              Devices
              <input type="number" min={1} max={50} value={form.devices} onChange={(e) => setForm({ ...form, devices: e.target.value })} />
            </label>
            <label className="dev-note">
              Note
              <input type="text" maxLength={60} placeholder="App Review, Mum’s iPad…" value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} />
            </label>
          </div>
          <div className="sec-row">
            <button type="button" className="pill primary" disabled={busy} onClick={make}>{busy ? 'Making…' : 'Make the code'}</button>
            <button type="button" className="link" onClick={() => setForm(null)}>Cancel</button>
          </div>
        </div>
      ) : (
        <p className="muted">
          <button type="button" className="link" onClick={open}>Make a code that lasts</button> for App Review, or someone who can’t come to your home network.
        </p>
      )}
      {codes.length > 0 && (
        <>
          <ul className="sec-list">
            {codes.map((c) => (
              <li key={c.id}>
                <Icon.key />
                <span>
                  <b>{c.note || c.name}</b>
                  <small>{c.note && `${c.name}’s account · `}{c.devices} of {devices(c.maxDevices)} paired · until {day(c.expiresAt)} · made {day(c.createdAt)}</small>
                </span>
                <button type="button" className="link" onClick={() => revoke(c)}>Revoke</button>
              </li>
            ))}
          </ul>
          <small className="muted">Revoking a code stops it pairing more devices. Ones it paired stay signed in: sign them out under “Where you’re signed in”, here or in that person’s Photostead.</small>
        </>
      )}
      {error && <p className="error" role="alert">{error}</p>}
    </div>
  );
}


/** Photostead's own Tailscale node: off, waiting for approval, or running at its HTTPS address. */
function Tailscale({ remote, onRemote }: { remote: Remote; onRemote: (r: Remote) => void }) {
  const ts = remote.tailscale;
  const [hostname, setHostname] = useState(ts.hostname || 'photostead');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  // Waiting for approval or a certificate: look again every two seconds.
  useEffect(() => {
    if (ts.status !== 'starting' && ts.status !== 'needs-login') return;
    const t = setInterval(() => api.remote().then(onRemote, () => {}), 2000);
    return () => clearInterval(t);
  }, [ts.status, onRemote]);
  const act = (f: () => Promise<Remote>) => {
    setBusy(true);
    setError('');
    f().then(onRemote, (e) => setError(message(e, 'That didn’t work. Try again.'))).finally(() => setBusy(false));
  };
  return (
    <section>
      <h3>Reach Photostead from anywhere</h3>
      <p className="muted">
        Photostead joins your Tailscale network as a device of its own, with a real HTTPS address. Your phone and computers with the Tailscale app reach it from anywhere,
        no ports opened and no domain needed. Passkeys work there.
      </p>
      {ts.status === 'running' && ts.url ? (
        <>
          <div className="dev-addr">
            <span className="dev-dot on" aria-hidden="true" />
            <a href={ts.url} target="_blank" rel="noreferrer">{ts.url.replace('https://', '')}</a>
            <Copy text={ts.url} />
          </div>
          <label className="check dev-funnel">
            <input type="checkbox" checked={ts.funnel} disabled={busy} onChange={(e) => act(() => api.setFunnel(e.target.checked))} />
            <span>
              Share links work from anywhere (Tailscale Funnel)
              <small className="muted">
                {ts.funnel && ts.devices
                  ? 'Share links and paired phones answer from the internet. Signing in on the web stays on your tailnet.'
                  : 'Only share links answer from the internet; the rest of Photostead stays on your tailnet.'}
              </small>
            </span>
          </label>
          {ts.funnel && (
            <label className="check dev-funnel dev-sub">
              <input type="checkbox" checked={ts.devices} disabled={busy} onChange={(e) => act(() => api.setFunnelDevices(e.target.checked))} />
              <span>
                Paired phones work from anywhere, without Tailscale
                <small className="muted">
                  Once a phone is paired, the Photostead app backs up and browses from anywhere, so nobody else needs Tailscale. Pairing a new phone and changing how
                  people sign in still happen at home or on your tailnet.
                </small>
              </span>
            </label>
          )}
          {ts.funnelError && (
            <p className="error" role="alert">
              Funnel didn’t open: {ts.funnelError}{' '}
              <a href="https://login.tailscale.com/admin/acls/file" target="_blank" rel="noreferrer">Allow Funnel in your Tailscale access controls</a>, then try again.
            </p>
          )}
          <div className="sec-row">
            <button type="button" className="pill" disabled={busy} onClick={() => act(() => api.setTailscale(false))}>Turn off</button>
            <button type="button" className="link" disabled={busy}
              onClick={() => window.confirm('Take Photostead off your tailnet? Turning it on again will need approving again in Tailscale.') && act(api.forgetTailscale)}>
              Leave the tailnet
            </button>
          </div>
        </>
      ) : ts.status === 'needs-login' ? (
        <div className="sec-confirm">
          <p>Approve Photostead in Tailscale, signed in as you. This page carries on once it’s in.</p>
          {ts.loginUrl ? (
            <a className="pill primary dev-go" href={ts.loginUrl} target="_blank" rel="noreferrer">Approve in Tailscale</a>
          ) : (
            <p className="muted">Waiting for Tailscale’s link…</p>
          )}
          <button type="button" className="link" onClick={() => act(() => api.setTailscale(false))}>Cancel</button>
        </div>
      ) : ts.status === 'starting' ? (
        <p className="muted">Joining the tailnet and getting a certificate…</p>
      ) : (
        <>
          {ts.status === 'error' && <p className="error" role="alert">{ts.error}</p>}
          <div className="sec-row">
            <label className="dev-host">
              <input value={hostname} onChange={(e) => setHostname(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))} aria-label="Name on the tailnet" maxLength={63} />
              <span>.your-tailnet.ts.net</span>
            </label>
            <button type="button" className="pill primary" disabled={busy || !hostname} onClick={() => act(() => api.setTailscale(true, hostname))}>
              {busy ? 'Starting…' : 'Turn on'}
            </button>
          </div>
        </>
      )}
      {error && <p className="error" role="alert">{error}</p>}
    </section>
  );
}

export function DevicesDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [remote, setRemote] = useState<Remote | null>(null);
  const [sessions, setSessions] = useState<SignedIn[]>([]);
  const [pairing, setPairing] = useState<{ code: string; expiresAt: string; qr: string; link: string; known: string[] } | null>(null);
  const [paired, setPaired] = useState('');
  const [, tick] = useState(0);
  const [error, setError] = useState('');
  const loadSessions = () => api.sessions().then((r) => {
    setSessions(r.sessions);
    return r.sessions;
  }, () => sessions);
  useEffect(() => {
    ref.current?.showModal();
    api.remote().then(setRemote, (e) => setError(message(e, 'Couldn’t read the remote access settings.')));
    void loadSessions();
  }, []);
  // The pairing code's countdown, until a device pairs with it (it's spent then).
  useEffect(() => {
    if (!pairing) return;
    const t = setInterval(() => {
      tick((n) => n + 1);
      void loadSessions().then((list) => {
        const fresh = list.find((x) => x.device && !pairing.known.includes(x.id));
        if (fresh) {
          setPairing(null);
          setPaired(fresh.device!);
        }
      });
    }, 3000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pairing]);
  const left = pairing ? Math.max(0, Math.round((new Date(pairing.expiresAt).getTime() - Date.now()) / 1000)) : 0;
  const a = remote?.addresses;
  return (
    <dialog ref={ref} className="zone-dialog sec-dialog dev-dialog" onClose={onClose} aria-labelledby="dev-title">
      <h2 id="dev-title">Devices & remote access</h2>
      {remote?.available && user.role === 'admin' && <Tailscale remote={remote} onRemote={setRemote} />}

      <section>
        <h3>Pair a phone or browser extension</h3>
        <p className="muted">
          The Photostead app scans the QR code; Photostead Capture takes the link. Either way the device signs in and learns this server’s addresses.
          {remote?.tailscale.funnel && remote.tailscale.devices && ' Pair it at home (or with Tailscale on); after that it works from anywhere.'}
        </p>
        {pairing && left > 0 ? (
          <div className="dev-pair">
            <img src={pairing.qr} alt="Pairing QR code" width={168} height={168} />
            <div>
              <b className="dev-code">{pairing.code}</b>
              <small className="muted">Works once, for {Math.floor(left / 60)}:{String(left % 60).padStart(2, '0')} more</small>
              <span className="dev-link">Photostead Capture: paste the link <Copy text={pairing.link} /></span>
            </div>
          </div>
        ) : (
          <>
            {paired && <p className="dev-paired"><Icon.check /> Paired {paired}</p>}
            <button type="button" className="pill" onClick={() => api.pairingCode().then((p) => {
              setPaired('');
              setPairing({ ...p, known: sessions.map((x) => x.id) });
            }, (e) => setError(message(e, 'Couldn’t make a code.')))}>
              <Icon.phone />
              {paired ? 'Pair another' : 'Show a pairing code'}
            </button>
          </>
        )}
        {a && (a.home || a.tailnet || a.public) && (
          <ul className="dev-addrs">
            {a.tailnet && <li><span>On your tailnet</span>{a.tailnet}</li>}
            {a.public && <li><span>In public</span>{a.public}</li>}
            {a.home && <li><span>At home</span>{a.home}</li>}
          </ul>
        )}
        {user.role === 'admin' && <LastingCodes user={user} addresses={a} />}
      </section>

      <section>
        <h3>Where you’re signed in</h3>
        <ul className="sec-list">
          {sessions.map((x) => (
            <li key={x.id}>
              {x.device ? <Icon.phone /> : <Icon.screen />}
              <span>
                <b>{x.device || browserOf(x.userAgent)}{x.current ? ' · this browser' : ''}</b>
                <small>{x.device ? `${x.platform ? `${x.platform} · ` : ''}paired ` : 'signed in '}{new Date(x.createdAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} · {ago(x.lastSeenAt)}</small>
              </span>
              {!x.current && (
                <button type="button" className="link" onClick={() => api.endSession(x.id).then(loadSessions, (e) => setError(message(e, 'Couldn’t sign it out.')))}>
                  Sign out
                </button>
              )}
            </li>
          ))}
        </ul>
      </section>

      {error && <p className="error" role="alert">{error}</p>}
      <div className="actions">
        <span className="grow" />
        <button type="button" onClick={() => ref.current?.close()}>Done</button>
      </div>
    </dialog>
  );
}
