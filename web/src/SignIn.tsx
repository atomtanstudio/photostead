// Signing in, and how you sign in: a password, then a code from an authenticator app if two-step
// sign-in is on; a passkey (nothing typed; browsers offer them only over HTTPS or on localhost); or
// the household's single sign-on provider, when the server has one.

import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type Security, type User } from './api';
import { Copy } from './copy';
import { Icon } from './icons';

// WebAuthn speaks in bytes; the server sends and takes base64url.
const fromB64 = (s: string) => {
  const t = s.replace(/-/g, '+').replace(/_/g, '/');
  return Uint8Array.from(atob(t + '='.repeat((4 - (t.length % 4)) % 4)), (c) => c.charCodeAt(0));
};
const toB64 = (buf: ArrayBuffer | null) => {
  if (!buf) return null;
  let s = '';
  new Uint8Array(buf).forEach((b) => (s += String.fromCharCode(b)));
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
};
type Desc = { id: string; type: 'public-key'; transports?: AuthenticatorTransport[] };
const descs = (list?: Desc[]) => list?.map((d) => ({ ...d, id: fromB64(d.id) }));

/** Whether this page can use passkeys at all (a secure context with WebAuthn). */
export const passkeysHere = () => window.isSecureContext && 'PublicKeyCredential' in window;

/** Makes a passkey from the server's options, and gives back what the server checks. */
async function makePasskey(options: { publicKey: Record<string, unknown> }) {
  const pk = options.publicKey as unknown as Omit<PublicKeyCredentialCreationOptions, 'challenge' | 'user' | 'excludeCredentials'> & {
    challenge: string; user: { id: string; name: string; displayName: string }; excludeCredentials?: Desc[] };
  const cred = (await navigator.credentials.create({
    publicKey: { ...pk, challenge: fromB64(pk.challenge), user: { ...pk.user, id: fromB64(pk.user.id) }, excludeCredentials: descs(pk.excludeCredentials) },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error('cancelled');
  const r = cred.response as AuthenticatorAttestationResponse;
  return { id: cred.id, rawId: toB64(cred.rawId), type: cred.type, response: { clientDataJSON: toB64(r.clientDataJSON), attestationObject: toB64(r.attestationObject),
    transports: r.getTransports?.() }, clientExtensionResults: cred.getClientExtensionResults(), authenticatorAttachment: cred.authenticatorAttachment };
}

/** Signs the server's challenge with a passkey the browser has for this address. */
async function usePasskey(options: { publicKey: Record<string, unknown> }) {
  const pk = options.publicKey as unknown as Omit<PublicKeyCredentialRequestOptions, 'challenge' | 'allowCredentials'> & { challenge: string; allowCredentials?: Desc[] };
  const cred = (await navigator.credentials.get({ publicKey: { ...pk, challenge: fromB64(pk.challenge), allowCredentials: descs(pk.allowCredentials) } })) as PublicKeyCredential | null;
  if (!cred) throw new Error('cancelled');
  const r = cred.response as AuthenticatorAssertionResponse;
  return { id: cred.id, rawId: toB64(cred.rawId), type: cred.type, response: { clientDataJSON: toB64(r.clientDataJSON), authenticatorData: toB64(r.authenticatorData),
    signature: toB64(r.signature), userHandle: toB64(r.userHandle) }, clientExtensionResults: cred.getClientExtensionResults(), authenticatorAttachment: cred.authenticatorAttachment };
}

const cancelled = (e: unknown) => e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError');
const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

export function Login({ signOn, internet, onSignedIn, mark }: { signOn?: string; internet?: boolean; onSignedIn: (u: User) => void; mark: React.ReactNode }) {
  // A message from a single sign-on trip that didn't end in a session (?signin=…).
  const [error, setError] = useState(() => new URLSearchParams(window.location.search).get('signin') ?? '');
  const [busy, setBusy] = useState(false);
  const [ticket, setTicket] = useState(''); // waiting for the two-step code
  const [code, setCode] = useState('');
  useEffect(() => {
    if (window.location.search) window.history.replaceState(null, '', window.location.pathname + window.location.hash);
  }, []);
  const run = async (f: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try {
      await f();
    } finally {
      setBusy(false);
    }
  };
  const password = (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    void run(async () => {
      try {
        const r = await api.login(String(form.get('email')), String(form.get('password')));
        if (r.user) onSignedIn(r.user);
        else if (r.twoStep && r.ticket) setTicket(r.ticket);
      } catch (err) {
        setError(err instanceof ApiError && err.status === 401 ? 'That email and password don’t match.' : 'Couldn’t reach the server. Try again.');
      }
    });
  };
  const second = (e: React.FormEvent) => {
    e.preventDefault();
    void run(async () => {
      try {
        onSignedIn(await api.secondStep(ticket, code));
      } catch (err) {
        setCode('');
        if (err instanceof ApiError && err.status === 401) setTicket(''); // ran out: back to the password
        setError(message(err, 'Couldn’t reach the server. Try again.'));
      }
    });
  };
  const passkey = () =>
    run(async () => {
      try {
        const begin = await api.passkeySignInBegin();
        onSignedIn(await api.passkeySignInFinish(begin.flow, await usePasskey(begin.options)));
      } catch (err) {
        if (!cancelled(err)) setError(message(err, 'That passkey didn’t sign you in.'));
      }
    });
  if (ticket) {
    return (
      <main className="login">
        <form onSubmit={second} aria-describedby={error ? 'login-error' : undefined}>
          <div className="brand">{mark} <span>Photostead</span></div>
          <h1 className="login-step">Two-step sign-in</h1>
          <p className="lede">Type the code your authenticator app shows for Photostead, or one of your recovery codes.</p>
          <label>
            Code
            <input value={code} onChange={(e) => setCode(e.target.value.trim())} inputMode="text" autoComplete="one-time-code" required autoFocus
              className="code" maxLength={20} placeholder="123 456" />
          </label>
          {error && <p id="login-error" className="error" role="alert">{error}</p>}
          <button type="submit" disabled={busy || code.length < 6}>{busy ? 'Checking…' : 'Sign in'}</button>
          <button type="button" className="link" onClick={() => { setTicket(''); setError(''); }}>Back</button>
        </form>
      </main>
    );
  }
  return (
    <main className="login">
      <form onSubmit={password} aria-describedby={error ? 'login-error' : undefined}>
        <div className="brand">{mark} <span>Photostead</span></div>
        {internet && <p className="lede">Here, over the internet, only guests can sign in. Household members: turn on Tailscale on this device.</p>}
        <label>
          Email
          <input name="email" type="email" autoComplete="username webauthn" required autoFocus />
        </label>
        <label>
          Password
          <input name="password" type="password" autoComplete="current-password" required />
        </label>
        {error && <p id="login-error" className="error" role="alert">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
        {(passkeysHere() || signOn) && <p className="login-or"><span>or</span></p>}
        {passkeysHere() && (
          <button type="button" className="secondary" onClick={() => void passkey()} disabled={busy}>
            <Icon.key />
            Sign in with a passkey
          </button>
        )}
        {signOn && (
          <a className="button secondary" href="/api/v1/auth/oidc/start">
            Sign in with {signOn}
          </a>
        )}
      </form>
    </main>
  );
}

const day = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : '');

/** Asks for the password again before a new way in is added (skipped for an account without one). */
function Confirm({ hasPassword, label, extra, onConfirm, onCancel, busy }: {
  hasPassword: boolean;
  label: string;
  extra?: React.ReactNode;
  onConfirm: (password: string) => void;
  onCancel: () => void;
  busy: boolean;
}) {
  const [pw, setPw] = useState('');
  return (
    <div className="sec-confirm">
      {extra}
      {hasPassword && (
        <input type="password" autoComplete="current-password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder="Your password" aria-label="Your password" autoFocus
          onKeyDown={(e) => e.key === 'Enter' && (e.preventDefault(), onConfirm(pw))} />
      )}
      <div className="sec-row">
        <button type="button" className="pill" onClick={onCancel}>Cancel</button>
        <button type="button" className="pill primary" disabled={busy || (hasPassword && !pw)} onClick={() => onConfirm(pw)}>{label}</button>
      </div>
    </div>
  );
}

/** Sign-in & security: two-step sign-in, passkeys and single sign-on for the signed-in person. */
export function SecurityDialog({ onClose }: { onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [sec, setSec] = useState<Security | null>(null);
  const [step, setStep] = useState<'' | 'two-on' | 'two-scan' | 'two-codes' | 'two-off' | 'two-new' | 'key-add' | 'link'>('');
  const [scan, setScan] = useState<{ secret: string; qr: string } | null>(null);
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState<string[]>([]);
  const [keyName, setKeyName] = useState(() => (/Mac/.test(navigator.userAgent) ? 'This Mac' : /iPhone|iPad/.test(navigator.userAgent) ? 'This iPhone' : 'This device'));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const reload = () => api.security().then(setSec, (e) => setError(message(e, 'Couldn’t read your sign-in settings.')));
  useEffect(() => {
    ref.current?.showModal();
    void reload();
  }, []);
  const act = async (f: () => Promise<void>) => {
    setBusy(true);
    setError('');
    try {
      await f();
    } catch (e) {
      if (!cancelled(e)) setError(message(e, 'That didn’t work. Try again.'));
    } finally {
      setBusy(false);
    }
  };
  const done = () => {
    setStep('');
    setCode('');
    void reload();
  };
  if (!sec) return <dialog ref={ref} className="zone-dialog sec-dialog" onClose={onClose} aria-busy="true" />;
  const hasPw = sec.hasPassword;
  return (
    <dialog ref={ref} className="zone-dialog sec-dialog" onClose={onClose} aria-labelledby="sec-title">
      <h2 id="sec-title">Sign-in & security</h2>

      <section>
        <h3>Two-step sign-in</h3>
        <p className="muted">After your password, a six-digit code from an authenticator app on your phone (1Password, Google Authenticator, Authy…).</p>
        {step === 'two-codes' ? (
          <div className="sec-confirm">
            <p><b>Save these recovery codes.</b> Each signs you in once if your phone isn’t at hand. They won’t be shown again.</p>
            <ul className="sec-codes">{codes.map((c) => <li key={c}>{c}</li>)}</ul>
            <div className="sec-row">
              <Copy text={codes.join('\n')} className="pill" />
              <button type="button" className="pill primary" onClick={done}>I’ve saved them</button>
            </div>
          </div>
        ) : step === 'two-scan' && scan ? (
          <div className="sec-confirm">
            <img className="sec-qr" src={scan.qr} alt="QR code for your authenticator app" width={180} height={180} />
            <p className="muted">Scan it with the app, or type this key: <code className="sec-key">{scan.secret.replace(/(.{4})/g, '$1 ').trim()}</code></p>
            <input className="code" inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
              placeholder="The code the app shows" aria-label="Code from the app" maxLength={6} autoFocus />
            <div className="sec-row">
              <button type="button" className="pill" onClick={done}>Cancel</button>
              <button type="button" className="pill primary" disabled={busy || code.length !== 6}
                onClick={() => void act(async () => { const r = await api.twoStepConfirm(code); setCodes(r.recoveryCodes); setStep('two-codes'); })}>
                Turn on
              </button>
            </div>
          </div>
        ) : step === 'two-on' || step === 'two-off' || step === 'two-new' ? (
          <Confirm hasPassword={hasPw} busy={busy} onCancel={done}
            label={step === 'two-on' ? 'Continue' : step === 'two-off' ? 'Turn off' : 'Make new codes'}
            onConfirm={(pw) => void act(async () => {
              if (step === 'two-on') {
                setScan(await api.twoStepSetup(pw));
                setStep('two-scan');
              } else if (step === 'two-off') {
                await api.twoStepOff(pw);
                done();
              } else {
                setCodes((await api.twoStepRecovery(pw)).recoveryCodes);
                setStep('two-codes');
              }
            })} />
        ) : sec.twoStep ? (
          <div className="sec-row">
            <span className="sec-on">On · {sec.recoveryLeft} recovery {sec.recoveryLeft === 1 ? 'code' : 'codes'} left</span>
            <button type="button" className="pill" onClick={() => setStep('two-new')}>New recovery codes</button>
            <button type="button" className="pill" onClick={() => setStep('two-off')}>Turn off</button>
          </div>
        ) : (
          <button type="button" className="pill" onClick={() => setStep('two-on')}>Turn on</button>
        )}
      </section>

      <section>
        <h3>Passkeys</h3>
        <p className="muted">Sign in with your face, fingerprint or device PIN, nothing typed. A passkey belongs to the address you made it at.</p>
        <ul className="sec-list">
          {sec.passkeys.map((k) => (
            <li key={k.id}>
              <Icon.key />
              <span>
                <b>{k.name}</b>
                <small>Added {day(k.createdAt)}{k.lastUsedAt ? ` · last used ${day(k.lastUsedAt)}` : ''}</small>
              </span>
              <button type="button" className="link" onClick={() => void act(async () => { await api.removePasskey(k.id); done(); })}>Remove</button>
            </li>
          ))}
        </ul>
        {!passkeysHere() ? (
          <p className="muted sec-note">
            Passkeys need Photostead reached over HTTPS: a domain, Tailscale or a Cloudflare Tunnel. Here it’s {window.location.origin}.
          </p>
        ) : step === 'key-add' ? (
          <Confirm hasPassword={hasPw} busy={busy} onCancel={done} label="Add the passkey"
            extra={<input value={keyName} onChange={(e) => setKeyName(e.target.value)} placeholder="What to call it" aria-label="Passkey name" maxLength={100} />}
            onConfirm={(pw) => void act(async () => {
              const begin = await api.passkeyAddBegin(pw);
              await api.passkeyAddFinish(begin.flow, keyName, await makePasskey(begin.options));
              done();
            })} />
        ) : (
          <button type="button" className="pill" onClick={() => setStep('key-add')}>Add a passkey</button>
        )}
      </section>

      {sec.signOn && (
        <section>
          <h3>Single sign-on</h3>
          <p className="muted">Sign in with {sec.signOn}, which asks for its own second step.</p>
          <ul className="sec-list">
            {sec.signOnLinks.map((l) => (
              <li key={l.id}>
                <Icon.person />
                <span><b>{l.email}</b></span>
                <button type="button" className="link" onClick={() => void act(async () => { await api.unlinkSignOn(l.id); done(); })}>Unlink</button>
              </li>
            ))}
          </ul>
          {step === 'link' ? (
            <Confirm hasPassword={hasPw} busy={busy} onCancel={done} label={`Go to ${sec.signOn}`}
              onConfirm={(pw) => void act(async () => { window.location.href = (await api.linkSignOn(pw)).url; })} />
          ) : sec.signOnLinks.length === 0 && (
            <button type="button" className="pill" onClick={() => setStep('link')}>Link {sec.signOn}</button>
          )}
        </section>
      )}

      {error && <p className="error" role="alert">{error}</p>}
      <div className="actions">
        <span className="grow" />
        <button type="button" onClick={() => ref.current?.close()}>Done</button>
      </div>
    </dialog>
  );
}
