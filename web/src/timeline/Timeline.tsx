import { useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type Ref } from 'react';
import { originalURL, playURL, thumbURL, type TimelineAsset } from '../api';
import { previewTimes } from '../previewTimes';
import { Icon } from '../icons';
import { placeholder } from '../thumbhash';
import { formatDay, formatDuration, monthName } from '../format';
import { MONTH_HEAD, estimateMonth, layoutMonth, type MonthLayout } from './layout';
import type { TimelineModel } from './model';

export const ZOOMS = [120, 170, 230, 292, 370, 470];

export type TimelineHandle = {
  /** Scrolls so a month (YYYY-MM) starts at the top. */
  scrollToMonth: (month: string) => void;
};

type Props = {
  model: TimelineModel;
  zoom: number;
  header?: ReactNode; // shown above the first month (search results)
  selected: ReadonlySet<string>;
  onOpen: (asset: TimelineAsset, tile: HTMLElement) => void;
  onToggle: (asset: TimelineAsset, shift: boolean) => void;
  onSelectDay: (ids: string[]) => void;
  onVisibleMonths?: (months: ReadonlySet<string>) => void;
  organize?: boolean; // show stars, flags and color labels
  blurAdult?: boolean; // adult items blurred until opened (the viewer's own setting)
  ref?: Ref<TimelineHandle>;
};

/**
 * The library as one long scroll of months. Every month has a height from the start (estimated
 * until it loads), so the scrollbar and the scrubber span the whole library; only rows near the
 * screen are in the DOM, which keeps it smooth at any size.
 */
export function Timeline({ model, zoom, header, selected, onOpen, onToggle, onSelectDay, onVisibleMonths, organize, blurAdult, ref }: Props) {
  const scroller = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  const [viewport, setViewport] = useState({ top: 0, height: 800 });
  const [, setVersion] = useState(0);
  // Month layouts, per list: switching between the timeline, a filter and search results must
  // never reuse another list's layout for the same month.
  const layouts = useMemo(() => ({ current: new Map<string, MonthLayout>() }), [model]);
  const narrow = width > 0 && width < 600;
  const target = narrow ? Math.min(ZOOMS[zoom], 200) : ZOOMS[zoom];
  const selecting = selected.size > 0;
  const offsetTop = () => canvas.current?.offsetTop ?? 0;

  useLayoutEffect(() => {
    const el = scroller.current!;
    const measure = () => {
      const style = getComputedStyle(el);
      const w = el.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
      setWidth(Math.max(0, Math.floor(w)));
      setViewport({ top: el.scrollTop - offsetTop(), height: el.clientHeight });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => model.subscribe(() => setVersion((v) => v + 1)), [model]);

  const loadedCount = model.months.filter((m) => m.assets).length;
  const geometry = useMemo(() => {
    const tops: number[] = [];
    const heights: number[] = [];
    let y = 0;
    for (const m of model.months) {
      let h = estimateMonth(m.count, width || 1000, target);
      if (m.assets && width) {
        const key = `${m.month}:${width}:${target}`;
        let l = layouts.current.get(key);
        if (!l) {
          l = layoutMonth(m.assets, width, target, m.flat);
          layouts.current.set(key, l);
        }
        h = l.height;
      }
      tops.push(y);
      heights.push(h);
      y += h;
    }
    return { tops, heights, total: y };
  }, [model, width, target, loadedCount]);

  // Keep what's on screen in place when months above it change height (a month loading, a
  // resize, a zoom change): remember the anchor before, restore it after.
  const anchor = useRef<{ month: number; offset: number } | null>(null);
  const geometryRef = useRef(geometry);
  const captureAnchor = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const top = el.scrollTop - offsetTop();
    const { tops, heights } = geometryRef.current;
    let i = tops.findIndex((t, k) => top >= t && top < t + heights[k]);
    if (i < 0) i = 0;
    anchor.current = { month: i, offset: heights[i] ? (top - tops[i]) / heights[i] : 0 };
  }, []);
  useLayoutEffect(() => {
    const prev = geometryRef.current;
    geometryRef.current = geometry;
    const el = scroller.current;
    const a = anchor.current;
    if (!el || !a || prev === geometry || el.scrollTop < 1) return;
    const to = offsetTop() + geometry.tops[a.month] + a.offset * geometry.heights[a.month];
    if (Math.abs(to - el.scrollTop) > 1) el.scrollTop = to;
  }, [geometry]);

  const onScroll = useCallback(() => {
    const el = scroller.current!;
    captureAnchor();
    const top = el.scrollTop - offsetTop();
    setViewport((v) => (Math.abs(v.top - top) < 40 && v.height === el.clientHeight ? v : { top, height: el.clientHeight }));
  }, [captureAnchor]);

  useImperativeHandle(ref, () => ({
    scrollToMonth: (month: string) => {
      const i = model.months.findIndex((m) => m.month === month);
      const el = scroller.current;
      if (i < 0 || !el) return;
      el.scrollTop = offsetTop() + geometryRef.current.tops[i];
      anchor.current = { month: i, offset: 0 };
    },
  }), [model]);

  // Load months that are on screen or close to it.
  useEffect(() => {
    const lo = viewport.top - viewport.height;
    const hi = viewport.top + viewport.height * 2;
    geometry.tops.forEach((t, i) => {
      if (t + geometry.heights[i] >= lo && t <= hi) void model.load(i);
    });
  }, [viewport, geometry, model]);

  // Tell the scrubber which months are on screen.
  const lastVisible = useRef('');
  useEffect(() => {
    if (!onVisibleMonths) return;
    const on = new Set<string>();
    geometry.tops.forEach((t, i) => {
      if (t + geometry.heights[i] > viewport.top && t < viewport.top + viewport.height) on.add(model.months[i].month);
    });
    const key = [...on].join();
    if (key !== lastVisible.current) {
      lastVisible.current = key;
      onVisibleMonths(on);
    }
  }, [viewport, geometry, model, onVisibleMonths]);

  const lo = viewport.top - viewport.height * 0.75;
  const hi = viewport.top + viewport.height * 1.75;
  const nodes: ReactNode[] = [];
  model.months.forEach((m, i) => {
    const top = geometry.tops[i];
    const height = geometry.heights[i];
    if (top + height < lo || top > hi) return;
    const [y, mo] = m.month.split('-').map(Number);
    nodes.push(
      <header key={`h${m.month}`} className="month" style={{ top }}>
        <h2>{m.label ?? monthName(mo)}</h2>
        <span>
          {m.label ? '' : `${y} · `}
          {m.count.toLocaleString()} {m.count === 1 ? 'item' : 'items'}
        </span>
      </header>,
    );
    const layout = m.assets && width ? layouts.current.get(`${m.month}:${width}:${target}`) : undefined;
    if (!layout) {
      nodes.push(
        <div key={`p${m.month}`} className={`month-skeleton${m.failed ? ' failed' : ''}`} style={{ top: top + MONTH_HEAD, height: height - MONTH_HEAD }}>
          {m.failed && (
            <button type="button" onClick={() => void model.load(i)}>
              Couldn’t load {monthName(mo)} {y}. Try again
            </button>
          )}
        </div>,
      );
      return;
    }
    for (const day of layout.days) {
      const dayTop = top + day.y;
      const last = day.rows[day.rows.length - 1];
      if (dayTop + (last ? last.y - day.y + last.h : 0) < lo || dayTop > hi) continue;
      const ids = day.rows.flatMap((r) => r.cells.map((c) => c.asset.id));
      const allOn = ids.every((id) => selected.has(id));
      if (day.key) nodes.push(
        <div key={`d${day.key}`} className="day-head" style={{ top: dayTop }}>
          <button
            type="button"
            className={`day-select${allOn ? ' on' : ''}`}
            onClick={() => onSelectDay(ids)}
            aria-pressed={allOn}
            aria-label={`${allOn ? 'Deselect' : 'Select'} everything from ${formatDay(day.key)}`}
          >
            <Icon.check />
          </button>
          <b>{formatDay(day.key)}</b>
          <span className="n">{day.count.toLocaleString()}</span>
        </div>,
      );
      for (const row of day.rows) {
        const rowTop = top + row.y;
        if (rowTop + row.h < lo || rowTop > hi) continue;
        for (const c of row.cells) {
          nodes.push(
            <Tile key={c.asset.id} asset={c.asset} x={c.x} y={rowTop} w={c.w} h={row.h} selected={selected.has(c.asset.id)}
              selecting={selecting} onOpen={onOpen} onToggle={onToggle} organize={organize} blur={blurAdult && c.asset.adult} />,
          );
        }
      }
    }
  });

  return (
    <div className={`scroller${selecting ? ' selecting' : ''}`} ref={scroller} onScroll={onScroll} tabIndex={-1}>
      {header}
      <div className="canvas" ref={canvas} style={{ height: geometry.total }}>
        {nodes}
      </div>
    </div>
  );
}

type TileProps = {
  asset: TimelineAsset;
  x: number;
  y: number;
  w: number;
  h: number;
  selected: boolean;
  selecting: boolean;
  onOpen: Props['onOpen'];
  onToggle: Props['onToggle'];
  organize?: boolean;
  blur?: boolean; // adult, for a viewer who blurs it
};

function Tile({ asset, x, y, w, h, selected, selecting, onOpen, onToggle, organize, blur }: TileProps) {
  const ph = placeholder(asset.id, asset.thumbhash);
  // A video plays, muted, while the pointer rests on it (after a moment, so sweeping across the
  // grid doesn't start them all). Clicking opens it with sound, from where the preview got to.
  const [play, setPlay] = useState(false);
  const wait = useRef(0);
  const playable = asset.type === 'video' && asset.render === 'done' && !blur; // a blurred video doesn't play on hover
  useEffect(() => () => window.clearTimeout(wait.current), []);
  const hover = playable
    ? {
        onMouseEnter: () => {
          window.clearTimeout(wait.current);
          wait.current = window.setTimeout(() => setPlay(true), 220);
        },
        onMouseLeave: () => {
          window.clearTimeout(wait.current);
          setPlay(false);
        },
      }
    : {};
  const label = `${asset.type === 'video' ? 'Video' : 'Photo'} from ${formatDay(asset.localDate)}`;
  return (
    <div
      className={`tile${selected ? ' sel' : ''}${organize && asset.flag === 'reject' ? ' rejected' : ''}${blur ? ' veiled' : ''}`}
      style={{ transform: `translate(${x}px, ${y}px)`, width: w, height: h, backgroundImage: ph ? `url(${ph})` : undefined }}
      data-id={asset.id}
      {...hover}
    >
      <button
        type="button"
        className="t-open"
        aria-label={label}
        onClick={(e) => {
          if (selecting || e.shiftKey || e.metaKey || e.ctrlKey) return onToggle(asset, e.shiftKey);
          window.clearTimeout(wait.current);
          setPlay(false); // the viewer takes over, with sound
          onOpen(asset, e.currentTarget);
        }}
      >
        {asset.render === 'done' && (
          <img src={thumbURL(asset.id, asset.thumbhash)} alt="" loading="lazy" decoding="async" onLoad={(e) => e.currentTarget.classList.add('ok')} />
        )}
        {play && (
          <video src={asset.copy ? playURL(asset.id) : originalURL(asset.id)} muted autoPlay loop playsInline preload="auto" aria-hidden="true"
            onLoadedMetadata={(e) => { e.currentTarget.currentTime = previewTimes.get(asset.id) ?? 0; }}
            onPlaying={(e) => e.currentTarget.classList.add('ok')}
            onTimeUpdate={(e) => previewTimes.set(asset.id, e.currentTarget.currentTime)} />
        )}
      </button>
      <span className="t-shade" />
      {blur && (
        <span className="t-veil" title="Adult content, blurred (Adult content in the sidebar changes this)">
          <Icon.eyeOff />
        </span>
      )}
      <button
        type="button"
        className="t-check"
        aria-pressed={selected}
        aria-label={`${selected ? 'Deselect' : 'Select'} this ${asset.type === 'video' ? 'video' : 'photo'}`}
        onClick={(e) => onToggle(asset, e.shiftKey)}
      >
        <Icon.check />
      </button>
      {asset.favorite && (
        <span className="t-fav" title="Favorite">
          <Icon.heart />
        </span>
      )}
      {organize && (asset.rating || asset.flag) && (
        <span className="t-marks" title={[asset.rating && `${asset.rating} ${asset.rating === 1 ? 'star' : 'stars'}`, asset.flag === 'pick' ? 'Pick' : asset.flag === 'reject' ? 'Rejected' : ''].filter(Boolean).join(' · ')}>
          {asset.rating ? <span className="t-stars">{'★'.repeat(asset.rating)}</span> : null}
          {asset.flag === 'pick' && <Icon.flag />}
          {asset.flag === 'reject' && <Icon.x />}
        </span>
      )}
      {organize && asset.label && <span className={`t-label ${asset.label}`} />}
      {(asset.motionId || (asset.stackSize ?? 0) > 1 || asset.type === 'video' || asset.ai || asset.shared) && (
        <span className="badges">
          {asset.shared && (
            <span className="badge" title={asset.fromOthers ? 'Shared with the Household by another member' : 'Shared with the Household'}>
              <Icon.household />
            </span>
          )}
          {asset.ai && (
            <span className="badge ai" title={asset.ai === 'edited' ? 'Edited with AI (evidence in the file)' : 'Made with AI (evidence in the file)'}>
              <Icon.spark />
              AI
            </span>
          )}
          {asset.motionId && <span className="badge">LIVE</span>}
          {(asset.stackSize ?? 0) > 1 && (
            <span className="badge" title={`${asset.stackSize} versions`}>
              <Icon.layers />
              {asset.stackSize}
            </span>
          )}
          {asset.type === 'video' && (
            <span className="badge">
              <Icon.play />
              {formatDuration(asset.durationMs)}
            </span>
          )}
        </span>
      )}
    </div>
  );
}
