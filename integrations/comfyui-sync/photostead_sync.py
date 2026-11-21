#!/usr/bin/env python3
"""Copy finished ComfyUI images and videos into Photostead. Source files are never changed.

Each settled file is hashed (SHA-256); Photostead is asked which it already has, and only the rest
are sent, filed by day in the photostead share with the file's time as its date. What was sent is
kept in a small SQLite state.
"""
import argparse
import datetime as dt
import fcntl
import glob
import hashlib
import json
import logging
import mimetypes
import os
from pathlib import Path
import signal
import sqlite3
import stat
import subprocess
import tempfile
import threading
import time
import uuid
from urllib.parse import urlsplit

import requests
from PIL import Image

IMAGES = {'.png', '.jpg', '.jpeg', '.webp', '.gif', '.avif', '.heic', '.heif', '.tif', '.tiff', '.bmp'}
VIDEOS = {'.mp4', '.mov', '.mkv', '.webm', '.avi', '.m4v', '.mpg', '.mpeg', '.mts', '.m2ts', '.3gp'}
LOG = logging.getLogger('comfy-photostead')
# Folders that never hold outputs: code that apps keep beside them (a .d.mts file isn't a video).
NEVER = {'node_modules', '__pycache__', 'site-packages'}


def load_env(path):
    """Literal dotenv values only; never execute an env file as shell code."""
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        key, value = line.removeprefix('export ').split('=', 1)
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        os.environ.setdefault(key.strip(), value)


def signature(path):
    s = path.stat(follow_symlinks=False)
    if not stat.S_ISREG(s.st_mode):
        raise ValueError('Not a regular file')
    return f'{s.st_dev}:{s.st_ino}:{s.st_size}:{s.st_mtime_ns}:{s.st_ctime_ns}'


def media_type(path):
    """Checks a file is a whole image or video before it's sent (ComfyUI may still be writing)."""
    if path.suffix.lower() in IMAGES:
        with Image.open(path) as image:
            mime = Image.MIME.get(image.format, 'application/octet-stream')
            image.verify()
        with Image.open(path) as image:
            image.load()
        return 'image', mime
    probe = subprocess.run(
        ['ffprobe', '-v', 'error', '-show_streams', '-show_format', '-of', 'json', str(path)],
        capture_output=True, timeout=90, check=True,
    )
    data = json.loads(probe.stdout)
    if not any(s.get('codec_type') == 'video' for s in data.get('streams', [])):
        raise ValueError('No video stream')
    return 'video', mimetypes.guess_type(path.name)[0] or 'application/octet-stream'


def sha256_of(path):
    digest = hashlib.sha256()
    with path.open('rb') as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


class PhotosteadError(RuntimeError):
    def __init__(self, method, path, status, detail=''):
        self.status = status
        super().__init__(f'Photostead {method} {path}: HTTP {status}{": " + detail if detail else ""}')


class Photostead:
    def __init__(self, url, key, album):
        self.url = url.rstrip('/')
        parsed = urlsplit(self.url)
        if parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError('Invalid Photostead URL')
        if not key.startswith('psk_'):
            raise ValueError('PHOTOSTEAD_API_KEY should be a key from `photostead apikey add` (psk_…)')
        self.session = requests.Session()
        self.session.trust_env = False
        self.session.headers.update({'Authorization': f'Bearer {key}', 'Accept': 'application/json'})
        self.album = album

    def request(self, method, path, **kwargs):
        response = self.session.request(method, self.url + path, timeout=(15, 600), allow_redirects=False, **kwargs)
        if not 200 <= response.status_code < 300:
            status = response.status_code
            try:
                detail = str(response.json().get('detail', ''))[:200]
            except ValueError:
                detail = ''
            response.close()
            raise PhotosteadError(method, path, status, detail)
        return response

    def known(self, digests):
        """Which of these files Photostead already has (deleted ones included). Those are put in the
        album too, as an upload would be: another copy may have reached Photostead first."""
        out = set()
        for i in range(0, len(digests), 1000):
            with self.request('POST', '/api/v1/uploads/check', json={'sha256': digests[i:i + 1000], 'album': self.album}) as response:
                out.update(response.json().get('known') or [])
        return out

    def upload(self, path, mime, digest, taken):
        """Sends one file with its date and album; Photostead answers with the SHA-256 it received."""
        boundary = uuid.uuid4().hex
        meta = {'fileName': path.name, 'takenAt': taken, 'album': self.album}
        with tempfile.TemporaryFile() as body:
            body.write(f'--{boundary}\r\nContent-Disposition: form-data; name="meta"\r\nContent-Type: application/json\r\n\r\n'.encode())
            body.write(json.dumps(meta).encode())
            filename = path.name.replace('"', '%22').replace('\r', '%0D').replace('\n', '%0A')
            body.write(f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\nContent-Type: {mime}\r\n\r\n'.encode())
            with path.open('rb') as source:
                while chunk := source.read(1024 * 1024):
                    body.write(chunk)
            body.write(f'\r\n--{boundary}--\r\n'.encode())
            body.seek(0)
            with self.request('POST', '/api/v1/uploads', data=body,
                              headers={'Content-Type': f'multipart/form-data; boundary={boundary}'}) as response:
                result = response.json()
        if result.get('sha256') != digest:
            raise ValueError('Photostead received different bytes; the file stays queued')
        return result


class Sync:
    def __init__(self, db_path, roots, photostead, settle=60, since=0, skip_dirs=()):
        self.roots = roots
        self.skip_dirs = set(skip_dirs)  # folder names never copied, wherever they are (H3LIX keeps its own library)
        self.photostead = photostead
        self.settle = settle
        self.since = since
        self.api_unavailable = False
        self.state_dir = Path(db_path).parent
        self.state_dir.mkdir(parents=True, exist_ok=True)
        self.db = sqlite3.connect(db_path)
        self.db.row_factory = sqlite3.Row
        self.db.executescript('''
          PRAGMA journal_mode=WAL;
          CREATE TABLE IF NOT EXISTS files (
            path TEXT PRIMARY KEY, signature TEXT NOT NULL, stable_since REAL NOT NULL,
            status TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0,
            next_attempt REAL NOT NULL DEFAULT 0, error TEXT, sha256 TEXT, outcome TEXT,
            media_type TEXT, bytes INTEGER, done_at REAL
          );
          CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
        ''')

    def discover(self):
        now = time.time()
        count = 0
        roots = sorted({Path(p).resolve() for pattern in self.roots for p in glob.glob(pattern) if Path(p).is_dir()})
        if not roots:
            raise RuntimeError('No Comfy output folders found')
        for root in roots:
            for directory, subdirs, names in os.walk(root, followlinks=False):
                subdirs[:] = [d for d in subdirs if not d.startswith('.') and d not in self.skip_dirs and d not in NEVER
                              and not Path(directory, d).is_symlink()]
                for name in names:
                    path = Path(directory, name)
                    if name.startswith('.') or path.suffix.lower() not in IMAGES | VIDEOS or path.is_symlink():
                        continue
                    try:
                        sig = signature(path)
                        modified = path.stat().st_mtime
                    except (OSError, ValueError):
                        continue
                    count += 1
                    row = self.db.execute('SELECT signature FROM files WHERE path=?', (str(path),)).fetchone()
                    if row is None or row['signature'] != sig:
                        self.db.execute('''INSERT OR REPLACE INTO files (path, signature, stable_since, status)
                          VALUES (?, ?, ?, ?)''', (str(path), sig, now, 'skipped' if modified < self.since else 'pending'))
        self.db.execute('INSERT OR REPLACE INTO meta VALUES (?, ?)', ('last_scan', str(now)))
        self.db.execute('INSERT OR REPLACE INTO meta VALUES (?, ?)', ('roots', json.dumps([str(p) for p in roots])))
        self.db.commit()
        return count

    def ready(self, limit=100):
        now = time.time()
        return self.db.execute('''SELECT * FROM files WHERE status IN ('pending', 'retry')
          AND stable_since <= ? AND next_attempt <= ?
          ORDER BY stable_since DESC, path LIMIT ?''', (now - self.settle, now, limit)).fetchall()

    def done(self, path, sig, digest, outcome, kind, size):
        self.db.execute('''UPDATE files SET status='done', sha256=?, outcome=?, media_type=?, bytes=?, done_at=?, error=NULL
          WHERE path=? AND signature=?''', (digest, outcome, kind, size, time.time(), str(path), sig))
        self.db.commit()

    def retry(self, row, exc):
        self.api_unavailable = isinstance(exc, requests.RequestException) or (
            isinstance(exc, PhotosteadError) and (exc.status in (401, 403, 429) or exc.status >= 500))
        attempts = row['attempts'] + 1
        error = str(exc)[:400] if isinstance(exc, (ValueError, RuntimeError)) else type(exc).__name__
        self.db.execute('''UPDATE files SET status='retry', attempts=?, next_attempt=?, error=? WHERE path=?''',
                        (attempts, time.time() + min(3600, 30 * 2 ** min(attempts - 1, 7)), error, row['path']))
        self.db.commit()
        LOG.warning('Retained for retry: %s (%s)', row['path'], error)

    def process(self, rows):
        """Hashes a batch, asks Photostead which it has, and sends the rest."""
        batch = []
        for row in rows:
            path = Path(row['path'])
            try:
                sig = signature(path)
                if sig != row['signature'] or path.stat().st_mtime > time.time() - self.settle:
                    continue  # still changing: the next scan sees it again
                kind, mime = media_type(path)
                digest = sha256_of(path)
                if signature(path) != sig:
                    continue
                batch.append((row, path, sig, kind, mime, digest, path.stat().st_size))
            except Exception as exc:
                self.retry(row, exc)
        if not batch:
            return
        try:
            known = self.photostead.known([b[5] for b in batch])
        except Exception as exc:
            for b in batch:
                self.retry(b[0], exc)
            return
        for row, path, sig, kind, mime, digest, size in batch:
            if self.api_unavailable:
                break
            try:
                if digest in known:
                    self.done(path, sig, digest, 'already there', kind, size)
                    continue
                taken = dt.datetime.fromtimestamp(path.stat().st_mtime, dt.timezone.utc).isoformat()
                result = self.photostead.upload(path, mime, digest, taken)
                if signature(path) != sig:
                    continue
                self.done(path, sig, digest, result.get('status', 'sent'), kind, size)
                LOG.info('%s %s', 'Already present' if result.get('status') == 'duplicate' else 'Uploaded', path)
            except Exception as exc:
                self.retry(row, exc)

    def status(self):
        return {'counts': dict(self.db.execute('SELECT status, count(*) FROM files GROUP BY status').fetchall()),
                'outcomes': dict(self.db.execute("SELECT outcome, count(*) FROM files WHERE status='done' GROUP BY outcome").fetchall()),
                'media': dict(self.db.execute("SELECT media_type, count(*) FROM files WHERE status='done' GROUP BY media_type").fetchall()),
                'bytes': self.db.execute("SELECT coalesce(sum(bytes),0) FROM files WHERE status='done'").fetchone()[0],
                'meta': dict(self.db.execute('SELECT * FROM meta').fetchall()),
                'errors': [dict(r) for r in self.db.execute("SELECT path, attempts, error FROM files WHERE status='retry' LIMIT 20")]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--env-file')
    parser.add_argument('--once', action='store_true', help='Scan and process currently settled files once')
    parser.add_argument('--status', action='store_true', help='Read local progress without calling Photostead')
    parser.add_argument('--limit', type=int, default=100, help='Maximum files per pass')
    args = parser.parse_args()
    if args.env_file:
        load_env(args.env_file)
    os.umask(0o077)
    logging.basicConfig(level=logging.INFO, format='%(asctime)s %(levelname)s %(message)s')
    state = Path(os.environ.get('PHOTOSTEAD_STATE_DIR', '/srv/ai/comfy-photostead/state'))
    roots = json.loads(os.environ.get('COMFY_OUTPUT_GLOBS', '["/srv/ai/comfyui/profiles/*/output", "/srv/ai/research/*/profile/output"]'))
    skip_dirs = [d.strip() for d in os.environ.get('COMFY_SKIP_DIRS', '').split(',') if d.strip()]
    sync = Sync(state / 'sync.sqlite3', roots, None, float(os.environ.get('PHOTOSTEAD_SETTLE_SECONDS', '60')),
                float(os.environ.get('PHOTOSTEAD_SINCE', '0')), skip_dirs)
    if args.status:
        print(json.dumps(sync.status(), indent=2))
        return
    lock = open(state / 'sync.lock', 'a')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise SystemExit('Another sync already holds the state lock')
    sync.photostead = Photostead(os.environ['PHOTOSTEAD_URL'], os.environ['PHOTOSTEAD_API_KEY'],
                                 os.environ.get('PHOTOSTEAD_ALBUM', 'ComfyUI Outputs'))
    destination = json.dumps([sync.photostead.url, sync.photostead.album])
    bound = sync.db.execute("SELECT value FROM meta WHERE key='destination'").fetchone()
    if bound and bound[0] != destination:
        raise SystemExit('State belongs to another destination; use a separate PHOTOSTEAD_STATE_DIR')
    sync.db.execute('INSERT OR REPLACE INTO meta VALUES (?, ?)', ('destination', destination))
    sync.db.commit()
    stop = threading.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, lambda *_: stop.set())
    poll = float(os.environ.get('PHOTOSTEAD_POLL_SECONDS', '20'))
    while not stop.is_set():
        rows = []
        sync.api_unavailable = False
        try:
            sync.discover()
            rows = sync.ready(args.limit)
            if rows:
                sync.process(rows)
            sync.db.execute('INSERT OR REPLACE INTO meta VALUES (?, ?)', ('last_pass', str(time.time())))
            sync.db.commit()
        except Exception as exc:
            LOG.error('Scan/API unavailable; retrying: %s', str(exc)[:300] if isinstance(exc, (ValueError, RuntimeError)) else type(exc).__name__)
            sync.api_unavailable = True
            if args.once:
                raise SystemExit(1)
        if args.once:
            print(json.dumps(sync.status(), indent=2))
            break
        stop.wait(0 if len(rows) == args.limit and not sync.api_unavailable else poll)


if __name__ == '__main__':
    main()
