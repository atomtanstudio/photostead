import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, uneditedURL, type PhotoEdit, type Recipe, type TimelineAsset } from './api';
import { Icon } from './icons';

/**
 * Editing a photo: turning, mirroring, cropping, and its light and color. It starts from the photo
 * as it is (an earlier edit is shown as it was set, to change or undo), and shows the edit live:
 * the turn and mirror drawn in a canvas, light and color with an SVG filter doing the same sums as
 * the server (out = a·in + b for each channel, then saturation). Saved, the server keeps the recipe
 * beside the original, which isn't changed, and makes the previews again.
 */

type Shape = 'free' | 'original' | [number, number];
const SHAPES: { label: string; shape: Shape }[] = [
  { label: 'Free', shape: 'free' },
  { label: 'Original', shape: 'original' },
  { label: 'Square', shape: [1, 1] },
  { label: '4:5', shape: [4, 5] },
  { label: '3:2', shape: [3, 2] },
  { label: '16:9', shape: [16, 9] },
];
type Rect = { x: number; y: number; w: number; h: number };
const WHOLE: Rect = { x: 0, y: 0, w: 1, h: 1 };
const MIN = 0.05;

const blank = (): Recipe => ({ rotate: 0 });
const round = (v: number | undefined, to = 1000) => (v ? Math.round(v * to) / to : undefined);
const whole = (c?: Rect) => !c || (c.x <= 0.0005 && c.y <= 0.0005 && c.w >= 0.9995 && c.h >= 0.9995);

/** What's sent: small numbers rounded, nothing that changes nothing. */
export function tidy(r: Recipe): Recipe {
  const out: Recipe = { rotate: r.rotate };
  if (r.flip) out.flip = true;
  if (!whole(r.crop)) {
    const c = r.crop!;
    out.crop = { x: round(c.x, 1e4) ?? 0, y: round(c.y, 1e4) ?? 0, w: round(c.w, 1e4) ?? 1, h: round(c.h, 1e4) ?? 1 };
  }
  for (const k of ['exposure', 'contrast', 'saturation', 'warmth'] as const) {
    const v = round(r[k], 100);
    if (v) out[k] = v;
  }
  return out;
}

/** A crop, kept on the same part of the picture when it's turned a quarter to the left. */
export const turnLeft = (c: Rect): Rect => ({ x: c.y, y: 1 - c.x - c.w, w: c.h, h: c.w });
export const mirror = (c: Rect): Rect => ({ ...c, x: 1 - c.x - c.w });

/** The largest crop of a shape (w:h in pixels) that fits, centred on what was kept before. */
export function fitShape(ratio: number, pw: number, ph: number, around: Rect): Rect {
  let w = 1;
  let h = (pw / ratio) / ph;
  if (h > 1) {
    h = 1;
    w = (ph * ratio) / pw;
  }
  const cx = around.x + around.w / 2;
  const cy = around.y + around.h / 2;
  return { x: Math.min(Math.max(cx - w / 2, 0), 1 - w), y: Math.min(Math.max(cy - h / 2, 0), 1 - h), w, h };
}

export function PhotoEditor({ asset, onClose, onSaved }: {
  asset: TimelineAsset;
  onClose: () => void;
  onSaved: (change: Partial<TimelineAsset>, edited: boolean) => void;
}) {
  const [start, setStart] = useState<PhotoEdit | null>(null);
  const [rec, setRec] = useState<Recipe>(blank);
  const [base, setBase] = useState<HTMLImageElement | null>(null);
  const [shape, setShape] = useState<Shape>('free');
  const [comparing, setComparing] = useState(false);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const canvas = useRef<HTMLCanvasElement>(null);
  const frame = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    api.photoEdit(asset.id).then((e) => {
      if (!live) return;
      setStart(e);
      setRec(e.recipe ? { ...e.recipe } : blank());
    }, (err: Error) => live && setError(err.message));
    const img = new Image();
    img.onload = () => live && setBase(img);
    img.onerror = () => live && setError('This photo couldn’t be opened for editing.');
    img.src = uneditedURL(asset.id);
    return () => {
      live = false;
    };
  }, [asset.id]);

  const sideways = rec.rotate === 90 || rec.rotate === 270;
  const pw = base ? (sideways ? base.naturalHeight : base.naturalWidth) : 0;
  const ph = base ? (sideways ? base.naturalWidth : base.naturalHeight) : 0;

  // The turned (and mirrored) picture, drawn once per turn: the crop and the look go on top.
  useEffect(() => {
    const c = canvas.current;
    if (!c || !base) return;
    c.width = pw;
    c.height = ph;
    const g = c.getContext('2d')!;
    g.save();
    g.translate(pw / 2, ph / 2);
    if (rec.flip) g.scale(-1, 1);
    g.rotate((rec.rotate * Math.PI) / 180);
    g.drawImage(base, -base.naturalWidth / 2, -base.naturalHeight / 2);
    g.restore();
  }, [base, rec.rotate, rec.flip, pw, ph]);

  const crop = rec.crop ?? WHOLE;
  const set = (change: Partial<Recipe>) => setRec((r) => ({ ...r, ...change }));
  // A quarter to the left as it's seen: mirrored, that's a quarter the other way before the mirror.
  const turn = () => setRec((r) => ({ ...r, rotate: ((r.rotate + (r.flip ? 90 : 270)) % 360) as Recipe['rotate'], crop: r.crop && turnLeft(r.crop) }));
  const flip = () => setRec((r) => ({ ...r, flip: !r.flip, crop: r.crop && mirror(r.crop) }));
  const pickShape = (s: Shape) => {
    setShape(s);
    if (s === 'free' || !pw) return;
    if (s === 'original') {
      set({ crop: undefined });
      return;
    }
    // Shapes follow the picture: a tall one gets 2:3 for 3:2.
    const [a, b] = s;
    const ratio = a === b ? 1 : (pw >= ph) === (a >= b) ? a / b : b / a;
    set({ crop: fitShape(ratio, pw, ph, crop) });
  };
  const ratio = useMemo((): number | null => {
    if (shape === 'free' || !pw) return null;
    if (shape === 'original') return pw / ph;
    const [a, b] = shape;
    return a === b ? 1 : (pw >= ph) === (a >= b) ? a / b : b / a;
  }, [shape, pw, ph]);

  // Dragging the crop: inside moves it, a corner resizes it (keeping its shape, if one is chosen).
  const drag = (e: React.PointerEvent, corner: '' | 'nw' | 'ne' | 'sw' | 'se') => {
    const box = frame.current?.getBoundingClientRect();
    if (!box) return;
    e.preventDefault();
    e.stopPropagation();
    (e.target as Element).setPointerCapture(e.pointerId);
    const from = { ...crop };
    const x0 = e.clientX;
    const y0 = e.clientY;
    if (corner && shape === 'original') setShape('free');
    const move = (ev: PointerEvent) => {
      const dx = (ev.clientX - x0) / box.width;
      const dy = (ev.clientY - y0) / box.height;
      if (!corner) {
        set({ crop: { ...from, x: Math.min(Math.max(from.x + dx, 0), 1 - from.w), y: Math.min(Math.max(from.y + dy, 0), 1 - from.h) } });
        return;
      }
      // The opposite corner stays put.
      const ax = corner.includes('w') ? from.x + from.w : from.x;
      const ay = corner.includes('n') ? from.y + from.h : from.y;
      let px = Math.min(Math.max((corner.includes('w') ? from.x : from.x + from.w) + dx, 0), 1);
      let py = Math.min(Math.max((corner.includes('n') ? from.y : from.y + from.h) + dy, 0), 1);
      let w = Math.max(Math.abs(px - ax), MIN);
      let h = Math.max(Math.abs(py - ay), MIN);
      if (ratio) {
        h = (w * pw) / ratio / ph;
        const room = corner.includes('n') ? ay : 1 - ay;
        if (h > room) {
          h = room;
          w = (h * ph * ratio) / pw;
        }
      }
      px = corner.includes('w') ? ax - w : ax;
      py = corner.includes('n') ? ay - h : ay;
      set({ crop: { x: Math.max(px, 0), y: Math.max(py, 0), w, h } });
    };
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  };

  const sent = useMemo(() => JSON.stringify(tidy(rec)), [rec]);
  const changed = start !== null && sent !== JSON.stringify(tidy(start.recipe ?? blank()));
  const hadEdit = Boolean(start?.recipe);

  const save = useCallback(async (undo: boolean) => {
    setBusy(undo ? 'Going back to the original…' : 'Saving…');
    setError('');
    try {
      const before = asset.thumbhash;
      const done = undo ? await api.undoPhotoEdit(asset.id) : await api.savePhotoEdit(asset.id, tidy(rec));
      // The previews are made again in the background: wait (a little) for them, so what shows
      // next is the edit.
      setBusy('Making the previews…');
      let change: Partial<TimelineAsset> = { width: done.width, height: done.height };
      for (let i = 0; i < 40; i++) {
        await new Promise((r) => setTimeout(r, 500));
        const a = await api.asset(asset.id).catch(() => null);
        if (a && a.thumbhash !== before && a.render === 'done') {
          change = { width: a.width, height: a.height, thumbhash: a.thumbhash };
          break;
        }
      }
      onSaved(change, Boolean(done.recipe));
    } catch (err) {
      setError((err as Error).message);
      setBusy('');
    }
  }, [asset.id, asset.thumbhash, rec, onSaved]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [onClose, busy]);

  // The look, as the server does it: per channel out = a·in + b, then saturation.
  const k = 1 + (rec.contrast ?? 0);
  const gain = 2 ** (rec.exposure ?? 0) * k;
  const off = (128 * (1 - k)) / 255;
  const warm = rec.warmth ?? 0;
  const look = comparing ? undefined : 'url(#pe-look)';

  return (
    <div className="editor" role="dialog" aria-modal="true" aria-label="Edit photo" data-testid="photo-editor">
      <svg width="0" height="0" style={{ position: 'absolute' }} aria-hidden="true">
        <filter id="pe-look" colorInterpolationFilters="sRGB">
          <feComponentTransfer>
            <feFuncR type="linear" slope={gain * (1 + 0.12 * warm)} intercept={off} />
            <feFuncG type="linear" slope={gain} intercept={off} />
            <feFuncB type="linear" slope={gain * (1 - 0.12 * warm)} intercept={off} />
          </feComponentTransfer>
          <feColorMatrix type="saturate" values={String(1 + (rec.saturation ?? 0))} />
        </filter>
      </svg>
      <div className="pe-stage">
        {base ? (
          <div className="pe-frame" ref={frame} style={{ aspectRatio: `${pw} / ${ph}`, ['--ar' as string]: pw / ph }}>
            <canvas ref={canvas} style={{ filter: look }} />
            {!comparing && (
              <div className="pe-crop" style={{ left: `${crop.x * 100}%`, top: `${crop.y * 100}%`, width: `${crop.w * 100}%`, height: `${crop.h * 100}%` }}
                onPointerDown={(e) => drag(e, '')} data-testid="crop-box">
                <i className="pe-third" />
                {(['nw', 'ne', 'sw', 'se'] as const).map((c) => (
                  <b key={c} className={`pe-handle ${c}`} onPointerDown={(e) => drag(e, c)} />
                ))}
              </div>
            )}
          </div>
        ) : (
          <p className="muted">{error || 'Opening…'}</p>
        )}
      </div>
      <aside className="pe-panel">
        <header>
          <h3>Edit</h3>
          <p className="muted small">The original stays as it is. You can undo this any time.</p>
        </header>
        <section>
          <h4>Crop and turn</h4>
          <div className="pe-row">
            <button type="button" className="pe-tool" onClick={turn} title="Turn left" data-testid="turn"><Icon.rotate /> Turn</button>
            <button type="button" className="pe-tool" onClick={flip} aria-pressed={Boolean(rec.flip)} title="Mirror"><Icon.flip /> Mirror</button>
          </div>
          <div className="pe-shapes" role="radiogroup" aria-label="Crop shape">
            {SHAPES.map((s) => (
              <button key={s.label} type="button" role="radio" aria-checked={JSON.stringify(shape) === JSON.stringify(s.shape)} onClick={() => pickShape(s.shape)}>
                {s.label}
              </button>
            ))}
          </div>
          {!whole(rec.crop) && <button type="button" className="link" onClick={() => { setShape('free'); set({ crop: undefined }); }}>Don’t crop</button>}
        </section>
        <section>
          <h4>Light</h4>
          <Slider label="Exposure" value={rec.exposure ?? 0} min={-2} max={2} step={0.05} show={(v) => `${v > 0 ? '+' : ''}${v.toFixed(2)}`} onChange={(v) => set({ exposure: v })} />
          <Slider label="Contrast" value={rec.contrast ?? 0} min={-1} max={1} step={0.01} onChange={(v) => set({ contrast: v })} />
        </section>
        <section>
          <h4>Color</h4>
          <Slider label="Saturation" value={rec.saturation ?? 0} min={-1} max={1} step={0.01} onChange={(v) => set({ saturation: v })} />
          <Slider label="Warmth" value={rec.warmth ?? 0} min={-1} max={1} step={0.01} onChange={(v) => set({ warmth: v })} />
        </section>
        <section className="pe-compare">
          <button type="button" className="g-btn" onPointerDown={() => setComparing(true)} onPointerUp={() => setComparing(false)} onPointerLeave={() => setComparing(false)}
            onKeyDown={(e) => e.key === ' ' && setComparing(true)} onKeyUp={() => setComparing(false)}>
            <Icon.eye /> Hold to see the original
          </button>
        </section>
        {error && base && <p className="r-warn">{error}</p>}
        <footer>
          {hadEdit && (
            <button type="button" className="link" disabled={Boolean(busy)} onClick={() => void save(true)} data-testid="undo-edit">Undo edit</button>
          )}
          <span className="pe-spacer" />
          <button type="button" className="g-btn" onClick={onClose} disabled={Boolean(busy)}>Cancel</button>
          <button type="button" className="g-btn primary" onClick={() => void save(false)} disabled={!changed || Boolean(busy) || !base} data-testid="save-edit">
            {busy || 'Save'}
          </button>
        </footer>
      </aside>
    </div>
  );
}

function Slider({ label, value, min, max, step, show, onChange }: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  show?: (v: number) => string;
  onChange: (v: number) => void;
}) {
  const text = show ? show(value) : `${value > 0 ? '+' : ''}${Math.round(value * 100)}`;
  return (
    <label className="pe-slider" onDoubleClick={() => onChange(0)} title="Double-click to reset">
      <span>{label}</span>
      <output className={value ? '' : 'muted'}>{text}</output>
      <input type="range" min={min} max={max} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} aria-label={label} />
    </label>
  );
}
