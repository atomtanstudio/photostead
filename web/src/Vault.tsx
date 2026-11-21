// The vault: private items behind a PIN. They're left out of everything else (the timeline,
// collections, search, People, sharing); they show here, to their owner, once it's unlocked. It
// locks itself 10 minutes after it was last used, and when you leave it.

import { useEffect, useRef, useState } from 'react';
import { api, ApiError, type VaultState } from './api';
import { Icon } from './icons';

const PIN = /^[0-9]{4,12}$/;

function message(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback;
}

/** A PIN field: digits only, hidden as they're typed. */
function PinInput({ value, onChange, label, autoFocus }: { value: string; onChange: (v: string) => void; label: string; autoFocus?: boolean }) {
  return (
    <input className="pin" type="password" inputMode="numeric" autoComplete="off" pattern="[0-9]*" maxLength={12} value={value} aria-label={label}
      placeholder={label} autoFocus={autoFocus} onChange={(e) => onChange(e.target.value.replace(/\D/g, ''))} />
  );
}

/** A new PIN, typed twice. */
function NewPin({ pin, again, setPin, setAgain, autoFocus }: { pin: string; again: string; setPin: (v: string) => void; setAgain: (v: string) => void; autoFocus?: boolean }) {
  return (
    <>
      <PinInput value={pin} onChange={setPin} label="New PIN (4 to 12 digits)" autoFocus={autoFocus} />
      <PinInput value={again} onChange={setAgain} label="The same PIN again" />
    </>
  );
}

const newPinProblem = (pin: string, again: string) =>
  !PIN.test(pin) ? 'A PIN is 4 to 12 digits.' : pin !== again ? 'The two PINs aren’t the same.' : '';

/** The vault while it's locked: its first PIN, unlocking it, or a new PIN when it's forgotten. */
export function VaultLock({ state, onOpen }: { state: VaultState; onOpen: (s: VaultState) => void }) {
  const [mode, setMode] = useState<'set' | 'unlock' | 'forgot'>(state.hasPin ? 'unlock' : 'set');
  const [pin, setPin] = useState('');
  const [again, setAgain] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const wait = state.waitUntil && new Date(state.waitUntil) > new Date() ? new Date(state.waitUntil) : null;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const problem = mode === 'unlock' ? (PIN.test(pin) ? '' : 'A PIN is 4 to 12 digits.') : newPinProblem(pin, again);
    if (problem) return setError(problem);
    setBusy(true);
    setError('');
    try {
      onOpen(mode === 'unlock' ? await api.unlockVault(pin) : await api.setVaultPin(pin, mode === 'forgot' ? { password } : undefined));
    } catch (err) {
      setError(message(err, 'Couldn’t open the vault. Try again.'));
      setPin('');
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="scroller page">
      <header className="results-head">
        <div>
          <p className="kicker">Library</p>
          <h1>Vault</h1>
          <p className="muted">Private photos and videos, behind a PIN</p>
        </div>
      </header>
      <form className="vault-lock" onSubmit={submit}>
        <span className="vl-icon"><Icon.lock /></span>
        {mode === 'set' && (
          <>
            <h2>Set up your vault</h2>
            <p>Items you move here are kept out of the timeline, search, People, albums and anything you share. Only you can see them, after typing this PIN.</p>
            <NewPin pin={pin} again={again} setPin={setPin} setAgain={setAgain} autoFocus />
          </>
        )}
        {mode === 'unlock' && (
          <>
            <h2>The vault is locked</h2>
            <p>Type its PIN to open it. It locks itself again after 10 minutes, or when you leave it.</p>
            <PinInput value={pin} onChange={setPin} label="PIN" autoFocus />
          </>
        )}
        {mode === 'forgot' && (
          <>
            <h2>A new PIN</h2>
            <p>Forgot the PIN? Your account’s password sets a new one. Nothing in the vault is lost.</p>
            <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Your password" aria-label="Your password" autoFocus />
            <NewPin pin={pin} again={again} setPin={setPin} setAgain={setAgain} />
          </>
        )}
        {error && <p className="error" role="alert">{error}</p>}
        {wait && !error && <p className="error" role="alert">Too many wrong PINs. Try again after {wait.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}.</p>}
        <button type="submit" className="pill primary" disabled={busy || (mode === 'forgot' && !password)}>
          {mode === 'set' ? 'Set the PIN and open' : mode === 'forgot' ? 'Set the new PIN' : busy ? 'Opening…' : 'Unlock'}
        </button>
        {mode === 'unlock' && (
          <button type="button" className="link" onClick={() => { setMode('forgot'); setError(''); setPin(''); }}>
            Forgot the PIN?
          </button>
        )}
        {mode === 'forgot' && (
          <button type="button" className="link" onClick={() => { setMode('unlock'); setError(''); setPin(''); setAgain(''); }}>
            Back
          </button>
        )}
      </form>
    </div>
  );
}

/**
 * A PIN in a dialog: the vault's first (when items are moved in before it has one), or a new one in
 * place of the current.
 */
export function PinDialog({ mode, onClose, onDone }: { mode: 'set' | 'change'; onClose: () => void; onDone: (s: VaultState) => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [current, setCurrent] = useState('');
  const [pin, setPin] = useState('');
  const [again, setAgain] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    ref.current?.showModal();
    ref.current?.querySelector('input')?.focus();
  }, []);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const problem = (mode === 'change' && !PIN.test(current) ? 'Type the current PIN.' : '') || newPinProblem(pin, again);
    if (problem) return setError(problem);
    setBusy(true);
    setError('');
    try {
      onDone(await api.setVaultPin(pin, mode === 'change' ? { currentPin: current } : undefined));
    } catch (err) {
      setError(message(err, 'Couldn’t set the PIN. Try again.'));
    } finally {
      setBusy(false);
    }
  };
  return (
    <dialog ref={ref} className="zone-dialog" onClose={onClose} aria-labelledby="pin-title">
      <form onSubmit={submit} className="pin-form">
        <h2 id="pin-title">{mode === 'set' ? 'Set up your vault' : 'Change the vault’s PIN'}</h2>
        <p className="muted">
          {mode === 'set'
            ? 'Items in the vault are kept out of everything else, and shown only after typing this PIN.'
            : 'The vault locks everywhere else you’re signed in.'}
        </p>
        {mode === 'change' && <PinInput value={current} onChange={setCurrent} label="Current PIN" />}
        <NewPin pin={pin} again={again} setPin={setPin} setAgain={setAgain} />
        {error && <p className="error" role="alert">{error}</p>}
        <div className="actions">
          <span className="grow" />
          <button type="button" className="secondary" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" disabled={busy}>
            {mode === 'set' ? 'Set the PIN' : 'Change it'}
          </button>
        </div>
      </form>
    </dialog>
  );
}
