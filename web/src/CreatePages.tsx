import { useEffect, useMemo, useState } from 'react';
import { api, thumbURL, type AIModel, type Prompt, type Sample } from './api';
import { formatSpan } from './format';
import { Copy } from './Generation';
import { Icon } from './icons';
import { placeholder } from './thumbhash';

const n = (x: number) => x.toLocaleString();
const month = (d?: string) => (d ? formatSpan(d, d) : '');

/** Up to four thumbnails in a row; empty slots keep the rows lined up. */
function Strip({ samples, big }: { samples: Sample[]; big?: boolean }) {
  return (
    <span className={`cr-strip${big ? ' big' : ''}`} aria-hidden="true">
      {[0, 1, 2, 3].map((i) => {
        const s = samples[i];
        if (!s) return <i key={i} />;
        const ph = placeholder(s.id, s.thumbhash);
        return (
          <img key={s.id} src={thumbURL(s.id)} alt="" loading="lazy" decoding="async" style={{ backgroundImage: ph ? `url(${ph})` : undefined }} />
        );
      })}
    </span>
  );
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

function Filter({ value, onChange, placeholder: ph }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <label className="cr-filter">
      <Icon.search />
      <span className="sr">Filter</span>
      <input type="search" value={value} onChange={(e) => onChange(e.target.value)} placeholder={ph} autoComplete="off" spellCheck={false} />
    </label>
  );
}

/** Every distinct prompt, with what it made: most used or most recent first, searchable. */
export function PromptsPage({ onOpen, onFail }: { onOpen: (id: string) => void; onFail: (e: unknown) => void }) {
  const [text, setText] = useState('');
  const q = useDebounced(text.trim(), 250);
  const [sort, setSort] = useState<'uses' | 'recent'>('uses');
  const [list, setList] = useState<Prompt[] | null>(null);
  const [total, setTotal] = useState(0);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => {
    let stale = false;
    setBusy(true);
    api
      .prompts({ q, sort, limit: 40 })
      .then((r) => {
        if (stale) return;
        setList(r.prompts);
        setTotal(r.total);
      }, onFail)
      .finally(() => !stale && setBusy(false));
    return () => {
      stale = true;
    };
  }, [q, sort, onFail]);

  const more = async () => {
    if (!list) return;
    setBusy(true);
    try {
      const r = await api.prompts({ q, sort, limit: 60, offset: list.length });
      setList([...list, ...r.prompts]);
      setTotal(r.total);
    } catch (e) {
      onFail(e);
    } finally {
      setBusy(false);
    }
  };

  const summary = !list
    ? ' '
    : q
      ? `${n(total)} ${total === 1 ? 'prompt matches' : 'prompts match'}`
      : `${n(total)} distinct prompts, ${sort === 'recent' ? 'most recent' : 'most used'} first`;
  return (
    <div className="scroller page">
      <header className="results-head cr-head">
        <div>
          <p className="kicker">Create</p>
          <h1>Prompt library</h1>
          <p className="muted">{summary}</p>
        </div>
        <div className="cr-tools">
          <Filter value={text} onChange={setText} placeholder="Search prompts" />
          <div className="cr-sort" role="group" aria-label="Sort prompts">
            <button type="button" aria-pressed={sort === 'uses'} onClick={() => setSort('uses')}>
              Most used
            </button>
            <button type="button" aria-pressed={sort === 'recent'} onClick={() => setSort('recent')}>
              Recent
            </button>
          </div>
        </div>
      </header>
      {list?.length === 0 && (
        <div className="empty page-empty">
          <h1>{q ? 'No prompts match' : 'No prompts yet'}</h1>
          <p>{q ? 'Try fewer words.' : 'Prompts come from the AI details saved in your files.'}</p>
        </div>
      )}
      <div className="cr-prompts">
        {list?.map((p) => {
          const expanded = open.has(p.id);
          const meta = [`${n(p.uses)} ${p.uses === 1 ? 'item' : 'items'}`, p.models.slice(0, 2).join(', '), p.loras.slice(0, 2).join(', '),
            p.lastDate && `last ${month(p.lastDate)}`].filter(Boolean);
          return (
            <article key={p.id} className={`cr-prompt${expanded ? ' open' : ''}`}>
              <button type="button" className="cr-strip-btn" onClick={() => onOpen(p.id)} aria-label={`Show the ${n(p.uses)} items from this prompt`}>
                <Strip samples={p.samples} big />
              </button>
              <div className="cr-pmain">
                <p
                  className="cr-ptext"
                  onClick={() => setOpen((s) => {
                    const next = new Set(s);
                    if (next.has(p.id)) next.delete(p.id);
                    else next.add(p.id);
                    return next;
                  })}
                  title={expanded ? 'Click to shorten' : 'Click to read it all'}
                >
                  {p.text}
                </p>
                <p className="cr-pmeta">{meta.join(' · ')}</p>
              </div>
              <div className="cr-pacts">
                <Copy text={p.text} label="Copy" />
                <button type="button" className="g-btn" onClick={() => onOpen(p.id)} title="Show what this prompt made">
                  <Icon.photos />
                  {n(p.uses)}
                </button>
              </div>
            </article>
          );
        })}
      </div>
      {list && list.length < total && (
        <div className="cr-more">
          <button type="button" className="pill" onClick={() => void more()} disabled={busy}>
            {busy ? 'Loading…' : `Show more (${n(total - list.length)} left)`}
          </button>
        </div>
      )}
    </div>
  );
}

/** Models and LoRAs side by side: how often each was used, with what, and when. */
export function ModelsPage({ onOpen, onFail }: { onOpen: (m: AIModel) => void; onFail: (e: unknown) => void }) {
  const [data, setData] = useState<{ models: AIModel[]; loras: AIModel[] } | null>(null);
  const [text, setText] = useState('');
  useEffect(() => {
    api.models().then(setData, onFail);
  }, [onFail]);
  const q = text.trim().toLowerCase();
  const total = data ? data.models.length + data.loras.length : 0;
  return (
    <div className="scroller page">
      <header className="results-head cr-head">
        <div>
          <p className="kicker">Create</p>
          <h1>Models &amp; LoRAs</h1>
          <p className="muted">{data ? `${n(data.models.length)} models and ${n(data.loras.length)} LoRAs` : ' '}</p>
        </div>
        <div className="cr-tools">
          <Filter value={text} onChange={setText} placeholder="Filter models and LoRAs" />
        </div>
      </header>
      {data && total === 0 && (
        <div className="empty page-empty">
          <h1>No models yet</h1>
          <p>Models and LoRAs come from the AI details saved in your files.</p>
        </div>
      )}
      {data && total > 0 && (
        <div className="cr-cols">
          <Column title="Models" list={data.models} q={q} onOpen={onOpen} withLabel="often with" />
          <Column title="LoRAs" list={data.loras} q={q} onOpen={onOpen} withLabel="mostly with" />
        </div>
      )}
    </div>
  );
}

function Column({ title, list, q, onOpen, withLabel }: { title: string; list: AIModel[]; q: string; onOpen: (m: AIModel) => void; withLabel: string }) {
  const shown = useMemo(() => (q ? list.filter((m) => m.name.toLowerCase().includes(q)) : list), [list, q]);
  const max = Math.log1p(list[0]?.uses ?? 1);
  return (
    <section className="cr-sec">
      <h3>
        {title} <span>{n(shown.length)}</span>
      </h3>
      {shown.length === 0 && <p className="muted small cr-none">Nothing matches.</p>}
      {shown.map((m) => (
        <button key={m.id} type="button" className="cr-row" onClick={() => onOpen(m)}>
          <Strip samples={m.samples} />
          <span className="cr-row-main">
            <b>{m.name}</b>
            <span>{[m.lastDate && `last ${month(m.lastDate)}`, m.with && `${withLabel} ${m.with}`].filter(Boolean).join(' · ')}</span>
          </span>
          <span className="cr-count">{n(m.uses)}</span>
          <span className="cr-bar" style={{ width: `${((Math.log1p(m.uses) / max) * 100).toFixed(1)}%` }} />
        </button>
      ))}
    </section>
  );
}
