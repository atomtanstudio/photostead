import { useCallback, useEffect, useRef, useState } from 'react';
import { api, editedURL, originalURL, playURL, playsOriginal, previewURL, type AssetDetail, type MarkChange, type TimelineAsset } from './api';
import { dateSourceLabel, formatBytes, formatDay, formatTime } from './format';
import { Copy, GenerationPanel } from './Generation';
import { LABELS, labelInfo } from './utility';
import { Icon } from './icons';
import { MarkControls, applyMarks, describeChange, markKey } from './marks';
import { previewTimes } from './previewTimes';
import { StackPanel } from './Stacks';
import { placeholder } from './thumbhash';
import type { TimelineModel } from './timeline/model';
import { ItemTalk } from './Talk';
import { PhotoEditor } from './PhotoEditor';

type Props = {
  model: TimelineModel;
  zone: string;
  asset: TimelineAsset;
  onNavigate: (a: TimelineAsset) => void;
  onClose: () => void;
  onSimilar?: (a: TimelineAsset) => void; // "More like this"
  onTrash: (a: TimelineAsset) => Promise<void>;
  onRestore: (a: TimelineAsset) => Promise<void>;
  onFavorite: (a: TimelineAsset, favorite: boolean) => Promise<void>;
  organize?: boolean; // stars, flags and labels are on
  onMark: (a: TimelineAsset, change: MarkChange) => Promise<void>;
  onEdit?: (a: TimelineAsset, detail: AssetDetail) => void; // keywords, description, date
  version?: number; // changes when the item was edited elsewhere: its details are read again
  album?: string; // a shared album it's open in: its comments, and picking it
  onStackChanged?: (text: string) => void;
  onPhotoEdited?: (a: TimelineAsset, change: Partial<TimelineAsset>) => void; // turned, cropped, light and color
  blurAdult?: boolean; // adult items blurred until shown (the viewer's own setting)
};

// Adult items shown on purpose while blurring: until the page is reloaded.
const revealed = new Set<string>();

/** Full-screen viewer: the preview (or the video itself), arrow keys to move, i for details. */
export function Viewer({ model, zone, asset, onNavigate, onClose, onSimilar, onTrash, onRestore, onFavorite, organize, onMark, onEdit, version, onStackChanged, album, onPhotoEdited, blurAdult }: Props) {
  const [hud, setHud] = useState<{ text: string; n: number } | null>(null);
  const [info, setInfo] = useState(() => localStorage.getItem('ps.info') !== 'off');
  const [detail, setDetail] = useState<AssetDetail | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [live, setLive] = useState(false);
  const [editing, setEditing] = useState(false);
  const [, setShown] = useState(0); // re-render once an item is shown
  const [failedOriginal, setFailedOriginal] = useState<string | null>(null); // an original this browser couldn't play
  const [trim, setTrim] = useState<{ start: number; end: number } | null>(null); // seconds
  // While showing the image a clip was made from: the clip, to go back to. The image may not be
  // in the timeline, so it's shown on its own, and closing still returns to the clip's place.
  const [via, setVia] = useState<TimelineAsset | null>(null);
  const dialog = useRef<HTMLDivElement>(null);
  const player = useRef<HTMLVideoElement>(null);
  const detour = via !== null && via.id !== asset.id;
  const alone = detour && !model.find(asset.id); // no neighbours to arrow to

  const go = useCallback(
    async (dir: 1 | -1) => {
      const next = await model.neighbor(asset.id, dir);
      if (next) {
        setVia(null);
        onNavigate(next);
      }
    },
    [model, asset.id, onNavigate],
  );
  const back = useCallback(() => {
    if (!via) return;
    setVia(null);
    onNavigate(via);
  }, [via, onNavigate]);
  const showFrom = useCallback(
    (id: string) => {
      api.asset(id).then((a) => {
        setVia(asset);
        onNavigate(a);
      }, () => {});
    },
    [asset, onNavigate],
  );

  // Trashing (or, in the trash, restoring) moves on to the next item, or closes at the end; from
  // a clip's start image, it goes back to the clip.
  const away = useCallback(
    async (act: (a: TimelineAsset) => Promise<void>) => {
      if (detour) {
        await act(asset);
        back();
        return;
      }
      const next = (await model.neighbor(asset.id, 1)) ?? (await model.neighbor(asset.id, -1));
      await act(asset);
      if (next) onNavigate(next);
      else onClose();
    },
    [model, asset, detour, back, onNavigate, onClose],
  );
  const toggleFavorite = useCallback(() => {
    if (!detail || detail.trashed) return;
    const f = !detail.favorite;
    setDetail((d) => (d && d.id === asset.id ? { ...d, favorite: f } : d));
    void onFavorite(asset, f);
  }, [detail, asset, onFavorite]);
  const mark = useCallback((ch: MarkChange) => {
    if (!detail || detail.trashed) return;
    setDetail((d) => (d && d.id === asset.id ? { ...d, ...applyMarks(d, ch) } : d));
    setHud((h) => ({ text: describeChange(ch), n: (h?.n ?? 0) + 1 }));
    void onMark(asset, ch);
  }, [detail, asset, onMark]);

  // Your own photos, once there's a preview, can be edited (turned, cropped, light and color).
  const canAdjust = Boolean(onPhotoEdited && detail && detail.id === asset.id && !detail.trashed && !detail.owner && asset.type === 'image' && asset.render === 'done');

  // A moment of your own video, kept as a photo (filed in your photostead share).
  const canKeepFrame = Boolean(detail && detail.id === asset.id && !detail.trashed && !detail.owner && asset.type === 'video');
  const keepFrame = useCallback(async () => {
    const v = player.current;
    if (!v) return;
    v.pause();
    const say = (text: string) => setHud((h) => ({ text, n: (h?.n ?? 0) + 1 }));
    try {
      const r = await api.saveFrame(asset.id, Math.round(v.currentTime * 1000));
      say(r.status === 'duplicate' ? 'That frame is already a photo' : 'Kept as a photo');
    } catch (err) {
      say((err as Error).message);
    }
  }, [asset.id]);

  const saveTrim = useCallback(async () => {
    if (!trim) return;
    const say = (text: string) => setHud((h) => ({ text, n: (h?.n ?? 0) + 1 }));
    try {
      const r = await api.saveTrim(asset.id, Math.round(trim.start * 1000), Math.round(trim.end * 1000));
      say(r.status === 'duplicate' ? 'That part is already a video' : 'Saved as a new video');
      setTrim(null);
    } catch (err) {
      say((err as Error).message);
    }
  }, [asset.id, trim]);

  // Full size: the edited photo when it's been edited (what's shown), otherwise the original file.
  const downloadHref = !detail || detail.id !== asset.id ? null : detail.edited ? editedURL(asset.id) : `${originalURL(asset.id)}?download=1`;
  const download = useCallback(() => {
    if (!downloadHref) return;
    const a = document.createElement('a');
    a.href = downloadHref;
    a.download = '';
    a.click();
  }, [downloadHref]);

  useEffect(() => {
    setLoaded(false);
    setLive(false);
    setEditing(false);
    setTrim(null);
    setDetail(null);
    const ctrl = new AbortController();
    api.asset(asset.id, ctrl.signal).then(setDetail).catch(() => {});
    // Warm the neighbours so arrowing through is instant.
    for (const dir of [1, -1] as const) {
      void model.neighbor(asset.id, dir).then((n) => {
        if (n && n.type === 'image' && n.render === 'done') new Image().src = previewURL(n.id, n.thumbhash);
      });
    }
    return () => ctrl.abort();
  }, [asset.id, model]);
  useEffect(() => {
    if (!version) return;
    const ctrl = new AbortController();
    api.asset(asset.id, ctrl.signal).then(setDetail).catch(() => {});
    return () => ctrl.abort();
  }, [version, asset.id]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || editing) return;
      if (e.key === 'Escape') onClose();
      else if (e.key === 'ArrowRight') void go(1);
      else if (e.key === 'ArrowLeft') void go(-1);
      else if (e.key === 'Backspace' && detour) back();
      else if ((e.key === 'Delete' || e.key === 'Backspace') && detail && !detail.trashed) void away(onTrash);
      else if (e.key === 'f') toggleFavorite();
      else if (e.key === 'e' && canAdjust) setEditing(true);
      else if (e.key === 'd' && downloadHref) download();
      else if (organize && detail && markKey(e.key, detail)) mark(markKey(e.key, detail)!);
      else if (e.key === 'i') {
        setInfo((v) => {
          localStorage.setItem('ps.info', v ? 'off' : 'on');
          return !v;
        });
      } else return;
      e.preventDefault();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [go, back, detour, onClose, away, onTrash, detail, toggleFavorite, organize, mark, editing, canAdjust, downloadHref, download]);

  useEffect(() => dialog.current?.focus(), []);

  const ph = placeholder(asset.id, asset.thumbhash);
  const ratio = asset.width && asset.height ? asset.width / asset.height : 0;
  const adult = detail && detail.id === asset.id ? Boolean(detail.adult) : Boolean(asset.adult);
  const veiled = Boolean(blurAdult) && adult && !revealed.has(asset.id);
  // A video with a copy: the original when this browser plays it, else the copy. Until its details
  // say which, nothing starts (rather than start one and switch).
  const known = detail && detail.id === asset.id ? detail : null;
  const copy = Boolean(known?.video?.copy) && (failedOriginal === asset.id || !playsOriginal(known?.video));
  const videoSrc = asset.copy && !known ? null : copy ? playURL(asset.id) : originalURL(asset.id);
  return (
    <div className={`viewer${info ? ' with-info' : ''}`} role="dialog" aria-modal="true" aria-label="Viewer" tabIndex={-1} ref={dialog}>
      <div className="stage">
        <div className={`${ratio ? 'media fit' : 'media'}${veiled ? ' veiled' : ''}`} style={{ backgroundImage: ph && !loaded ? `url(${ph})` : undefined, ['--ar' as string]: ratio || undefined }}>
          {live && asset.motionId ? (
            <video key={asset.motionId} src={originalURL(asset.motionId)} autoPlay muted playsInline onEnded={() => setLive(false)} />
          ) : asset.type === 'video' ? (
            videoSrc && <video key={`${asset.id}:${copy}`} ref={player} src={videoSrc} poster={asset.render === 'done' ? previewURL(asset.id, asset.thumbhash) : undefined} controls={!veiled} autoPlay={!veiled} playsInline
              onLoadedMetadata={(e) => { // carry on from the muted hover preview, with sound
                const at = previewTimes.get(asset.id);
                previewTimes.delete(asset.id);
                if (at && at < e.currentTarget.duration - 1) e.currentTarget.currentTime = at;
              }}
              onLoadedData={() => setLoaded(true)}
              onError={() => { if (!copy && known?.video?.copy) setFailedOriginal(asset.id); }} />
          ) : asset.render === 'done' ? (
            <img key={asset.id} src={previewURL(asset.id, asset.thumbhash)} alt={detail?.fileName ?? ''} onLoad={() => setLoaded(true)} className={loaded ? 'ok' : ''} />
          ) : (
            <p className="unrendered">{asset.render === 'failed' ? 'This file couldn’t be previewed.' : 'Preview not made yet.'}</p>
          )}
        </div>
        {trim && (
          <div className="v-trimbar" data-testid="trim-bar">
            <span>From <b className="mono">{clock(trim.start)}</b></span>
            <button type="button" className="g-btn" onClick={() => player.current && setTrim({ ...trim, start: Math.min(player.current.currentTime, trim.end - 0.5) })}>Start here</button>
            <span>to <b className="mono">{clock(trim.end)}</b></span>
            <button type="button" className="g-btn" onClick={() => player.current && setTrim({ ...trim, end: Math.max(player.current.currentTime, trim.start + 0.5) })}>End here</button>
            <button type="button" className="g-btn" onClick={() => {
              const v = player.current;
              if (!v) return;
              v.currentTime = trim.start;
              void v.play();
            }}>Play it</button>
            <button type="button" className="g-btn primary" onClick={() => void saveTrim()} data-testid="save-trim">Save as a new video</button>
            <button type="button" className="g-btn" onClick={() => setTrim(null)}>Cancel</button>
          </div>
        )}
        {veiled && (
          <button type="button" className="v-veil" data-testid="show-adult" onClick={() => {
            revealed.add(asset.id);
            setShown((n) => n + 1);
            if (asset.type === 'video') void player.current?.play();
          }}>
            <Icon.eyeOff />
            <b>Adult content</b>
            <span>Show</span>
          </button>
        )}
        {hud && <div key={hud.n} className="v-hud" aria-live="polite">{hud.text}</div>}
        {album && <ItemTalk albumId={album} assetId={asset.id} dark />}
        <button type="button" className="v-btn v-close" onClick={onClose} aria-label="Close (Esc)">
          <svg viewBox="0 0 24 24"><path d="m6.5 6.5 11 11m0-11-11 11" /></svg>
        </button>
        {detour && (
          <button type="button" className="v-btn v-back" onClick={back} title="Back to the clip (⌫)">
            <Icon.back />
            Back to the clip
          </button>
        )}
        {asset.motionId && (
          <button type="button" className="v-btn v-live" onClick={() => setLive((v) => !v)} aria-pressed={live} aria-label="Play the Live Photo">
            LIVE
          </button>
        )}
        {detail?.trashed ? (
          <button type="button" className="v-btn v-trash" onClick={() => void away(onRestore)} aria-label="Restore" title="Restore">
            <Icon.restore />
          </button>
        ) : (
          <>
            <button type="button" className="v-btn v-fav" onClick={toggleFavorite} aria-pressed={Boolean(detail?.favorite)} disabled={!detail}
              aria-label={detail?.favorite ? 'Unfavorite (F)' : 'Favorite (F)'} title={detail?.favorite ? 'Unfavorite (F)' : 'Favorite (F)'}>
              {detail?.favorite ? <Icon.heartFill /> : <Icon.heart />}
            </button>
            <button type="button" className="v-btn v-trash" onClick={() => void away(onTrash)} disabled={!detail} aria-label="Move to the trash (Delete)" title="Move to the trash (Delete)">
              <Icon.trash />
            </button>
            {canKeepFrame && (
              <button type="button" className="v-btn v-trim" onClick={() => {
                const v = player.current;
                if (v) setTrim(trim ? null : { start: v.currentTime, end: v.duration || v.currentTime + 1 });
              }} aria-pressed={trim !== null} aria-label="Trim (save a part as a new video)" title="Trim: save a part as a new video" data-testid="trim">
                <Icon.film />
              </button>
            )}
            {canKeepFrame && (
              <button type="button" className="v-btn v-adjust" onClick={() => void keepFrame()} aria-label="Keep this frame as a photo" title="Keep this frame as a photo" data-testid="keep-frame">
                <Icon.camera />
              </button>
            )}
            {canAdjust && (
              <button type="button" className="v-btn v-adjust" onClick={() => setEditing(true)} aria-label="Edit the photo (E)" title="Edit: crop, turn, light and color (E)" data-testid="adjust">
                <Icon.adjust />
              </button>
            )}
          </>
        )}
        {downloadHref && (
          <a className="v-btn" href={downloadHref} download style={{ top: 12, right: 12 + 50 * (3 + (detail?.trashed ? 0 : (canAdjust ? 1 : 0) + (canKeepFrame ? 2 : 0))) }}
            aria-label="Download, full size (D)" title={detail?.edited ? 'Download the edited photo, full size (D)' : 'Download, full size (D)'} data-testid="download">
            <Icon.download />
          </a>
        )}
        <button type="button" className="v-btn v-info" onClick={() => setInfo((v) => !v)} aria-pressed={info} aria-label="Details (i)">
          <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5" /><path d="M12 11v5.5M12 7.6v.1" /></svg>
        </button>
        {!alone && (
          <>
            <button type="button" className="v-btn v-prev" onClick={() => void go(-1)} aria-label="Newer (←)">
              <svg viewBox="0 0 24 24"><path d="m14.5 6-6 6 6 6" /></svg>
            </button>
            <button type="button" className="v-btn v-next" onClick={() => void go(1)} aria-label="Older (→)">
              <svg viewBox="0 0 24 24"><path d="m9.5 6 6 6-6 6" /></svg>
            </button>
          </>
        )}
      </div>
      {info && (
        <InfoPanel asset={asset} detail={detail} zone={zone} onLeave={onClose} onSimilar={onSimilar} onFrom={showFrom} onMark={organize ? mark : undefined}
          onEdit={onEdit && detail && !detail.trashed ? () => onEdit(asset, detail) : undefined}
          stack={detail && !detail.trashed ? <StackPanel key={`${asset.id}:${version ?? 0}`} asset={asset} onShow={(m) => showFrom(m.id)} onChanged={(t) => onStackChanged?.(t)} /> : null}
          onUnlabel={(label) =>
            api.removeLabel(label, [asset.id]).then(() => {
              setDetail((d) => d && d.id === asset.id ? { ...d, labels: d.labels.filter((x) => x !== label), adult: label === 'adult' ? false : d.adult } : d);
              if (label === 'adult') model.patch([asset.id], { adult: false });
            }, () => {})}
          onLabel={(label) =>
            api.addLabel(label, [asset.id]).then(() => {
              // Adult takes the place of suggestive.
              setDetail((d) => d && d.id === asset.id ? { ...d, labels: [...d.labels.filter((x) => !(label === 'adult' && x === 'suggestive')), label].sort(),
                adult: label === 'adult' ? true : d.adult } : d);
              if (label === 'adult') {
                revealed.add(asset.id); // it was being looked at: don't blur it now
                model.patch([asset.id], { adult: true });
              }
            }, () => {})}
        />
      )}
      {editing && (
        <PhotoEditor asset={asset} onClose={() => setEditing(false)}
          onSaved={(change, edited) => {
            setEditing(false);
            setLoaded(false);
            setDetail((d) => (d && d.id === asset.id ? { ...d, ...change, edited } : d));
            onPhotoEdited?.(asset, change);
            setHud((h) => ({ text: edited ? 'Edited' : 'Back to the original', n: (h?.n ?? 0) + 1 }));
          }} />
      )}
    </div>
  );
}

const clock = (s: number) => `${Math.floor(s / 60)}:${(s % 60).toFixed(1).padStart(4, '0')}`;

function InfoPanel({ asset, detail, zone, onLeave, onSimilar, onUnlabel, onLabel, onFrom, onMark, onEdit, stack }: {
  asset: TimelineAsset;
  stack?: React.ReactNode;
  onMark?: (ch: MarkChange) => void;
  onEdit?: () => void;
  detail: AssetDetail | null;
  zone: string;
  onLeave: () => void;
  onFrom: (id: string) => void;
  onUnlabel: (label: string) => void;
  onLabel: (label: string) => void;
  onSimilar?: (a: TimelineAsset) => void;
}) {
  const cam = detail?.camera;
  const exposure = cam && [
    cam.fNumber && `ƒ/${cam.fNumber}`,
    cam.exposureS && (cam.exposureS < 1 ? `1/${Math.round(1 / cam.exposureS)} s` : `${cam.exposureS} s`),
    cam.focalMm && `${Math.round(cam.focalMm)} mm`,
    cam.iso && `ISO ${cam.iso}`,
  ].filter(Boolean).join(' · ');
  return (
    <aside className="info" aria-label="Details">
      <section>
        <h3>{formatDay((detail ?? asset).localDate)}</h3>
        <p className="mono">{detail ? formatTime(detail.takenAt, detail.offsetMin, zone) : '\u00a0'}</p>
        {detail && <p className="muted small">{dateSourceLabel(detail.dateSource)}</p>}
        <div className="i-acts">
          {onSimilar && (
            <button type="button" className="g-btn i-similar" onClick={() => onSimilar(asset)} title="Find items that look like this one">
              <Icon.spark />
              More like this
            </button>
          )}
          {onEdit && (
            <button type="button" className="g-btn i-similar" onClick={onEdit} title="Keywords, description and date">
              <Icon.pencil />
              Edit
            </button>
          )}
        </div>
      </section>
      {stack}
      {detail && onMark && !detail.trashed && (
        <section className="i-marks">
          <MarkControls marks={detail} onChange={onMark} />
        </section>
      )}
      {detail && (
        <>
          {(detail.favorite || detail.archived || detail.albums.length > 0 || detail.people.length > 0 || detail.characters.length > 0 || detail.labels.length > 0 || (detail.keywords?.length ?? 0) > 0) && (
            <section className="i-tags">
              {detail.favorite && (
                <span className="i-tag">
                  <Icon.heart /> Favorite
                </span>
              )}
              {detail.archived && (
                <span className="i-tag">
                  <Icon.archive /> Archived
                </span>
              )}
              {detail.labels.map((name) => {
                const l = labelInfo(name);
                if (!l) return null;
                const I = l.icon;
                return (
                  <span key={name} className="i-tag i-label">
                    <a href={`#/${l.hash}`} onClick={onLeave} title={`Open ${l.title}`}>
                      <I /> {l.tag}
                    </a>
                    <button type="button" onClick={() => onUnlabel(name)} aria-label={`Not ${l.one}`} title={`Not ${l.one}`}>
                      <Icon.x />
                    </button>
                  </span>
                );
              })}
              {detail.keywords?.map((k) => (
                <span key={`k:${k}`} className="i-tag" title="Keyword">
                  # {k}
                </span>
              ))}
              {detail.characters.map((c) => (
                <a key={c.id} className="i-tag" href={`#/characters/${c.id}`} onClick={onLeave}
                  title={c.source === 'lineage' ? 'Made from an image of this character' : c.source === 'faces' ? 'Recognized by face' : 'Character, from the LoRAs it was made with'}>
                  <Icon.spark /> {c.name}
                </a>
              ))}
              {detail.albums.map((a) => (
                <a key={a.id} className="i-tag" href={`#/albums/${a.id}`} title="Open the album" onClick={onLeave}>
                  <Icon.album /> {a.name}
                </a>
              ))}
              {detail.people.map((p) => (
                <a key={p.id} className="i-tag" href={`#/people/${p.id}`}
                  title={p.kind === 'pet' ? 'Recognized in this photo' : p.source === 'faces' ? 'Recognized by face' : 'Suggested by an import'} onClick={onLeave}>
                  {p.kind === 'pet' ? <Icon.paw /> : <Icon.person />} {p.name || (p.kind === 'pet' ? 'Unnamed pet' : 'Unnamed person')}
                  {p.source !== 'faces' && <i>?</i>}
                </a>
              ))}
            </section>
          )}
          {((asset.type === 'image' && !detail.generation && LABELS.some((l) => !detail.labels.includes(l.name))) || !detail.labels.includes('adult')) && (
            <section>
              <details className="i-labelas">
                <summary>Label as…</summary>
                <div className="i-tags">
                  {!detail.labels.includes('adult') && (
                    <button type="button" className="i-tag" onClick={() => onLabel('adult')} title="Adult content: nudity or sex" data-testid="label-adult">
                      <Icon.adult /> Adult
                    </button>
                  )}
                  {(asset.type === 'image' && !detail.generation ? LABELS : []).filter((l) => !detail.labels.includes(l.name)).map((l) => {
                    const I = l.icon;
                    return (
                      <button key={l.name} type="button" className="i-tag" onClick={() => onLabel(l.name)} title={`Add to ${l.title}`}>
                        <I /> {l.tag}
                      </button>
                    );
                  })}
                </div>
              </details>
            </section>
          )}
          {detail.generation && <GenerationPanel g={detail.generation} id={asset.id} fileName={detail.fileName} onLeave={onLeave} onFrom={onFrom} />}
          {detail.text && (
            <section>
              <h4>Text in the picture</h4>
              <div className="g-prompt i-text">
                <p>{detail.text}</p>
                <div className="g-row">
                  <Copy text={detail.text} label="Copy text" />
                </div>
              </div>
            </section>
          )}
          {(detail.title || detail.description) && (
            <section>
              {detail.title && <p>{detail.title}</p>}
              {detail.description && <p className="muted">{detail.description}</p>}
            </section>
          )}
          {(detail.owner || detail.shared) && (
            <section>
              <p className="muted small">{detail.owner ? `Shared with the Household by ${detail.owner.name}` : 'Shared with the Household'}</p>
            </section>
          )}
          <section>
            <h4>File</h4>
            <p className="break">{detail.fileName}</p>
            <p className="muted small break">
              {detail.library} / {detail.folderPath === '.' ? '' : detail.folderPath}
            </p>
            {detail.video && !detail.video.direct && (
              <p className="muted small">
                {detail.video.copy
                  ? `${detail.video.codec.toUpperCase()}${detail.video.hdr ? ' HDR' : ''}: browsers that can’t play it get a copy that plays everywhere`
                  : detail.video.waiting
                    ? 'A copy that plays in every browser is being made'
                    : `${detail.video.codec.toUpperCase()}: some browsers can’t play it`}
              </p>
            )}
            {detail.edited && (
              <p className="small">
                Edited · <a href={editedURL(asset.id)} download>Download the edited photo</a> · <a href={`${originalURL(asset.id)}?download=1`} download>the original</a>
              </p>
            )}
            <p className="mono small">
              {asset.width && asset.height ? `${asset.width} × ${asset.height} · ` : ''}
              {formatBytes(detail.fileSize)}
              {detail.mime ? ` · ${detail.mime}` : ''}
            </p>
          </section>
          {cam && (
            <section>
              <h4>Camera</h4>
              <p>{[cam.make, cam.model].filter(Boolean).join(' ')}</p>
              {cam.lens && <p className="muted small">{cam.lens}</p>}
              {exposure && <p className="mono small">{exposure}</p>}
            </section>
          )}
          {detail.location && (
            <section>
              <h4>Location</h4>
              {detail.location.place && (
                <p>
                  {detail.location.placeId ? (
                    <a href={`#/places/${detail.location.placeId}`} onClick={onLeave} title="Everything taken here">{detail.location.place}</a>
                  ) : detail.location.place}
                </p>
              )}
              <p className="mono small">
                {detail.location.lat.toFixed(5)}, {detail.location.lon.toFixed(5)}
              </p>
            </section>
          )}
          {detail.renderError && (
            <section>
              <h4>Preview problem</h4>
              <p className="muted small break">{detail.renderError}</p>
            </section>
          )}
        </>
      )}
    </aside>
  );
}
