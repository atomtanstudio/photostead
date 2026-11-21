/**
 * Shared by the site modules (civitai.js, instagram.js, tiktok.js, pinterest.js): the capture button
 * (the same as on X: Photostead's mark, a spinner while saving, a green check once saved), toasts,
 * and the hand-off to the service worker, which downloads the post's files and uploads them.
 *
 * A site module calls PhotosteadSite.start({ site, scan, collect }):
 *   scan(place)  finds the site's posts and calls place({ owner, mount, resolveId, variant }) for each:
 *                owner is the element the button belongs to (one button per owner), mount(slot) puts
 *                the button in the page, resolveId() gives the post's id (asked again on every click,
 *                for buttons that follow the address, like a single-post page).
 *   collect(id)  reads that post: { site, id, url, author, authorName, caption, details, takenAt,
 *                prompt, media: [{ url, video, ext, key, fallbacks }] } (lib/sites.js checks it).
 *   pageDownload true when the site's media servers serve only its own pages (TikTok's videos): the
 *                files are downloaded here and handed over; any the page can't read (TikTok's
 *                pictures) are left to the service worker.
 */
(function () {
  "use strict";
  if (window.PhotosteadSite) return;

  const DEFAULT_LABEL = "Save this post’s images/video and text to Photostead";
  const SAVED_LABEL = "Saved in Photostead — click to save again";
  const LOGO_SVG = `<svg class="imtc-logo" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 10.6 12 3.6l8.5 7v9.1a.8.8 0 0 1-.8.8H4.3a.8.8 0 0 1-.8-.8z"/><circle cx="12" cy="14.2" r="3.3"/></svg>`;
  const CHECK_SVG = `<svg class="imtc-saved-check" viewBox="0 0 16 16" aria-hidden="true"><path d="M6.2 11.1 3.4 8.3l-1.1 1.1 3.9 3.9 8.4-8.4-1.1-1.1z"/></svg>`;

  const slots = new WeakMap(); // owner → its slot
  const live = new Set(); // buttons on the page, with how to find their post
  const saved = new Map(); // "site:id" → saved before?
  const busy = new Set();
  const pending = new Set();
  let checkTimer = null;

  function toast(message, isError) {
    document.querySelector(".imtc-toast")?.remove();
    const el = document.createElement("div");
    el.className = "imtc-toast" + (isError ? " error" : "");
    el.textContent = message;
    document.body.appendChild(el);
    setTimeout(() => el.remove(), isError ? 6000 : 3500);
  }

  function setLabel(btn, label) {
    btn.title = label;
    btn.setAttribute("aria-label", label);
  }

  function showSaved(btn, on) {
    btn.classList.toggle("imtc-saved", on);
    setLabel(btn, on ? SAVED_LABEL : DEFAULT_LABEL);
  }

  // Which of the posts on the page were saved before: asked in one go, a moment after they appear.
  function queueCheck(key) {
    if (saved.has(key)) return;
    pending.add(key);
    clearTimeout(checkTimer);
    checkTimer = setTimeout(async () => {
      const keys = [...pending];
      pending.clear();
      try {
        const res = await chrome.runtime.sendMessage({ type: "check-captures-exist", payload: { externalIds: keys } });
        const yes = new Set(res?.exists || []);
        for (const key of keys) saved.set(key, yes.has(key));
      } catch (_) { return; }
      refresh();
    }, 200);
  }

  function keyOf(entry) {
    let id = "";
    try { id = entry.resolveId() || ""; } catch (_) { /* not yet */ }
    return id ? `${entry.site}:${id}` : "";
  }

  // Buttons that follow the address (or a post that loaded late) take on the right saved state.
  function refresh() {
    for (const entry of live) {
      if (!entry.btn.isConnected) { live.delete(entry); continue; }
      if (busy.has(entry.key) || entry.btn.classList.contains("imtc-loading")) continue;
      const key = keyOf(entry);
      if (key !== entry.key) entry.key = key;
      if (!key) { showSaved(entry.btn, false); continue; }
      if (saved.has(key)) showSaved(entry.btn, saved.get(key));
      else queueCheck(key);
    }
  }

  // Sites whose media servers serve only the site's own pages (TikTok): the files are fetched here,
  // in the page, and passed to the service worker in pieces over a port, with the post.
  const CHUNK = 3 * 1024 * 1024;
  function base64(bytes) {
    let text = "";
    for (let at = 0; at < bytes.length; at += 0x8000) text += String.fromCharCode(...bytes.subarray(at, at + 0x8000));
    return btoa(text);
  }
  async function fetchFile(m) {
    let lastError = new Error("The file couldn’t be downloaded.");
    for (const url of [m.url, ...(m.fallbacks || [])]) {
      try {
        const response = await fetch(url, { credentials: "include" });
        if (!response.ok) throw new Error(`The site answered ${response.status} for the file. Open the post and try again.`);
        const buffer = await response.arrayBuffer();
        if (!buffer.byteLength) throw new Error("The site sent an empty file.");
        return { bytes: new Uint8Array(buffer), contentType: response.headers.get("content-type") || "" };
      } catch (error) { lastError = error; }
    }
    throw lastError;
  }
  function saveWithFiles(post) {
    return new Promise((resolve, reject) => {
      const port = chrome.runtime.connect({ name: "save-post-with-files" });
      let done = false;
      port.onMessage.addListener((message) => { done = true; port.disconnect(); resolve(message); });
      port.onDisconnect.addListener(() => { if (!done) reject(new Error(chrome.runtime.lastError?.message || "Photostead Capture stopped before the save finished.")); });
      (async () => {
        port.postMessage({ type: "post", post });
        for (const [index, m] of post.media.entries()) {
          // A file the page can't fetch (TikTok's pictures: their servers don't let pages read
          // them) isn't sent; the service worker downloads it itself.
          const got = await fetchFile(m).catch(() => null);
          if (!got) continue;
          const { bytes, contentType } = got;
          port.postMessage({ type: "file", index, size: bytes.length, contentType });
          for (let at = 0; at < bytes.length; at += CHUNK) port.postMessage({ type: "chunk", index, data: base64(bytes.subarray(at, at + CHUNK)) });
        }
        port.postMessage({ type: "end" });
      })().catch((error) => { done = true; port.disconnect(); reject(error); });
    });
  }

  async function save(entry, collect, pageDownload) {
    const btn = entry.btn;
    const id = entry.resolveId();
    if (!id) { toast("Couldn’t tell which post this is. Open the post and try again.", true); return; }
    const key = `${entry.site}:${id}`;
    if (busy.has(key)) return;
    busy.add(key);
    btn.classList.remove("imtc-saved", "imtc-done");
    btn.classList.add("imtc-loading");
    setLabel(btn, "Saving to Photostead…");
    toast("Reading the post…");
    let ok = false;
    try {
      const post = await collect(id);
      const count = post.media?.length || 0;
      toast(`Saving ${count} file${count === 1 ? "" : "s"} to Photostead…`);
      const res = pageDownload ? await saveWithFiles(post) : await chrome.runtime.sendMessage({ type: "save-post", payload: post });
      if (res?.error) throw new Error(res.error);
      const p = res?.photostead;
      if (!p?.ok) throw new Error(p?.configured ? `Photostead save failed: ${p.error || "unknown error"}` : (p?.error || "Pair with Photostead in the extension popup first."));
      ok = true;
      saved.set(key, true);
      toast(`Saved to Photostead (${p.assetCount} file${p.assetCount === 1 ? "" : "s"}${p.duplicate ? ", already there" : ""}).`);
      if (p.openAfterSave && p.materialUrl) chrome.runtime.sendMessage({ type: "open-url", payload: { url: p.materialUrl } }).catch(() => {});
    } catch (error) {
      const message = String(error?.message || error);
      toast(/Extension context invalidated/.test(message) ? "Photostead Capture was updated. Reload the page and try again." : message, true);
    } finally {
      busy.delete(key);
      btn.classList.remove("imtc-loading");
      if (ok) showSaved(btn, true);
      else showSaved(btn, saved.get(key) === true);
    }
  }

  function makeSlot(site, variant) {
    const slot = document.createElement("div");
    slot.className = `imtc-forensic-slot psc-slot psc-${site}${variant ? ` psc-${variant}` : ""}`;
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "imtc-forensic-btn";
    btn.setAttribute("data-testid", "photostead-capture");
    setLabel(btn, DEFAULT_LABEL);
    btn.innerHTML = `<span class="imtc-btn-wrap"><span class="imtc-btn-inner"><span class="imtc-btn-icon">${LOGO_SVG}<span class="imtc-saved-badge">${CHECK_SVG}</span></span></span></span>`;
    slot.appendChild(btn);
    // Keep the site from treating a press on the button as a press on the post under it.
    for (const type of ["pointerdown", "mousedown", "mouseup", "touchstart", "dblclick"]) {
      slot.addEventListener(type, (e) => e.stopPropagation());
    }
    return { slot, btn };
  }

  // A button over the corner of a picture (CivitAI and Pinterest grids).
  function overlay(container) {
    return (slot) => {
      if (getComputedStyle(container).position === "static") container.style.position = "relative";
      slot.classList.add("psc-overlay");
      container.appendChild(slot);
    };
  }

  function start({ site, scan, collect, pageDownload = false }) {
    function place({ owner, mount, resolveId, variant }) {
      if (!owner || slots.get(owner)?.isConnected) return;
      const { slot, btn } = makeSlot(site, variant);
      const entry = { site, btn, resolveId, key: "" };
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        save(entry, collect, pageDownload);
      });
      try { mount(slot); } catch (_) { return; }
      if (!slot.isConnected) return;
      slots.set(owner, slot);
      live.add(entry);
    }

    let timer = null;
    const run = () => {
      timer = null;
      try { scan(place); } catch (error) { console.debug("Photostead Capture:", error); }
      refresh();
    };
    const schedule = () => { if (!timer) timer = setTimeout(run, 250); };
    const init = () => {
      run();
      new MutationObserver(schedule).observe(document.body, { childList: true, subtree: true });
    };
    if (document.body) init();
    else document.addEventListener("DOMContentLoaded", init, { once: true });
  }

  // Helpers the site modules share.
  const helpers = {
    // The JSON a page carries in a <script> (by id or type), parsed; null when it isn't there.
    scriptJson(doc, selector) {
      const out = [];
      for (const el of doc.querySelectorAll(selector)) {
        try { out.push(JSON.parse(el.textContent)); } catch (_) { /* not JSON */ }
      }
      return out;
    },
    // Another page of the same site, as a document (its embedded data, for posts not on this page).
    async fetchDocument(path) {
      const response = await fetch(path, { credentials: "include" });
      if (!response.ok) throw new Error(`The site answered ${response.status}. Open the post and try again.`);
      return new DOMParser().parseFromString(await response.text(), "text/html");
    },
    async fetchJson(path, headers = {}) {
      const response = await fetch(path, { credentials: "include", headers: { Accept: "application/json", ...headers } });
      if (!response.ok) throw new Error(`The site answered ${response.status}.`);
      return response.json();
    },
    // Depth-first search of parsed JSON for the first object that passes test.
    findDeep(value, test, depth = 0) {
      if (!value || typeof value !== "object" || depth > 60) return null;
      if (!Array.isArray(value) && test(value)) return value;
      for (const child of Array.isArray(value) ? value : Object.values(value)) {
        const found = helpers.findDeep(child, test, depth + 1);
        if (found) return found;
      }
      return null;
    },
    largest(list, size = (x) => (x.width || 0) * (x.height || 0)) {
      return [...(list || [])].sort((a, b) => size(b) - size(a))[0] || null;
    },
  };

  window.PhotosteadSite = { start, overlay, toast, saveWithFiles, ...helpers };
})();
