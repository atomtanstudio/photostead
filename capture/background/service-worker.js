import { buildForensicBundleRecord } from '../lib/forensic-bundle.js';
import { saveCapture, getCapture, getAllCapturesForExport, listCaptures, countCaptures, deleteCapture, clearAllCaptures } from '../lib/storage.js';
import { SITES, siteOf, postPlan } from '../lib/sites.js';
import { FileReceiver } from '../lib/transfer.js';
import { serverUrl, capturePlan, testConnection, uploadItem, pair, parsePairing, addressesOf, forgetReachable } from '../lib/photostead.js';

// Paired: a device token, the server's id, and its addresses (the one that answered last first).
// apiKey is a key made on the server, from before pairing; it still works.
const defaults = { serverUrl: '', addresses: [], serverId: '', token: '', apiKey: '', deviceName: '', openAfterSave: false };
const getSettings = async () => ({ ...defaults, ...(await chrome.storage.local.get('photosteadSettings')).photosteadSettings });
const connected = settings => Boolean(settings.token || settings.apiKey);
// Keeps the address that answered (api() moves it to the front) for next time.
async function keepAddress(settings, before) {
  if (settings.serverUrl !== before) await chrome.storage.local.set({ photosteadSettings: settings });
}
// Credentials are available only to extension pages/service worker, never content scripts.
chrome.storage.local.setAccessLevel({ accessLevel: 'TRUSTED_CONTEXTS' });
let queue = Promise.resolve();
const serial = fn => { const result = queue.then(fn, fn); queue = result.catch(() => {}); return result; };

// Which Photostead a save went to (so a retry goes to the same one): its id once paired, whatever
// address it's reached at.
async function destination(settings) {
  if (settings.serverId) return settings.serverId;
  const bytes = new TextEncoder().encode(`${serverUrl(settings.serverUrl)}\n${settings.apiKey}`);
  return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)), b => b.toString(16).padStart(2, '0')).join('');
}
// files: the bytes of the plan's items (by their index in the post) that the page downloaded.
async function saveToPhotostead(record, files = null) {
  const settings = await getSettings();
  const before = settings.serverUrl;
  const keepAlive = setInterval(() => chrome.runtime.getPlatformInfo().catch(() => {}), 20000);
  try {
    if (!connected(settings)) throw new Error('Pair with Photostead in the extension popup first.');
    const target = await destination(settings);
    const plan = record.plan || capturePlan(record);
    // Store only selected tweet text/media URLs and resumable upload state.
    const job = { captureId: record.captureId, capturedAt: record.capturedAt, focalTweetId: plan.tweetId, plan, target,
      checkpoints: record.target === target ? record.checkpoints || {} : {}, photostead: { ok: false } };
    await saveCapture(job);
    await chrome.action.setBadgeText({ text: '…' });
    const assetIds = [];
    try {
      for (const item of plan.items) {
        const checkpoint = job.checkpoints[item.key] || {};
        await uploadItem(settings, item, checkpoint, async state => {
          job.checkpoints[item.key] = state;
          await saveCapture(job);
        }, files?.get(item.index));
        assetIds.push(checkpoint.assetId);
      }
      job.photostead = { ok: true, configured: true, assetCount: assetIds.length, assetIds,
        duplicate: plan.items.every(item => job.checkpoints[item.key]?.duplicate),
        materialUrl: `${serverUrl(settings.serverUrl)}/`, openAfterSave: settings.openAfterSave };
      await saveCapture(job);
      await keepAddress(settings, before);
      await chrome.action.setBadgeText({ text: '✓' });
      await chrome.action.setBadgeBackgroundColor({ color: '#287b65' });
      return job.photostead;
    } catch (error) {
      job.photostead = { ok: false, configured: true, error: error.message };
      await saveCapture(job);
      throw error;
    }
  } catch (error) {
    await chrome.action.setBadgeText({ text: '!' });
    await chrome.action.setBadgeBackgroundColor({ color: '#b53949' });
    return { ok: false, configured: connected(settings), error: error.message };
  } finally { clearInterval(keepAlive); }
}

// A post from a site whose media servers serve only its own pages (TikTok): the page sends the post,
// then each file in base64 pieces, then "end"; the answer is the save's result.
chrome.runtime.onConnect.addListener(port => {
  if (port.name !== 'save-post-with-files') return;
  const site = siteOf(port.sender?.url);
  if (!site || !SITES[site].pageDownload) { port.disconnect(); return; }
  let post = null;
  const receiver = new FileReceiver();
  port.onMessage.addListener(message => {
    if (message.type === 'post') post = message.post;
    else if (message.type === 'file' || message.type === 'chunk') { if (!receiver.add(message)) port.disconnect(); }
    else if (message.type === 'end') {
      serial(async () => {
        if (post?.site !== site) throw new Error('A post can only be saved from its own site.');
        const plan = postPlan(post);
        const files = receiver.files(plan.items);
        return { photostead: await saveToPhotostead({ captureId: crypto.randomUUID(), capturedAt: new Date().toISOString(), focalTweetId: plan.tweetId, plan }, files) };
      }).then(result => port.postMessage(result), error => port.postMessage({ error: error.message }));
    }
  });
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  (async () => {
    const fromPage = Boolean(sender.tab);
    if (fromPage && !['ingest-forensic-bundle','check-capture-exists','check-captures-exist','save-post','open-url','graphql-capture','set-forensic-active'].includes(message.type)) throw new Error('Open the extension popup for this action.');
    const onX = /^https:\/\/(x\.com|twitter\.com)\//.test(sender.url || '');
    if (fromPage && !onX && !siteOf(sender.url)) throw new Error('Capture isn’t available on this site.');
    if (fromPage && !onX && !['save-post','check-captures-exist','open-url'].includes(message.type)) throw new Error('That’s only for X.');
    switch (message.type) {
      case 'graphql-capture': case 'set-forensic-active': return { ok: true };
      case 'get-stats': return { settings: await getSettings(), count: await countCaptures(), jobs: (await getAllCapturesForExport()).map(j => ({ captureId: j.captureId, tweetId: j.focalTweetId, ok: j.photostead?.ok, error: j.photostead?.error })) };
      case 'update-settings': { // with an API key made on the server, instead of pairing
        const settings = { ...defaults, serverUrl: serverUrl(message.payload.serverUrl), apiKey: String(message.payload.apiKey || '').trim(), openAfterSave: Boolean(message.payload.openAfterSave) };
        await chrome.storage.local.set({ photosteadSettings: settings });
        forgetReachable();
        return { ok: true };
      }
      case 'pair': {
        const { text, serverUrl: typed, name } = message.payload || {};
        const p = parsePairing(text);
        if (!/^[A-Za-z0-9 -]{8,20}$/.test(p.code)) throw new Error('Paste the pairing link, or type the code Photostead shows (like ABCD-2345).');
        const urls = [...new Set([...p.urls, typed && serverUrl(typed)].filter(Boolean))];
        const paired = await pair(urls, p.code, name || 'Photostead Capture');
        const settings = { ...(await getSettings()), ...paired, apiKey: '', deviceName: name || 'Photostead Capture' };
        await chrome.storage.local.set({ photosteadSettings: settings });
        forgetReachable();
        return { ok: true, addresses: addressesOf(settings) };
      }
      case 'set-open-after-save': {
        const settings = { ...(await getSettings()), openAfterSave: Boolean(message.payload?.on) };
        await chrome.storage.local.set({ photosteadSettings: settings });
        return { ok: true };
      }
      case 'unpair': { // forget the token here (the device can also be signed out from Photostead)
        const settings = { ...(await getSettings()), token: '', apiKey: '', serverId: '', addresses: [] };
        await chrome.storage.local.set({ photosteadSettings: settings });
        forgetReachable();
        return { ok: true };
      }
      case 'test-connection': {
        const settings = await getSettings();
        const before = settings.serverUrl;
        const result = await testConnection(settings);
        await keepAddress(settings, before);
        return { ...result, url: settings.serverUrl };
      }
      case 'ingest-forensic-bundle': return serial(async () => {
        const payload = message.payload || {};
        const record = await buildForensicBundleRecord({ captureId: crypto.randomUUID(), capturedAt: new Date().toISOString(), focalTweetId: payload.tweetId, pageUrl: payload.pageUrl, apiResponses: payload.apiResponses || [], bulkCapture: true });
        return { photostead: await saveToPhotostead(record) };
      });
      case 'retry-failed': return serial(async () => {
        let saved = 0, failed = 0;
        const settings = await getSettings();
        const target = await destination(settings);
        for (const job of await getAllCapturesForExport()) {
          if (job.photostead?.ok || job.target !== target) continue;
          const result = await saveToPhotostead(job);
          if (result.ok) saved++; else { failed++; break; }
        }
        return { saved, failed };
      });
      case 'save-post': return serial(async () => { // from a site module: CivitAI, Instagram, TikTok, Pinterest
        const post = message.payload || {};
        if (fromPage && siteOf(sender.url) !== post.site) throw new Error('A post can only be saved from its own site.');
        const plan = postPlan(post);
        return { photostead: await saveToPhotostead({ captureId: crypto.randomUUID(), capturedAt: new Date().toISOString(), focalTweetId: plan.tweetId, plan }) };
      });
      case 'check-captures-exist': { // many at once, for the buttons on a page full of posts
        const target = await destination(await getSettings());
        const asked = new Set((message.payload?.externalIds || []).map(String));
        const saved = new Set((await getAllCapturesForExport()).filter(j => j.target === target && j.photostead?.ok && asked.has(j.focalTweetId)).map(j => j.focalTweetId));
        return { exists: [...saved] };
      }
      case 'check-capture-exists': {
        const target = await destination(await getSettings());
        const jobs = await getAllCapturesForExport();
        return { exists: jobs.some(j => j.target === target && j.focalTweetId === message.payload?.externalId && j.photostead?.ok) };
      }
      case 'open-url': {
        const settings = await getSettings();
        const url = new URL(message.payload?.url || settings.serverUrl);
        if (!addressesOf(settings).some(a => new URL(a).origin === url.origin)) throw new Error('Unexpected destination.');
        await chrome.tabs.create({ url: url.href }); return { ok: true };
      }
      case 'list-captures': return listCaptures(message.payload || {});
      case 'get-capture': return getCapture(message.payload.captureId);
      case 'export-all': return { captures: await getAllCapturesForExport() };
      case 'delete-capture': await deleteCapture(message.payload.captureId); return { ok: true };
      case 'clear-all': await clearAllCaptures(); return { ok: true };
      default: throw new Error('Unsupported action.');
    }
  })().then(sendResponse, error => sendResponse({ error: error.message }));
  return true;
});
