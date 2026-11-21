import { useEffect, useRef, useState } from 'react';
import type { Bucket } from './api';
import { monthName } from './format';

type Props = {
  buckets: Bucket[]; // newest first
  visible: ReadonlySet<string>;
  theme: 'light' | 'dark'; // the one in effect (system resolved): redraw when the colors change
  onJump: (month: string) => void;
};

/**
 * The right rail: one bar per month (log scale, so a 30,000-item month doesn't flatten the
 * rest), year labels on the outer edge, the months on screen drawn darker. Hover for the month
 * and its count; click to go there.
 */
export function Scrubber({ buckets, visible, theme, onJump }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const [hover, setHover] = useState<{ y: number; i: number } | null>(null);

  useEffect(() => {
    const el = host.current!;
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const n = buckets.length;
  const step = n ? size.h / n : 0;

  useEffect(() => {
    const cv = canvas.current;
    if (cv && !n) cv.getContext('2d')?.clearRect(0, 0, cv.width, cv.height); // nothing dated to show (ranked results)
    if (!cv || !size.w || !size.h || !n) return;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    cv.width = Math.round(size.w * dpr);
    cv.height = Math.round(size.h * dpr);
    const ctx = cv.getContext('2d')!;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, size.w, size.h);
    const cs = getComputedStyle(document.documentElement);
    const ink = cs.getPropertyValue('--ink').trim() || '#000';
    const muted = cs.getPropertyValue('--muted').trim() || '#888';
    const max = Math.max(...buckets.map((b) => b.count), 1);
    const edge = size.w - 42; // bars grow inward from the year labels
    const room = edge - 8;
    // Thin bars even when there are only a few months (search results), so they read as a ruler.
    const thick = Math.min(4, Math.max(1, step * 0.64));
    const inset = Math.min(step * 0.18, 6);
    buckets.forEach((b, i) => {
      const on = visible.has(b.month);
      const w = 3 + (room - 3) * (Math.log1p(b.count) / Math.log1p(max));
      ctx.globalAlpha = on ? 1 : 0.42;
      ctx.fillStyle = on ? ink : muted;
      ctx.fillRect(edge - w, i * step + inset, w, thick);
    });
  }, [buckets, visible, size, n, step, theme]);

  // A label at each year's newest month, skipping any that would collide.
  const years: { year: string; y: number; month: string }[] = [];
  let lastY = -99;
  buckets.forEach((b, i) => {
    const y = i * step;
    if (i && buckets[i - 1].month.slice(0, 4) === b.month.slice(0, 4)) return;
    if (y - lastY < 15) return;
    years.push({ year: b.month.slice(0, 4), y, month: b.month });
    lastY = y;
  });

  const at = (clientY: number) => {
    const r = host.current!.getBoundingClientRect();
    const y = clientY - r.top;
    return { y, i: Math.max(0, Math.min(n - 1, Math.floor(y / step))) };
  };
  const tip = hover && buckets[hover.i];
  return (
    <div
      className="ruler"
      ref={host}
      style={{ ['--hy' as string]: `${hover?.y ?? 0}px` }}
      onMouseMove={(e) => n && setHover(at(e.clientY))}
      onMouseLeave={() => setHover(null)}
      onClick={(e) => n && onJump(buckets[at(e.clientY).i].month)}
      role="navigation"
      aria-label="Jump to a year or month"
    >
      <canvas ref={canvas} aria-hidden="true" />
      <div className="years">
        {years.map((y) => (
          <button key={y.year} type="button" style={{ top: y.y }} onClick={(e) => (e.stopPropagation(), onJump(y.month))}>
            {y.year}
          </button>
        ))}
      </div>
      {tip && (
        <div className="ruler-tip" style={{ top: hover.y }}>
          {monthName(Number(tip.month.slice(5)))} {tip.month.slice(0, 4)} · {tip.count.toLocaleString()}
        </div>
      )}
    </div>
  );
}
