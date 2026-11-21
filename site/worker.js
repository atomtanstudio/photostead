// photostead.com's only Worker code: byte ranges for the launch film. Cloudflare's static assets answer a Range
// request with the whole file, and Safari (iPhone, iPad, Mac) won't play a video served that way. This runs only
// for /film/* (assets.run_worker_first in wrangler.jsonc); every other path is served straight from the assets.
export default {
  async fetch(request, env) {
    const res = await env.ASSETS.fetch(request);
    if (res.status !== 200 || !res.body) return res;
    const headers = new Headers(res.headers);
    headers.set('Accept-Ranges', 'bytes');
    const m = /^bytes=(\d*)-(\d*)$/.exec((request.headers.get('Range') || '').trim());
    if (!m || (m[1] === '' && m[2] === '')) return new Response(res.body, { status: 200, headers });

    // The size: from the asset's own header, or by reading it once when there's none.
    let size = Number(res.headers.get('Content-Length')) || 0;
    let body = res.body;
    if (!size) {
      const buf = new Uint8Array(await res.arrayBuffer());
      size = buf.byteLength;
      body = new Blob([buf]).stream();
    }
    let start, end;
    if (m[1] === '') {
      start = Math.max(0, size - Number(m[2])); // the last n bytes
      end = size - 1;
    } else {
      start = Number(m[1]);
      end = m[2] === '' ? size - 1 : Math.min(Number(m[2]), size - 1);
    }
    if (start > end || start >= size) {
      await body.cancel();
      return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${size}`, 'Accept-Ranges': 'bytes' } });
    }
    headers.set('Content-Range', `bytes ${start}-${end}/${size}`);
    headers.set('Content-Length', String(end - start + 1));
    return new Response(slice(body, start, end + 1), { status: 206, headers });
  },
};

// The bytes [from, to) of a stream, passed along as views of its own chunks (nothing is copied).
function slice(body, from, to) {
  let at = 0;
  return body.pipeThrough(
    new TransformStream({
      transform(chunk, out) {
        const a = Math.max(from - at, 0);
        const b = Math.min(to - at, chunk.byteLength);
        at += chunk.byteLength;
        if (b > a) out.enqueue(chunk.subarray(a, b));
        if (at >= to) out.terminate();
      },
    }),
  );
}
