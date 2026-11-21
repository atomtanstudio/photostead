// The service worker's side of a page handing over files it downloaded (content/sites/common.js,
// saveWithFiles): "file" starts one (by its index in the post), "chunk"s carry its bytes in base64.
const LIMIT = 2 * 1024 ** 3; // 2 GB in all: more than any post's files

export class FileReceiver {
  constructor() { this.parts = new Map(); this.types = new Map(); this.received = 0; }

  // Takes one message; false once the files are too large to be a post's.
  add(message) {
    if (message.type === 'file') {
      this.types.set(message.index, String(message.contentType || ''));
      this.parts.set(message.index, []);
    } else if (message.type === 'chunk') {
      const text = atob(String(message.data || ''));
      const bytes = new Uint8Array(text.length);
      for (let i = 0; i < text.length; i++) bytes[i] = text.charCodeAt(i);
      this.received += bytes.length;
      if (this.received > LIMIT) return false;
      this.parts.get(message.index)?.push(bytes);
    }
    return true;
  }

  // The files for a plan's items, by item index; an item the page sent nothing for is left out
  // (it's downloaded the usual way).
  files(items) {
    const out = new Map();
    for (const item of items) {
      const parts = this.parts.get(item.index);
      if (!parts) continue;
      const type = this.types.get(item.index)?.split(';')[0] || (item.video ? 'video/mp4' : 'image/jpeg');
      out.set(item.index, new Blob(parts, { type }));
    }
    return out;
  }
}
