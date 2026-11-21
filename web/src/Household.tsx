// The Household: who's in it, inviting someone (a link they open to make their account), the
// guests albums are shared with (for the server's admin, to remove), and your own auto-share rule.
// What's shared shows to every member beside their own items; the timeline's switch shows
// everything, only yours, or only the Household's.

import { useCallback, useEffect, useRef, useState } from 'react';
import { api, ApiError, type Guest, type Household, type Person, type ShareRule, type User } from './api';
import { Copy } from './copy';
import { Icon } from './icons';
import { personName } from './People';

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);
const ROLE = { owner: 'Owner', adult: 'Adult', member: 'Member' } as const;
const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
/** Whose guest they are, the albums shared with them, and where they're signed in. */
const guestLine = (g: Guest) => [
  g.addedBy ? `${g.addedBy}’s guest` : 'Guest',
  g.albums ? `in ${plural(g.albums, 'album', 'albums')}` : 'in no albums now',
  g.devices ? `signed in on ${plural(g.devices, 'device', 'devices')}` : '',
].filter(Boolean).join(' · ');


/** Your auto-share rule: off, everything, or from a day on, with any people or only some. */
function RuleEditor({ rule, onSave, busy }: { rule: ShareRule; onSave: (r: ShareRule) => void; busy: boolean }) {
  const [on, setOn] = useState(rule.enabled);
  const [from, setFrom] = useState(rule.from ?? '');
  const [people, setPeople] = useState<string[]>(rule.people ?? []);
  const [ai, setAi] = useState(Boolean(rule.includeAi));
  const [named, setNamed] = useState<Person[]>([]);
  useEffect(() => {
    api.people().then((r) => setNamed(r.people.filter((p) => p.name && !p.hidden)), () => {});
  }, []);
  const changed = on !== rule.enabled || from !== (rule.from ?? '') || ai !== Boolean(rule.includeAi) || people.join() !== (rule.people ?? []).join();
  return (
    <div className="hh-rule">
      <label className="check">
        <input type="checkbox" checked={on} onChange={(e) => setOn(e.target.checked)} />
        <span>Share my photos and videos automatically</span>
      </label>
      {on && (
        <>
          <div className="sec-row">
            <span className="muted">Taken</span>
            <select value={from ? 'from' : 'all'} onChange={(e) => setFrom(e.target.value === 'all' ? '' : new Date().toISOString().slice(0, 10))} aria-label="Which dates">
              <option value="all">any time</option>
              <option value="from">from a day on</option>
            </select>
            {from && <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} aria-label="From" />}
          </div>
          {named.length > 0 && (
            <div className="hh-people">
              <span className="muted">{people.length ? 'Only with' : 'With anyone, or only with'}</span>
              {named.map((p) => (
                <button key={p.id} type="button" className="chip" aria-pressed={people.includes(p.id)}
                  onClick={() => setPeople((x) => (x.includes(p.id) ? x.filter((i) => i !== p.id) : [...x, p.id]))}>
                  {personName(p)}
                </button>
              ))}
            </div>
          )}
          <label className="check">
            <input type="checkbox" checked={ai} onChange={(e) => setAi(e.target.checked)} />
            <span>Include AI-made images</span>
          </label>
          <p className="muted">Screenshots and documents stay yours. Anything taken out of the Household stays out.</p>
        </>
      )}
      {changed && (
        <button type="button" className="pill primary" disabled={busy} onClick={() => onSave({ enabled: on, from: on ? from : undefined, people: on ? people : [], includeAi: on && ai })}>
          {busy ? 'Saving…' : 'Save'}
        </button>
      )}
    </div>
  );
}

export function HouseholdDialog({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const [h, setH] = useState<Household | null>(null);
  const [name, setName] = useState('');
  const [invite, setInvite] = useState<{ name: string; role: 'adult' | 'member' }>({ name: '', role: 'adult' });
  const [link, setLink] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const show = (x: Household) => {
    setH(x);
    setName(x.name);
  };
  useEffect(() => {
    ref.current?.showModal();
    api.household().then(show, (e) => setError(message(e, 'Couldn’t read the household.')));
  }, []);
  const act = (f: () => Promise<Household | void>) => {
    setBusy(true);
    setError('');
    f().then((x) => {
      if (x) show(x);
      else return api.household().then(show);
    }).catch((e) => setError(message(e, 'That didn’t work. Try again.'))).finally(() => setBusy(false));
  };
  if (!h) return <dialog ref={ref} className="zone-dialog sec-dialog" onClose={onClose} aria-busy="true">{error && <p className="error">{error}</p>}</dialog>;
  return (
    <dialog ref={ref} className="zone-dialog sec-dialog hh-dialog" onClose={onClose} aria-labelledby="hh-title">
      {h.canManage ? (
        <div className="hh-name">
          <input id="hh-title" value={name} onChange={(e) => setName(e.target.value)} aria-label="Household name" maxLength={100} />
          {name.trim() && name !== h.name && <button type="button" className="pill" disabled={busy} onClick={() => act(() => api.renameHousehold(name.trim()))}>Rename</button>}
        </div>
      ) : (
        <h2 id="hh-title">{h.name}</h2>
      )}
      <p className="muted">
        {h.shared.toLocaleString()} {h.shared === 1 ? 'item' : 'items'} shared. Everyone keeps their own library; what’s shared shows to every member beside their own.
      </p>

      <section>
        <h3>Members</h3>
        <ul className="sec-list">
          {h.members.map((m) => (
            <li key={m.id}>
              <Icon.person />
              <span>
                <b>{m.name}{m.me ? ' · you' : ''}</b>
                <small>{m.email} · {m.shared.toLocaleString()} shared</small>
              </span>
              {h.canManage && m.role !== 'owner' && !m.me ? (
                <select value={m.role} disabled={busy} onChange={(e) => act(() => api.setMemberRole(m.id, e.target.value as 'adult' | 'member'))} aria-label={`${m.name}’s role`}>
                  <option value="adult">Adult</option>
                  <option value="member">Member</option>
                </select>
              ) : (
                <small className="hh-role">{ROLE[m.role]}</small>
              )}
            </li>
          ))}
        </ul>
      </section>

      {h.canManage && (
        <section>
          <h3>Invite someone</h3>
          <p className="muted">They open the link, choose their name and password, and join. It works once, for 7 days, at this address.</p>
          {link ? (
            <div className="sec-confirm">
              <div className="dev-addr"><span className="link-text">{link}</span><Copy text={link} /></div>
              <button type="button" className="link" onClick={() => setLink('')}>Invite someone else</button>
            </div>
          ) : (
            <div className="sec-row">
              <input className="hh-input" value={invite.name} onChange={(e) => setInvite({ ...invite, name: e.target.value })} placeholder="Their name (optional)" aria-label="Their name" maxLength={100} />
              <select value={invite.role} onChange={(e) => setInvite({ ...invite, role: e.target.value as 'adult' | 'member' })} aria-label="Role">
                <option value="adult">Adult</option>
                <option value="member">Member</option>
              </select>
              <button type="button" className="pill primary" disabled={busy} onClick={() => act(async () => {
                const r = await api.invite({ name: invite.name.trim() || undefined, role: invite.role });
                setLink(r.url);
                setInvite({ name: '', role: 'adult' });
              })}>
                Make a link
              </button>
            </div>
          )}
          {h.invites.length > 0 && (
            <ul className="sec-list">
              {h.invites.map((i) => (
                <li key={i.id}>
                  <Icon.clock />
                  <span>
                    <b>{i.name || i.email || 'Invitation'}</b>
                    <small>{ROLE[i.role]} · waiting until {new Date(i.expiresAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</small>
                  </span>
                  <button type="button" className="link" onClick={() => act(() => api.cancelInvite(i.id))}>Cancel</button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {h.guests.length > 0 && (
        <section>
          <h3>Guests</h3>
          <p className="muted">People albums are shared with from outside the household. They see only those albums.</p>
          <ul className="sec-list">
            {h.guests.map((g) => (
              <li key={g.id}>
                <Icon.person />
                <span>
                  <b>{g.name}</b>
                  <small>{guestLine(g)}</small>
                </span>
                <button type="button" className="link" disabled={busy} onClick={() => window.confirm(
                  `Remove ${g.name}? They’re signed out on every device, their link stops working, and their comments and picks go. The albums and photos stay, with anything they added.`,
                ) && act(() => api.removeGuest(g.id))}>
                  Remove
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}

      <section>
        <h3>Your sharing</h3>
        <p className="muted">Share by hand from a selection, or let a rule share for you as new photos come in.</p>
        <RuleEditor key={JSON.stringify(h.rule)} rule={h.rule} busy={busy} onSave={(r) => act(async () => {
          const x = await api.setShareRule(r);
          onChanged();
          return x;
        })} />
      </section>

      {error && <p className="error" role="alert">{error}</p>}
      <div className="actions">
        <span className="grow" />
        <button type="button" onClick={() => ref.current?.close()}>Done</button>
      </div>
    </dialog>
  );
}

/** Someone opening an invitation. To the household: their name, email and a password, and
 *  they're in. A guest's (to an album): it opens at once, with no account to make. Signed in here
 *  as someone else, it says so rather than switching. */
export function AcceptInvite({ token, user, mark, onDone }: { token: string; user: User | null; mark: React.ReactNode; onDone: (u: User) => void }) {
  const [info, setInfo] = useState<{ household: string; invitedBy: string; name?: string; email?: string; guest?: boolean } | null>(null);
  const [gone, setGone] = useState('');
  const [elsewhere, setElsewhere] = useState(''); // a guest's invitation where someone's signed in
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api.invitation(token).then(setInfo, (e) => setGone(message(e, 'This invitation can’t be opened.')));
  }, [token]);
  const openAsGuest = useCallback(() => {
    setElsewhere('');
    api.openGuestInvite(token).then(
      () => window.location.replace('/'),
      (e) => (e instanceof ApiError && e.status === 409 ? setElsewhere(e.message) : setGone(message(e, 'This invitation can’t be opened.'))),
    );
  }, [token]);
  useEffect(() => {
    if (info?.guest) openAsGuest();
  }, [info, openAsGuest]);
  const signOut = (then: () => void) => void api.logout().finally(then);
  const home = () => {
    window.history.replaceState(null, '', '/');
    window.location.reload();
  };
  const submit = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const f = new FormData(e.currentTarget);
    if (f.get('password') !== f.get('again')) return setError('The two passwords aren’t the same.');
    setBusy(true);
    setError('');
    try {
      const u = await api.acceptInvite(token, { displayName: String(f.get('name')).trim(), email: String(f.get('email')).trim(), password: String(f.get('password')) });
      window.history.replaceState(null, '', '/#/');
      onDone(u);
    } catch (err) {
      setError(message(err, 'Couldn’t make your account. Try again.'));
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login">
      <form className="setup" onSubmit={submit}>
        <div className="brand">{mark} <span>Photostead</span></div>
        {gone ? (
          <>
            <h1>That invitation is gone</h1>
            <p className="lede">{gone}</p>
            <a className="link" href="/#/">Sign in instead</a>
          </>
        ) : !info || (info.guest && !elsewhere) ? (
          <p className="lede">Opening the invitation…</p>
        ) : info.guest ? (
          <>
            <h1>You’re signed in as someone else</h1>
            <p className="lede">{elsewhere}</p>
            <button type="button" onClick={() => signOut(openAsGuest)}>Sign out and open it</button>
          </>
        ) : user ? (
          <>
            <h1>You’re signed in</h1>
            <p className="lede">You’re signed in here as {user.displayName || user.email}. This invitation is for someone new joining the household: open it in a private window, or sign out first.</p>
            <button type="button" onClick={() => signOut(() => window.location.reload())}>Sign out</button>
            <button type="button" className="link" onClick={home}>Back to my library</button>
          </>
        ) : (
          <>
            <h1>Join {info.household || 'the household'}</h1>
            <p className="lede">{info.invitedBy ? `${info.invitedBy} invited you. ` : ''}Your photos stay yours; what anyone shares shows to everyone in the household.</p>
            <label>Your name<input name="name" defaultValue={info.name ?? ''} required maxLength={100} autoFocus /></label>
            <label>Email<input name="email" type="email" defaultValue={info.email ?? ''} required autoComplete="username" /></label>
            <label>Password<input name="password" type="password" minLength={12} required autoComplete="new-password" placeholder="At least 12 characters" /></label>
            <label>The same password again<input name="again" type="password" minLength={12} required autoComplete="new-password" /></label>
            {error && <p className="error" role="alert">{error}</p>}
            <button type="submit" disabled={busy}>{busy ? 'Joining…' : 'Join'}</button>
          </>
        )}
      </form>
    </main>
  );
}
