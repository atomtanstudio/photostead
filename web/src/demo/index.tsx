// The demo build (npm run build:demo): the real web app, answered in the browser by DemoServer
// from a static sample library, with its pictures served as plain files. Nothing is saved.

import { useState } from 'react';
import { mediaFrom, type PhotoEdit } from '../api';
import { DemoServer, type DemoRequest, type DemoResponse } from './engine';
import type { DemoLibrary } from './library';
import { renderEdit, type Rendered } from './render';

const BASE = import.meta.env.BASE_URL; // /demo/

/** Loads the library and routes the app's /api requests (fetch, XMLHttpRequest and beacons) to the demo server. */
export async function installDemo() {
  const lib: DemoLibrary = await fetch(`${BASE}library.json`).then((r) => r.json());
  const server = new DemoServer(lib);
  const types = new Map(lib.items.map((i) => [i.id, i.type]));
  const hashes = new Map(lib.items.map((i) => [i.id, i.thumbhash]));
  const edited = new Map<string, Rendered>(); // edited photos' previews, made in the browser
  mediaFrom((id, what) => {
    const e = edited.get(id);
    if (what === 'face') return `${BASE}media/f/${id}.webp`;
    if (what === 'thumbnail') return e?.thumbnail ?? `${BASE}media/t/${id}.webp`;
    if (what === 'preview' || what === 'edited') return e?.preview ?? `${BASE}media/p/${id}.webp`;
    if ((what === 'original' || what === 'play') && types.get(id) === 'video') return `${BASE}media/v/${id}.mp4`;
    return `${BASE}media/p/${id}.webp`; // the picture as it is (base, original): the demo's largest
  });

  // A saved edit's previews are made in the background, as the server makes them; the editor
  // waits for the item's new ThumbHash. Undoing one goes back to the picture as it is.
  const afterEdit = (u: URL, res: DemoResponse) => {
    const id = u.pathname.match(/^\/api\/v1\/assets\/([0-9a-f-]{36})\/edit$/)?.[1];
    const recipe = id && res.status === 200 ? (res.body as PhotoEdit).recipe : undefined;
    if (!id || recipe === undefined) return;
    if (recipe === null) {
      edited.delete(id);
      return;
    }
    renderEdit(`${BASE}media/p/${id}.webp`, recipe).then(
      (r) => {
        // Shown from when the item's ThumbHash changes, unless it was edited again (or undone) meanwhile.
        const before = edited.get(id);
        edited.set(id, r);
        if (!server.rendered(id, recipe, r.thumbhash)) {
          if (before) edited.set(id, before);
          else edited.delete(id);
        }
      },
      () => server.rendered(id, recipe, hashes.get(id) ?? ''),
    );
  };

  const apiPath = (url: string) => {
    const u = new URL(url, window.location.href);
    return u.origin === window.location.origin && u.pathname.startsWith('/api/') ? u : null;
  };
  const answer = (method: string, u: URL, text: string | null) => {
    let body: Record<string, unknown> = {};
    try {
      if (text) body = JSON.parse(text);
    } catch {
      /* not JSON: a form upload, which the demo doesn't take */
    }
    const req: DemoRequest = { method: method.toUpperCase(), path: u.pathname, query: u.searchParams, body };
    return server.handle(req);
  };

  const realFetch = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input instanceof Request ? input.url : String(input);
    const u = apiPath(url);
    if (!u) return realFetch(input, init);
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET');
    const text = typeof init?.body === 'string' ? init.body : input instanceof Request ? await input.text() : null;
    const res = answer(method, u, text);
    afterEdit(u, res);
    return res.body === undefined
      ? new Response(null, { status: res.status })
      : new Response(JSON.stringify(res.body), { status: res.status, headers: { 'Content-Type': res.status < 400 ? 'application/json' : 'application/problem+json' } });
  };
  const realBeacon = navigator.sendBeacon?.bind(navigator);
  navigator.sendBeacon = (url: string | URL, data?: BodyInit | null) => {
    const u = apiPath(String(url));
    if (!u) return realBeacon ? realBeacon(url, data) : false;
    answer('POST', u, typeof data === 'string' ? data : null);
    return true;
  };
  // Uploads go by XMLHttpRequest (for their progress): answered here too, which says they need a server.
  const xhr = XMLHttpRequest.prototype;
  const realOpen = xhr.open as (this: XMLHttpRequest, ...args: unknown[]) => void;
  const realSend = xhr.send;
  const to = new WeakMap<XMLHttpRequest, { method: string; u: URL }>();
  xhr.open = function (this: XMLHttpRequest, ...args: unknown[]) {
    const u = apiPath(String(args[1]));
    if (u) to.set(this, { method: String(args[0]), u });
    else to.delete(this);
    realOpen.apply(this, args);
  } as typeof xhr.open;
  xhr.send = function (this: XMLHttpRequest, body?: Document | XMLHttpRequestBodyInit | null) {
    const t = to.get(this);
    if (!t) return realSend.call(this, body);
    const res = answer(t.method, t.u, typeof body === 'string' ? body : null);
    const text = res.body === undefined ? '' : JSON.stringify(res.body);
    setTimeout(() => {
      for (const [k, v] of [['readyState', 4], ['status', res.status], ['responseText', text], ['response', text]] as const) Object.defineProperty(this, k, { value: v });
      for (const e of ['readystatechange', 'load', 'loadend']) this.dispatchEvent(new ProgressEvent(e));
    }, 0);
  };
  return <DemoBadge />;
}

const TRY = [
  'Search “beach at sunset” or “receipt”',
  'Ask “when did I last take pictures of Miso?”',
  'Open Places, or People then Pepper',
  'Open a photo and press e to edit it',
  'Open Trips, then Lisbon, then Activity',
  'Open Made with AI and look at a prompt',
];

/** Says this is the demo, what to try, and where the real thing is. */
function DemoBadge() {
  const [open, setOpen] = useState(false);
  return (
    <aside className={`demo-badge${open ? ' open' : ''}`} aria-label="About this demo">
      <button type="button" className="demo-pill" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <span className="demo-dot" aria-hidden="true" />
        Demo
      </button>
      {open && (
        <div className="demo-card">
          <p>
            <b>A sample library, running in your browser.</b> The household and every picture here were made with AI for this demo. Changes last until you reload.
          </p>
          <ul>
            {TRY.map((t) => (
              <li key={t}>{t}</li>
            ))}
          </ul>
          <a href="/">Back to photostead.com</a>
        </div>
      )}
    </aside>
  );
}
