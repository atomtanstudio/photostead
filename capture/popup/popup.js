import { serverUrl, parsePairing } from '../lib/photostead.js';
const $ = id => document.getElementById(id);
async function send(type, payload) {
  const result = await chrome.runtime.sendMessage({ type, payload });
  if (result?.error) throw new Error(result.error);
  return result;
}
function status(text, error = false) { $('status').textContent = text; $('status').classList.toggle('error', error); }
// What to call this browser in Photostead's list of devices.
async function deviceName() {
  const brave = await navigator.brave?.isBrave?.().catch(() => false);
  const ua = navigator.userAgent;
  const browser = brave ? 'Brave' : /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : 'Chrome';
  const os = /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : '';
  return `Photostead Capture in ${browser}${os ? ` on ${os}` : ''}`;
}
// Server access is asked for while handling the click (a browser rule), for every address known then.
async function allow(urls) {
  const origins = [...new Set(urls.map(u => `${new URL(u).origin}/*`))];
  if (!origins.length) return;
  if (!(await chrome.permissions.request({ origins }))) throw new Error('Access to Photostead wasn’t allowed.');
}
async function refresh() {
  const { settings, jobs } = await send('get-stats');
  const paired = Boolean(settings.token || settings.apiKey);
  $('paired').hidden = !paired;
  $('pair').hidden = paired;
  $('where').textContent = [settings.serverUrl, ...(settings.addresses || []).filter(a => a !== settings.serverUrl)].join(' · ');
  $('url').value = settings.serverUrl || '';
  $('open').checked = settings.openAfterSave;
  const pending = jobs.filter(j => !j.ok);
  $('jobs').textContent = `${jobs.filter(j => j.ok).length} saved posts · ${pending.length} interrupted saves`;
  status(pending[0]?.error || (paired ? 'Ready. Use the capture button on a post.' : 'Pair with Photostead to start.'), Boolean(pending[0]?.error));
}
$('pair').addEventListener('submit', async event => {
  event.preventDefault();
  const button = event.submitter; button.disabled = true;
  try {
    const link = parsePairing($('code').value);
    const typed = $('url').value.trim();
    await allow([...link.urls, ...(typed ? [serverUrl(typed)] : [])]);
    status('Pairing…');
    const r = await send('pair', { text: $('code').value, serverUrl: typed, name: await deviceName() });
    $('code').value = '';
    await refresh();
    status(`Paired. Photostead answers at ${r.addresses[0]}.`);
  } catch (error) { status(error.message, true); } finally { button.disabled = false; }
});
$('usekey').onclick = async () => {
  $('usekey').disabled = true;
  try {
    const url = serverUrl($('url').value.trim());
    await allow([url]);
    await send('update-settings', { serverUrl: url, apiKey: $('key').value.trim(), openAfterSave: $('open').checked });
    status('Checking connection…');
    await send('test-connection');
    $('key').value = '';
    await refresh();
    status('Connected to Photostead.');
  } catch (error) { status(error.message, true); } finally { $('usekey').disabled = false; }
};
$('test').onclick = async () => {
  $('test').disabled = true; status('Checking connection…');
  try { const r = await send('test-connection'); await refresh(); status(`Connected to Photostead at ${r.url}.`); }
  catch (error) { status(error.message, true); } finally { $('test').disabled = false; }
};
$('unpair').onclick = async () => {
  if (!confirm('Unpair this browser? Captures stop until you pair again. (You can also sign it out in Photostead.)')) return;
  await send('unpair').catch(error => status(error.message, true));
  await refresh();
};
$('open').onchange = () => send('set-open-after-save', { on: $('open').checked }).catch(error => status(error.message, true));
$('retry').onclick = async () => {
  $('retry').disabled = true; status('Retrying interrupted saves…');
  try { const r = await send('retry-failed'); await refresh(); status(`${r.saved} saved; ${r.failed} failed.`, Boolean(r.failed)); }
  catch (error) { status(error.message, true); } finally { $('retry').disabled = false; }
};
$('visit').onclick = () => send('open-url').catch(error => status(error.message, true));
refresh().catch(error => status(error.message, true));
