import { useCallback, useEffect, useState } from 'react';
import { api, thumbURL, type Stack, type TimelineAsset } from './api';
import { plural } from './Collections';
import { Icon } from './icons';
import { placeholder } from './thumbhash';

export const STACK_KINDS: Record<Stack['kind'], string> = {
  raw_jpeg: 'RAW + JPEG',
  burst: 'Burst',
  near_duplicate: 'Look-alikes',
  ai_batch: 'AI batch',
  manual: 'Stack',
};

function Thumb({ a, size, current, onClick }: { a: TimelineAsset; size: number; current?: boolean; onClick?: () => void }) {
  const ph = placeholder(a.id, a.thumbhash);
  return (
    <button type="button" className={`st-thumb${current ? ' cur' : ''}`} style={{ width: size, height: size, backgroundImage: ph ? `url(${ph})` : undefined }}
      onClick={onClick} aria-current={current} title={a.width && a.height ? `${a.width} × ${a.height}` : undefined}>
      {a.render === 'done' && <img src={thumbURL(a.id, a.thumbhash)} alt="" loading="lazy" decoding="async" onLoad={(e) => e.currentTarget.classList.add('ok')} />}
    </button>
  );
}

/**
 * The stack an item in the viewer is in: the rest of it to look at, and the owner's say over it
 * (put this one on top, take it out, unstack).
 */
export function StackPanel({ asset, onShow, onChanged }: {
  asset: TimelineAsset;
  onShow: (a: TimelineAsset) => void;
  onChanged: (text: string) => void;
}) {
  const [stack, setStack] = useState<Stack | null>(null);
  const load = useCallback(() => {
    api.stackOf(asset.id).then(setStack, () => setStack(null));
  }, [asset.id]);
  useEffect(load, [load]);
  if (!stack || stack.members.length < 2) return null;
  const top = stack.cover === asset.id;
  const act = (p: Promise<unknown>, text: string) =>
    p.then(() => {
      onChanged(text);
      load();
    }, () => {});
  return (
    <section className="i-stack">
      <h4>
        {STACK_KINDS[stack.kind]}
        {stack.state === 'suggested' ? ' · suggested' : ''} · {stack.members.length}
      </h4>
      <div className="st-strip">
        {stack.members.map((m) => (
          <Thumb key={m.id} a={m} size={56} current={m.id === asset.id} onClick={() => m.id !== asset.id && onShow(m)} />
        ))}
      </div>
      <div className="i-acts">
        {!top && (
          <button type="button" className="g-btn" onClick={() => void act(api.stackCover(stack.id, asset.id), 'Put on top of its stack')}>
            Put on top
          </button>
        )}
        <button type="button" className="g-btn" onClick={() => void act(api.removeFromStack(stack.id, [asset.id]), 'Took it out of the stack')}
          title="It shows on its own, and won't be stacked like this again">
          Take out
        </button>
        <button type="button" className="g-btn" onClick={() => void act(api.unstack(stack.id), 'Unstacked: each shows on its own')}>
          Unstack
        </button>
      </div>
    </section>
  );
}

/**
 * Look-alikes: groups of photos that look all but the same, waiting for a yes or no. Stack them
 * (the best on top), keep the best and trash the rest, or say they aren't look-alikes.
 */
export function LookAlikesPage({ onOpen, onFail, onChanged }: {
  onOpen: (list: TimelineAsset[], a: TimelineAsset) => void;
  onFail: (e: unknown) => void;
  onChanged: (text: string, undo?: () => Promise<unknown>) => void;
}) {
  const [groups, setGroups] = useState<Stack[] | null>(null);
  const [total, setTotal] = useState(0);
  const more = useCallback((from: number) => {
    api.lookAlikes(from).then((r) => {
      setTotal(r.total);
      setGroups((g) => (from === 0 ? r.stacks : [...(g ?? []), ...r.stacks]));
    }, onFail);
  }, [onFail]);
  useEffect(() => more(0), [more]);
  const [busy, setBusy] = useState(false);
  // Every group stacked, best on top, in one go. Nothing is trashed; Undo puts them all back here.
  const stackAll = () => {
    setBusy(true);
    api.confirmAllStacks().then((r) => {
      setGroups([]);
      setTotal(0);
      onChanged(`Stacked ${plural(r.changed, 'group', 'groups')}, the best of each on top`, () => api.unconfirmStacks(r.stackIds).then(() => more(0)));
    }, onFail).finally(() => setBusy(false));
  };
  const settle = (g: Stack, p: Promise<unknown>, text: string, undo?: () => Promise<unknown>) =>
    p.then(() => {
      setGroups((list) => (list ?? []).filter((x) => x.id !== g.id));
      setTotal((n) => n - 1);
      onChanged(text, undo);
    }, onFail);
  return (
    <div className="scroller page">
      <header className="results-head">
        <div>
          <p className="kicker">Utilities</p>
          <h1>Look-alikes</h1>
          <p className="muted">
            {groups === null ? ' ' : total ? `${plural(total, 'group', 'groups')} of photos that look all but the same, such as one picture saved twice` : 'Nothing waiting'}
          </p>
        </div>
        {total > 1 && (
          <div className="h-actions">
            <button type="button" className="pill" disabled={busy} onClick={stackAll}
              title="Stack every group, the best of each on top. Nothing goes to the trash, and you can undo it.">
              <Icon.layers />
              {busy ? 'Stacking…' : `Stack all ${total.toLocaleString()}`}
            </button>
          </div>
        )}
      </header>
      {groups?.length === 0 && (
        <div className="empty page-empty">
          <h1>No look-alikes</h1>
          <p>Photos that look all but the same show up here as they’re found, for you to stack or tidy up.</p>
        </div>
      )}
      <div className="la-list">
        {groups?.map((g) => {
          const top = g.members.find((m) => m.id === g.cover) ?? g.members[0];
          const rest = g.members.filter((m) => m.id !== top.id).map((m) => m.id);
          return (
            <section key={g.id} className="la-group">
              <div className="la-thumbs">
                {g.members.map((m) => (
                  <figure key={m.id}>
                    <Thumb a={m} size={150} current={m.id === top.id} onClick={() => onOpen(g.members, m)} />
                    <figcaption>{m.id === top.id ? 'best · ' : ''}{m.width && m.height ? `${m.width} × ${m.height}` : ''}</figcaption>
                  </figure>
                ))}
              </div>
              <div className="la-acts">
                <button type="button" className="pill" onClick={() => void settle(g, api.confirmStack(g.id), 'Stacked, the best on top', () => api.unstack(g.id))}>
                  <Icon.layers />
                  Stack them
                </button>
                <button type="button" className="pill" onClick={() => void settle(g, api.keepTopTrashRest(g.id), `Kept the best, moved ${plural(rest.length, 'other', 'others')} to the trash`, () => api.restore(rest))}>
                  <Icon.trash />
                  Keep the best, trash the rest
                </button>
                <button type="button" className="pill" onClick={() => void settle(g, api.unstack(g.id), 'Kept apart')}>
                  Not look-alikes
                </button>
              </div>
            </section>
          );
        })}
      </div>
      {groups && groups.length < total && (
        <div className="people-more">
          <button type="button" className="pill" onClick={() => more(groups.length)}>
            Show more
          </button>
        </div>
      )}
    </div>
  );
}
