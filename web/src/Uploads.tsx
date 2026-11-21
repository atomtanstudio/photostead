import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { Icon } from './icons';
import { readSidecars, type Extra, type Picked } from './sidecars';

/**
 * Uploading from the browser: drop files or whole folders anywhere on the page (or pick them),
 * and they go up three at a time into your library, or into the album you're looking at. A panel
 * in the corner follows them: how far along, what was already here (Photostead keeps one copy),
 * what failed (try again). Leaving the page while they're going asks first.
 */

type State = 'waiting' | 'checking' | 'sending' | 'added' | 'here' | 'failed';

export type Job = { id: number; file: File; name: string; album?: string; extra?: Extra; state: State; sent: number; error?: string };

/** Photos, videos and RAW files: what a dropped folder's files are filtered to. */
const MEDIA = /\.(jpe?g|png|gif|webp|avif|heic|heif|tiff?|bmp|dng|cr2|cr3|nef|nrw|arw|srf|sr2|raf|orf|rw2|pef|srw|3fr|iiq|x3f|mp4|m4v|mov|avi|mkv|webm|3gp|mts|m2ts|mpg|mpeg|wmv)$/i;
export const ACCEPT = 'image/*,video/*,.dng,.cr2,.cr3,.nef,.nrw,.arw,.raf,.orf,.rw2,.pef,.srw,.heic,.heif';
const AT_ONCE = 3;
const HASH_UP_TO = 256 << 20; // above this a file isn't read twice: the server sorts out duplicates

class Queue {
  jobs: Job[] = [];
  private next = 1;
  private running = 0;
  private listeners = new Set<() => void>();
  private snapshot: Job[] = [];
  onAdded: (() => void) | null = null;
  private lastAdded = 0;

  subscribe = (f: () => void) => {
    this.listeners.add(f);
    return () => this.listeners.delete(f);
  };
  get = () => this.snapshot;
  private changed() {
    this.snapshot = [...this.jobs];
    this.listeners.forEach((f) => f());
  }

  from = ''; // where the last lot came from, when its notes were read: "Google Takeout", "XMP sidecars"

  /** Adds what was picked or dropped: photos and videos, with what their sidecars say (Google
   *  Takeout's JSON, XMP), and without what Takeout says was in the trash. */
  async add(picked: Picked[], album?: string) {
    const { extras, from } = await readSidecars(picked);
    this.from = from;
    let n = 0;
    for (const { file, path } of picked) {
      if (!(MEDIA.test(file.name) || file.type.startsWith('image/') || file.type.startsWith('video/'))) continue;
      const extra = extras.get(path);
      if (extra?.skip) continue;
      this.jobs.push({ id: this.next++, file, name: file.name, album, extra, state: 'waiting', sent: 0 });
      n++;
    }
    this.changed();
    this.pump();
    return n;
  }

  retry() {
    for (const j of this.jobs) if (j.state === 'failed') Object.assign(j, { state: 'waiting', sent: 0, error: undefined });
    this.changed();
    this.pump();
  }

  /** Forgets what's finished (and, with everything, what's waiting too). */
  clear(everything = false) {
    this.jobs = this.jobs.filter((j) => (everything ? j.state === 'sending' || j.state === 'checking' : !['added', 'here', 'failed'].includes(j.state)));
    this.changed();
  }

  get busy() {
    return this.jobs.some((j) => j.state === 'waiting' || j.state === 'checking' || j.state === 'sending');
  }

  private pump() {
    while (this.running < AT_ONCE) {
      const job = this.jobs.find((j) => j.state === 'waiting');
      if (!job) break;
      this.running++;
      void this.send(job).finally(() => {
        this.running--;
        this.pump();
        if (!this.busy || Date.now() - this.lastAdded > 4000) {
          this.lastAdded = Date.now();
          this.onAdded?.(); // the timeline catches up as they arrive, and once at the end
        }
      });
    }
  }

  private set(job: Job, patch: Partial<Job>) {
    Object.assign(job, patch);
    this.changed();
  }

  private async send(job: Job) {
    // Already in the library? Asked first unless notes came with it (an album, a date, a place:
    // the server applies those to the copy it has, so the file goes anyway).
    if (!job.album && !job.extra && job.file.size <= HASH_UP_TO && crypto.subtle) {
      // Already in the library? (Only where the browser can hash: https, or this computer.)
      this.set(job, { state: 'checking' });
      try {
        const sum = hex(await crypto.subtle.digest('SHA-256', await job.file.arrayBuffer()));
        const r = await fetch('/api/v1/uploads/check', {
          method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ sha256: [sum] }),
        });
        if (r.ok && ((await r.json()) as { known: string[] }).known.includes(sum)) {
          this.set(job, { state: 'here', sent: job.file.size });
          return;
        }
      } catch {
        // asked about nothing: send it, and the server sorts it out
      }
    }
    this.set(job, { state: 'sending', sent: 0 });
    const form = new FormData();
    const meta = { fileName: job.file.name, takenAt: new Date(job.file.lastModified).toISOString(), ...job.extra };
    delete (meta as Extra).skip;
    form.append('meta', new Blob([JSON.stringify(meta)], { type: 'application/json' }), 'meta.json');
    form.append('file', job.file, job.file.name);
    const url = job.album ? `/api/v1/albums/${job.album}/upload` : '/api/v1/uploads';
    await new Promise<void>((done) => {
      const x = new XMLHttpRequest();
      x.open('POST', url);
      x.withCredentials = true;
      x.upload.onprogress = (e) => this.set(job, { sent: e.loaded });
      x.onload = () => {
        if (x.status >= 200 && x.status < 300) {
          let status = '';
          try {
            status = (JSON.parse(x.responseText) as { status?: string }).status ?? '';
          } catch {
            // a reply without a body is fine
          }
          this.set(job, { state: status === 'duplicate' ? 'here' : 'added', sent: job.file.size });
        } else {
          let why = `Photostead said no (${x.status}).`;
          try {
            const p = JSON.parse(x.responseText) as { detail?: string; title?: string };
            why = p.detail || p.title || why;
          } catch {
            // keep the status
          }
          this.set(job, { state: 'failed', error: why });
        }
        done();
      };
      x.onerror = () => {
        this.set(job, { state: 'failed', error: 'The connection dropped.' });
        done();
      };
      x.send(form);
    });
  }
}

const hex = (b: ArrayBuffer) => [...new Uint8Array(b)].map((x) => x.toString(16).padStart(2, '0')).join('');

export const uploads = new Queue();

export function useUploads() {
  return useSyncExternalStore(uploads.subscribe, uploads.get);
}

/** Every file in what was dropped, folders opened all the way down, with its path. */
async function dropped(dt: DataTransfer): Promise<Picked[]> {
  const entries = [...dt.items].map((i) => i.webkitGetAsEntry?.()).filter((e): e is FileSystemEntry => !!e);
  if (entries.length === 0) return [...dt.files].map((file) => ({ file, path: file.name }));
  const out: Picked[] = [];
  const walk = async (e: FileSystemEntry): Promise<void> => {
    if (e.isFile) {
      const file = await new Promise<File>((ok, no) => (e as FileSystemFileEntry).file(ok, no));
      out.push({ file, path: e.fullPath.replace(/^\//, '') });
    } else if (e.isDirectory) {
      const reader = (e as FileSystemDirectoryEntry).createReader();
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((ok, no) => reader.readEntries(ok, no));
        if (batch.length === 0) break;
        for (const child of batch) await walk(child);
      }
    }
  };
  for (const e of entries) await walk(e);
  return out.filter((p) => !p.file.name.startsWith('.'));
}

/**
 * Dropping onto the page (anywhere), the corner panel, and asking before leaving mid-upload.
 * album: the album open now that takes them (yours, or one you contribute to).
 */
export function Uploads({ album, albumTitle, onAdded }: { album?: string; albumTitle?: string; onAdded: () => void }) {
  const jobs = useUploads();
  const [over, setOver] = useState(false);
  const [open, setOpen] = useState(false);
  const depth = useRef(0);
  uploads.onAdded = onAdded;

  useEffect(() => {
    const files = (e: DragEvent) => !!e.dataTransfer && [...e.dataTransfer.types].includes('Files');
    const enter = (e: DragEvent) => {
      if (!files(e)) return;
      e.preventDefault();
      depth.current++;
      setOver(true);
    };
    const leave = (e: DragEvent) => {
      if (!files(e)) return;
      depth.current = Math.max(0, depth.current - 1);
      if (depth.current === 0) setOver(false);
    };
    const overIt = (e: DragEvent) => {
      if (files(e)) e.preventDefault();
    };
    const drop = (e: DragEvent) => {
      if (!files(e) || !e.dataTransfer) return;
      e.preventDefault();
      depth.current = 0;
      setOver(false);
      const dt = e.dataTransfer;
      void dropped(dt).then((fs) => uploads.add(fs, album));
    };
    window.addEventListener('dragenter', enter);
    window.addEventListener('dragleave', leave);
    window.addEventListener('dragover', overIt);
    window.addEventListener('drop', drop);
    return () => {
      window.removeEventListener('dragenter', enter);
      window.removeEventListener('dragleave', leave);
      window.removeEventListener('dragover', overIt);
      window.removeEventListener('drop', drop);
    };
  }, [album]);

  useEffect(() => {
    const ask = (e: BeforeUnloadEvent) => {
      if (uploads.busy) e.preventDefault();
    };
    window.addEventListener('beforeunload', ask);
    return () => window.removeEventListener('beforeunload', ask);
  }, []);

  const total = jobs.reduce((s, j) => s + j.file.size, 0);
  const sent = jobs.reduce((s, j) => s + j.sent, 0);
  const count = (st: State[]) => jobs.filter((j) => st.includes(j.state)).length;
  const left = count(['waiting', 'checking', 'sending']);
  const added = count(['added']);
  const here = count(['here']);
  const failed = count(['failed']);
  const busy = left > 0;

  return (
    <>
      {over && (
        <div className="drop-over" aria-live="polite">
          <div>
            <Icon.upload />
            <b>{album ? `Drop to add to ${albumTitle || 'this album'}` : 'Drop to add to your library'}</b>
            <span>Photos, videos and folders of them</span>
          </div>
        </div>
      )}
      {jobs.length > 0 && (
        <section className="up-panel" aria-label="Uploads" data-testid="uploads">
          <header>
            <div>
              <b>
                {busy ? `Uploading ${(jobs.length - left + 1).toLocaleString()} of ${jobs.length.toLocaleString()}` : failed ? 'Some didn’t upload' : 'Uploaded'}
              </b>
              <small className="muted">
                {uploads.from && <>With what {uploads.from} say: dates, places, favorites, albums. </>}
                {[added && `${added.toLocaleString()} added`, here && `${here.toLocaleString()} already here`, failed && `${failed.toLocaleString()} failed`]
                  .filter(Boolean).join(' · ') || 'Starting…'}
              </small>
            </div>
            <button type="button" className="up-x" onClick={() => setOpen(!open)} aria-expanded={open}>{open ? 'Hide' : 'Show'}</button>
            {!busy && <button type="button" className="up-x" onClick={() => uploads.clear(true)} aria-label="Close">✕</button>}
          </header>
          <div className="up-bar"><i style={{ width: `${total ? Math.round((sent / total) * 100) : 0}%` }} /></div>
          {failed > 0 && !busy && (
            <button type="button" className="up-retry" onClick={() => uploads.retry()}>Try the {failed === 1 ? 'one' : failed.toLocaleString()} that failed again</button>
          )}
          {open && (
            <ul className="up-list">
              {jobs.slice(-200).map((j) => (
                <li key={j.id} title={j.error}>
                  <span>{j.name}</span>
                  <em className={j.state === 'failed' ? 'r-warn' : ''}>
                    {j.state === 'sending' ? `${Math.round((j.sent / Math.max(1, j.file.size)) * 100)}%` : LABEL[j.state]}
                  </em>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </>
  );
}

const LABEL: Record<State, string> = { waiting: 'waiting', checking: 'checking', sending: '', added: 'added', here: 'already here', failed: 'failed' };

/** Picking files (or a folder) to upload, from the rail or an album. */
export function pickFiles(folder: boolean, album?: string) {
  const input = document.createElement('input');
  input.type = 'file';
  input.multiple = true;
  if (folder) input.webkitdirectory = true;
  else input.accept = ACCEPT;
  input.onchange = () => input.files && void uploads.add([...input.files].map((file) => ({ file, path: file.webkitRelativePath || file.name })), album);
  input.click();
}
