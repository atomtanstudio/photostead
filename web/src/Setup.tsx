// The setup checklist: what's left to make Photostead ready for a household, at the top of the
// home timeline for the admin. Each step is marked done from what the server says wherever it can
// (photos in, a phone paired, reachable from away, someone else in the household); a second copy
// of the photos can't be seen from here, so the admin says so. Any step can be skipped, and the
// whole list put away: it's back in the sidebar while steps are left.

import { useEffect, useState } from 'react';
import { api, type Counts, type Household, type Remote, type ServerStatus, type SetupSkip, type SignedIn, type User } from './api';
import { Icon } from './icons';

export type SetupStepId = SetupSkip;

export type SetupStep = {
  id: SetupStepId;
  title: string;
  text: string; // what to do, or, once done, what's there
  action: string;
  done: boolean; // as the server says
  skipped: boolean; // the admin skipped it, or did it elsewhere
};

type Facts = { remote?: Remote; sessions?: SignedIn[]; household?: Household };

const settled = <T,>(r: PromiseSettledResult<T>) => (r.status === 'fulfilled' ? r.value : undefined);
const many = (n: number, one: string, more: string) => `${n.toLocaleString()} ${n === 1 ? one : more}`;
const host = (url?: string) => (url ? url.replace(/^https?:\/\//, '').replace(/\/.*$/, '') : '');

/** The checklist's steps for the admin (none for anyone else). Looked at again when `again` changes:
 * after a dialog that can finish a step closes. */
export function useSetup(user: User, counts: Counts | null, status: ServerStatus | null, again: number): SetupStep[] {
  const admin = user.role === 'admin';
  const [facts, setFacts] = useState<Facts>({});
  useEffect(() => {
    if (!admin) return;
    let live = true;
    void Promise.allSettled([api.remote(), api.sessions(), api.household()]).then(([r, s, h]) => {
      if (live) setFacts({ remote: settled(r), sessions: settled(s)?.sessions, household: settled(h) });
    });
    return () => {
      live = false;
    };
  }, [admin, again]);
  if (!admin) return [];

  const skipped = new Set(user.settings?.setupSkipped ?? []);
  const items = counts?.all ?? 0;
  const indexing = status?.indexing ?? 0;
  const phones = (facts.sessions ?? []).filter((x) => x.device && (x.platform === 'ios' || x.platform === 'android'));
  const ts = facts.remote?.tailscale;
  const away = ts?.status === 'running' ? host(ts.url) : host(facts.remote?.addresses.public);
  const hh = facts.household;
  const others = hh ? hh.members.length - 1 : 0;
  const invited = hh?.invites.length ?? 0;

  const steps: Omit<SetupStep, 'skipped'>[] = [
    {
      id: 'photos',
      title: 'Bring your photos in',
      done: items > 0,
      text: items > 0 ? `${many(items, 'photo or video', 'photos and videos')} so far${indexing ? `, ${many(indexing, 'file', 'files')} still being read` : ''}`
        : indexing ? `Reading ${many(indexing, 'file', 'files')}: they show up here as they’re ready.`
        : 'From Google Photos, Apple Photos, Immich or any folder. Drop it anywhere on this page.',
      action: 'Show me how',
    },
    {
      id: 'phone',
      title: 'Back up your phone',
      done: phones.length > 0,
      text: phones.length > 0 ? `${phones.map((p) => p.device).join(', ')} ${phones.length === 1 ? 'is' : 'are'} paired`
        : 'Photostead for iPhone sends new photos and videos here by itself. Pair it with a code.',
      action: 'Pair a phone',
    },
    {
      id: 'away',
      title: 'Reach it away from home',
      done: Boolean(away),
      text: away ? `At ${away}`
        : ts?.status === 'needs-login' ? 'Waiting for you to approve Photostead in Tailscale.'
        : 'Photostead joins your Tailscale network, so your phones and share links work from anywhere.',
      action: ts?.status === 'needs-login' ? 'Approve' : 'Set up',
    },
    {
      id: 'household',
      title: 'Invite your household',
      done: others > 0 || invited > 0,
      text: others > 0 ? `${many(others + 1, 'person', 'people')} in ${hh?.name || 'the household'}${invited ? `, ${many(invited, 'invitation', 'invitations')} out` : ''}`
        : invited > 0 ? `${many(invited, 'invitation', 'invitations')} out`
        : 'Everyone gets their own library and phone backup. You choose what’s shared.',
      action: 'Invite',
    },
    {
      id: 'copy',
      title: 'Keep a second copy',
      done: false, // a copy elsewhere can't be seen from here
      text: 'Photostead copies its database nightly and checks every original. Keep your photos somewhere else too: another disk, or a cloud backup.',
      action: 'Backups',
    },
  ];
  return steps.map((s) => ({ ...s, skipped: !s.done && skipped.has(s.id) }));
}

/** How many steps are left: neither done nor skipped. */
export const stepsLeft = (steps: SetupStep[]) => steps.filter((s) => !s.done && !s.skipped).length;

export function SetupCard({ steps, onAction, onSkip, onHide }: {
  steps: SetupStep[];
  onAction: (id: SetupStepId) => void;
  onSkip: (id: SetupStepId, skip: boolean) => void;
  onHide: () => void;
}) {
  const left = stepsLeft(steps);
  const finished = steps.length - left;
  const next = steps.find((s) => !s.done && !s.skipped);
  return (
    <section className="setup-card" aria-labelledby="setup-title" data-testid="setup">
      <div className="setup-head">
        <div>
          <span className="setup-kicker">Get started</span>
          <h2 id="setup-title">{left === 0 ? 'You’re all set' : 'Set up Photostead for your household'}</h2>
        </div>
        <span className="setup-count" aria-label={`${finished} of ${steps.length} steps done`}>
          {finished} of {steps.length}
        </span>
        <button type="button" className="setup-hide" onClick={onHide} aria-label="Put the checklist away" title="Put the checklist away (it stays in the sidebar)">
          <Icon.x />
        </button>
      </div>
      <div className="setup-bar" aria-hidden="true">
        <i style={{ width: `${(100 * finished) / steps.length}%` }} />
      </div>
      <ol className="setup-steps">
        {steps.map((s, i) => (
          <li key={s.id} className={s.done ? 'done' : s.skipped ? 'skipped' : s === next ? 'next' : undefined}>
            <span className="setup-mark" aria-hidden="true">{s.done || s.skipped ? <Icon.check /> : i + 1}</span>
            <div className="setup-text">
              <b>{s.title}</b>
              <span>{s.skipped ? (s.id === 'copy' ? 'Done elsewhere' : 'Skipped') : s.text}</span>
            </div>
            <div className="setup-acts">
              {s.skipped ? (
                <button type="button" className="link" onClick={() => onSkip(s.id, false)}>Undo</button>
              ) : !s.done && (
                <>
                  <button type="button" className="link" onClick={() => onSkip(s.id, true)}>{s.id === 'copy' ? 'I’ve done this' : 'Skip'}</button>
                  <button type="button" className={`pill${s === next ? ' primary' : ''}`} onClick={() => onAction(s.id)}>{s.action}</button>
                </>
              )}
            </div>
          </li>
        ))}
      </ol>
      {left === 0 && (
        <div className="setup-end">
          <button type="button" className="pill primary" onClick={onHide}>Done</button>
        </div>
      )}
    </section>
  );
}
